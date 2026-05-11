package deposit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/shopspring/decimal"

	walletconfig "monera-digital/internal/wallet/config"
)

// AlertFunc fires on MANUAL_REVIEW / FAILED branches. Implementations push to
// Feishu + email; called outside the DB tx so failures don't roll back the
// deposit state. nil is allowed (no-op).
type AlertFunc func(level, title string, fields map[string]string)

// SerialNoFunc generates a journal serial_no. Injectable for deterministic tests.
type SerialNoFunc func() string

// Service runs the SPEC §6.4 state machine inside a single DB transaction.
type Service struct {
	repo         Repository
	registry     ChainsRegistry
	alertFn      AlertFunc
	serialFn     SerialNoFunc
	allowedTypes map[string]bool
}

// ChainsRegistry is the narrow Registry view the deposit Service needs.
type ChainsRegistry interface {
	GetCoinChainBySafeheronKey(key string) (*walletconfig.CoinChain, bool)
}

// NewService wires the deposit state machine. registry/alertFn may be nil — the
// Service still routes events but degrades gracefully.
func NewService(repo Repository, reg ChainsRegistry, alertFn AlertFunc) *Service {
	return &Service{
		repo:     repo,
		registry: reg,
		alertFn:  alertFn,
		serialFn: defaultSerialNo,
		allowedTypes: map[string]bool{
			"TRANSACTION_CREATED":        true,
			"TRANSACTION_STATUS_CHANGED": true,
		},
	}
}

// SetSerialFunc overrides the journal serial generator (tests only).
func (s *Service) SetSerialFunc(fn SerialNoFunc) {
	if fn != nil {
		s.serialFn = fn
	}
}

// ProcessOne locks one PENDING event, runs the state machine, commits the tx.
// Returns processed=true when an event was handled (caller should immediately
// call ProcessOne again to drain the queue), processed=false when the queue is
// empty (caller should sleep).
func (s *Service) ProcessOne(ctx context.Context) (processed bool, err error) {
	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return false, fmt.Errorf("begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	evt, err := s.repo.LockNextPendingEvent(ctx, tx)
	if err != nil {
		if errors.Is(err, ErrNoPending) {
			return false, nil
		}
		return false, fmt.Errorf("lock event: %w", err)
	}

	var alerts []alertPayload
	if procErr := s.processEvent(ctx, tx, evt, &alerts); procErr != nil {
		// Mark event as ERROR + reset balances. Best-effort: if MarkEventError
		// itself fails we let the worker retry on next tick (the tx rolls
		// back, the event stays PENDING).
		if markErr := s.repo.MarkEventError(ctx, tx, evt.ID, procErr.Error()); markErr != nil {
			return true, fmt.Errorf("process event (and mark-error failed: %v): %w", markErr, procErr)
		}
		if err := tx.Commit(); err != nil {
			return true, fmt.Errorf("commit error state: %w", err)
		}
		committed = true
		s.fireAlerts(alerts)
		return true, procErr
	}

	if err := s.repo.MarkEventDone(ctx, tx, evt.ID); err != nil {
		return true, fmt.Errorf("mark event done: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return true, fmt.Errorf("commit: %w", err)
	}
	committed = true
	s.fireAlerts(alerts)
	return true, nil
}

type alertPayload struct {
	level  string
	title  string
	fields map[string]string
}

func (s *Service) fireAlerts(alerts []alertPayload) {
	if s.alertFn == nil {
		return
	}
	for _, a := range alerts {
		s.alertFn(a.level, a.title, a.fields)
	}
}

func (s *Service) processEvent(ctx context.Context, tx Tx, evt *Event, alerts *[]alertPayload) error {
	var env PayloadEnvelope
	if err := json.Unmarshal(evt.RawPayload, &env); err != nil {
		return fmt.Errorf("unmarshal raw_payload: %w", err)
	}

	// Early-exit filters (mark DONE, no deposit row).
	if !s.allowedTypes[env.EventType] {
		log.Printf("deposit worker: skipping eventType=%s eventID=%s", env.EventType, evt.EventID)
		return nil
	}
	d := env.EventDetail
	if d.TransactionDirection != "INFLOW" {
		log.Printf("deposit worker: skipping direction=%s eventID=%s", d.TransactionDirection, evt.EventID)
		return nil
	}

	// Route the event.
	userID, found, err := s.repo.LookupAddressOwner(ctx, d.DestinationAddress)
	if err != nil {
		return fmt.Errorf("lookup address owner: %w", err)
	}
	if !found {
		return s.flagManualReview(ctx, tx, evt, &d, 0, "", 0, ReasonAddressUnassigned, alerts)
	}

	var coinChain *walletconfig.CoinChain
	if s.registry != nil {
		if cc, ok := s.registry.GetCoinChainBySafeheronKey(d.CoinKey); ok {
			coinChain = cc
		}
	}
	if coinChain == nil {
		return s.flagManualReview(ctx, tx, evt, &d, userID, "", 0, ReasonCoinUnsupported, alerts)
	}

	amount, err := decimal.NewFromString(d.TxAmount)
	if err != nil {
		return fmt.Errorf("parse txAmount %q: %w", d.TxAmount, err)
	}
	minAmount, _ := decimal.NewFromString(coinChain.MinDepositAmount)
	if amount.LessThan(minAmount) {
		var symbol string
		if coinChain.Coin != nil {
			symbol = coinChain.Coin.Symbol
		}
		return s.flagManualReview(ctx, tx, evt, &d, userID, symbol, coinChain.ID, ReasonBelowMinAmount, alerts)
	}

	var coinSymbol string
	if coinChain.Coin != nil {
		coinSymbol = coinChain.Coin.Symbol
	}

	// UPSERT deposits with status_rank guard.
	row := &DepositRow{
		UserID:             userID,
		SafeheronTxKey:     d.TxKey,
		Amount:             d.TxAmount,
		Asset:              coinSymbol,
		ChainCode:          coinChain.ChainCode,
		CoinChainID:        coinChain.ID,
		SafeheronStatus:    d.TransactionStatus,
		SafeheronSubStatus: d.TransactionSubStatus,
		StatusRank:         StatusRank(d.TransactionStatus),
		BlockHeight:        d.BlockHeight,
		BlockHash:          d.BlockHash,
		Status:             DepositStatusPending,
		FromAddress:        d.SourceAddress,
		ToAddress:          d.DestinationAddress,
		TxHash:             d.TxHash,
	}
	dep, err := s.repo.UpsertDeposit(ctx, tx, row)
	if err != nil {
		return fmt.Errorf("upsert deposit: %w", err)
	}

	// Credit branch: COMPLETED + CONFIRMED + still PENDING.
	if d.TransactionStatus == "COMPLETED" &&
		d.TransactionSubStatus == "CONFIRMED" &&
		dep.Status == DepositStatusPending {

		accountID, _, err := s.repo.FindOrCreateAccountForUpdate(ctx, tx, userID, coinSymbol)
		if err != nil {
			return fmt.Errorf("lock account: %w", err)
		}
		newBalance, err := s.repo.CreditAccount(ctx, tx, accountID, d.TxAmount)
		if err != nil {
			return fmt.Errorf("credit account: %w", err)
		}
		if err := s.repo.WriteJournal(ctx, tx, &JournalEntry{
			SerialNo:        s.serialFn(),
			UserID:          int64(userID),
			AccountID:       accountID,
			Amount:          d.TxAmount,
			BalanceSnapshot: newBalance,
			BizType:         JournalBizTypeDeposit,
			RefID:           dep.ID,
		}); err != nil {
			return fmt.Errorf("write journal: %w", err)
		}
		if err := s.repo.MarkDepositCredited(ctx, tx, dep.ID); err != nil {
			return fmt.Errorf("mark credited: %w", err)
		}
		return nil
	}

	// Failed terminal branch.
	if isFailedStatus(d.TransactionStatus) && dep.Status != DepositStatusCredited && dep.Status != DepositStatusFailed {
		if err := s.repo.MarkDepositFailed(ctx, tx, dep.ID, d.TransactionSubStatus); err != nil {
			return fmt.Errorf("mark failed: %w", err)
		}
		*alerts = append(*alerts, alertPayload{
			level: "WARN",
			title: "Deposit failed",
			fields: map[string]string{
				"userId":            fmt.Sprintf("%d", userID),
				"txKey":             d.TxKey,
				"amount":            d.TxAmount,
				"symbol":            coinSymbol,
				"transactionStatus": d.TransactionStatus,
				"reason":            d.TransactionSubStatus,
			},
		})
		return nil
	}

	// Intermediate state (CONFIRMING, etc.) — deposit row updated, nothing else to do.
	return nil
}

func (s *Service) flagManualReview(
	ctx context.Context,
	tx Tx,
	evt *Event,
	d *PayloadEventDetail,
	userID int,
	symbol string,
	coinChainID int,
	reason string,
	alerts *[]alertPayload,
) error {
	// Insert a placeholder deposits row so the operator UI has something to
	// link to. user_id may be 0 when the address is unassigned — that's the
	// MANUAL_REVIEW signal the operator dashboard filters on.
	row := &DepositRow{
		UserID:             userID,
		SafeheronTxKey:     d.TxKey,
		Amount:             d.TxAmount,
		Asset:              symbol,
		ChainCode:          d.CoinKey, // best-effort when registry miss
		CoinChainID:        coinChainID,
		SafeheronStatus:    d.TransactionStatus,
		SafeheronSubStatus: d.TransactionSubStatus,
		StatusRank:         StatusRank(d.TransactionStatus),
		BlockHeight:        d.BlockHeight,
		BlockHash:          d.BlockHash,
		Status:             DepositStatusManualReview,
		FromAddress:        d.SourceAddress,
		ToAddress:          d.DestinationAddress,
		TxHash:             d.TxHash,
	}
	dep, err := s.repo.UpsertDeposit(ctx, tx, row)
	if err != nil {
		return fmt.Errorf("upsert manual_review deposit: %w", err)
	}
	if err := s.repo.MarkDepositManualReview(ctx, tx, dep.ID, reason); err != nil {
		return fmt.Errorf("mark manual_review: %w", err)
	}
	*alerts = append(*alerts, alertPayload{
		level: "ERROR",
		title: "Deposit manual review",
		fields: map[string]string{
			"reason":             reason,
			"eventId":            evt.EventID,
			"userId":             fmt.Sprintf("%d", userID),
			"destinationAddress": d.DestinationAddress,
			"amount":             d.TxAmount,
			"coinKey":            d.CoinKey,
			"txKey":              d.TxKey,
		},
	})
	return nil
}

func isFailedStatus(s string) bool {
	switch s {
	case "FAILED", "CANCELLED", "REJECTED":
		return true
	}
	return false
}

// defaultSerialNo formats a millisecond-precision timestamp. Good enough for
// the journal serial_no which only needs uniqueness within the table.
func defaultSerialNo() string {
	return fmt.Sprintf("DPS%d", time.Now().UnixNano())
}
