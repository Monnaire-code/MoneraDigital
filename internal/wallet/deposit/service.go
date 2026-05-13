package deposit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/shopspring/decimal"

	"monera-digital/internal/safeheron"
	walletconfig "monera-digital/internal/wallet/config"
)

// AlertFunc fires on MANUAL_REVIEW / FAILED branches. Implementations push to
// Feishu + email; called outside the DB tx so failures don't roll back the
// deposit state. nil is allowed (no-op).
type AlertFunc func(level, title string, fields map[string]string)

// SerialNoFunc generates a journal serial_no. Injectable for deterministic tests.
type SerialNoFunc func() string

// KYTClient is the minimal Safeheron interface the deposit Service needs (dependency inversion).
type KYTClient interface {
	KytReport(ctx context.Context, txKey string) (*safeheron.KytReportResponse, error)
}

// ChainsRegistry is the narrow Registry view the deposit Service needs.
type ChainsRegistry interface {
	GetCoinChainBySafeheronKey(key string) (*walletconfig.CoinChain, bool)
}

// Service runs the SPEC §6.4 + §6.5 state machine.
type Service struct {
	repo     Repository
	registry ChainsRegistry
	alertFn  AlertFunc
	serialFn SerialNoFunc
	// KYT fields (v1.5 T10)
	kytEnabled        bool
	safeheronClient   KYTClient
	kytOrphanMaxRetry int
	kytTimeout        time.Duration
}

// NewService wires the deposit state machine. registry/alertFn may be nil — the
// Service still routes events but degrades gracefully.
func NewService(repo Repository, reg ChainsRegistry, alertFn AlertFunc) *Service {
	return &Service{
		repo:     repo,
		registry: reg,
		alertFn:  alertFn,
		serialFn: defaultSerialNo,
	}
}

// SetSerialFunc overrides the journal serial generator (tests only).
func (s *Service) SetSerialFunc(fn SerialNoFunc) {
	if fn != nil {
		s.serialFn = fn
	}
}

// SetKYTDeps injects KYT dependencies (called by container after NewService, before Worker.Run).
func (s *Service) SetKYTDeps(client KYTClient, enabled bool, orphanMaxRetry int, timeout time.Duration) {
	s.safeheronClient = client
	s.kytEnabled = enabled
	if orphanMaxRetry <= 0 {
		orphanMaxRetry = 100
	}
	s.kytOrphanMaxRetry = orphanMaxRetry
	if timeout <= 0 {
		timeout = 20 * time.Minute
	}
	s.kytTimeout = timeout
}

// ProcessOne is the KYT-aware deposit state machine entry (SPEC §6.4 + §6.5).
//
// Three-transaction structure (v1.5, D-33):
//
//	T-α: Lock event → parse/route → UPSERT deposit → if needsKYT && kytEnabled: MoveToKYTPending + COMMIT
//	T-β: KytReport API call (outside any DB transaction)
//	T-γ: UpdateAMLFields → DecideKYT → credit/MR/keep-pending → MarkEventDone + COMMIT
func (s *Service) ProcessOne(ctx context.Context) (processed bool, err error) {
	// ========== T-α START ==========
	tx1, err := s.repo.BeginTx(ctx)
	if err != nil {
		return false, fmt.Errorf("begin tx: %w", err)
	}
	committed1 := false
	defer func() {
		if !committed1 {
			_ = tx1.Rollback()
		}
	}()

	evt, err := s.repo.LockNextPendingEvent(ctx, tx1)
	if err != nil {
		if errors.Is(err, ErrNoPending) {
			return false, nil
		}
		return false, fmt.Errorf("lock event: %w", err)
	}

	var env PayloadEnvelope
	if err := json.Unmarshal(evt.RawPayload, &env); err != nil {
		if markErr := s.repo.MarkEventError(ctx, tx1, evt.ID, err.Error()); markErr != nil {
			return true, fmt.Errorf("%w: mark-error=%v procErr=%v", ErrMarkErrorFailed, markErr, err)
		}
		if cErr := tx1.Commit(); cErr != nil {
			return true, fmt.Errorf("commit error state: %w", cErr)
		}
		committed1 = true
		return true, fmt.Errorf("unmarshal raw_payload: %w", err)
	}

	// Dispatch by EventType
	switch evt.EventType {
	case "AML_KYT_ALERT":
		var w struct {
			EventDetail AMLKYTAlertDetail `json:"eventDetail"`
		}
		if err := json.Unmarshal(evt.RawPayload, &w); err != nil {
			if markErr := s.repo.MarkEventError(ctx, tx1, evt.ID, err.Error()); markErr != nil {
				return true, fmt.Errorf("%w: mark-error=%v procErr=%v", ErrMarkErrorFailed, markErr, err)
			}
			if cErr := tx1.Commit(); cErr != nil {
				return true, fmt.Errorf("commit error state: %w", cErr)
			}
			committed1 = true
			return true, fmt.Errorf("unmarshal AML_KYT_ALERT: %w", err)
		}
		processed, pErr := s.processKYTAlert(ctx, tx1, evt, &w.EventDetail)
		committed1 = true // processKYTAlert owns tx1 lifecycle (commit or rollback)
		return processed, pErr

	case "TRANSACTION_CREATED", "TRANSACTION_STATUS_CHANGED":
		// Fall through to main TRANSACTION processing below

	default:
		if err := s.repo.MarkEventDone(ctx, tx1, evt.ID); err != nil {
			return true, fmt.Errorf("mark event done: %w", err)
		}
		if err := tx1.Commit(); err != nil {
			return true, fmt.Errorf("commit: %w", err)
		}
		committed1 = true
		log.Printf("deposit worker: skipping eventType=%s eventID=%s", env.EventType, evt.EventID)
		return true, nil
	}

	d := env.EventDetail

	// Early-exit: not INFLOW
	if d.TransactionDirection != "INFLOW" {
		if err := s.repo.MarkEventDone(ctx, tx1, evt.ID); err != nil {
			return true, fmt.Errorf("mark event done: %w", err)
		}
		if err := tx1.Commit(); err != nil {
			return true, fmt.Errorf("commit: %w", err)
		}
		committed1 = true
		log.Printf("deposit worker: skipping direction=%s eventID=%s", d.TransactionDirection, evt.EventID)
		return true, nil
	}

	// Route: lookup address owner
	var alerts []alertPayload
	userID, found, err := s.repo.LookupAddressOwner(ctx, d.DestinationAddress)
	if err != nil {
		return true, fmt.Errorf("lookup address owner: %w", err)
	}
	if !found {
		procErr, cErr := s.flagAndFinalize(ctx, tx1, evt, &d, 0, "", "", 0, ReasonAddressUnassigned, &alerts)
		committed1 = cErr == nil
		if cErr != nil {
			return true, cErr
		}
		s.fireAlerts(alerts)
		return true, procErr
	}

	var coinChain *walletconfig.CoinChain
	if s.registry != nil {
		if cc, ok := s.registry.GetCoinChainBySafeheronKey(d.CoinKey); ok {
			coinChain = cc
		}
	}
	if coinChain == nil {
		procErr, cErr := s.flagAndFinalize(ctx, tx1, evt, &d, userID, "", "", 0, ReasonCoinUnsupported, &alerts)
		committed1 = cErr == nil
		if cErr != nil {
			return true, cErr
		}
		s.fireAlerts(alerts)
		return true, procErr
	}

	amount, err := decimal.NewFromString(d.TxAmount)
	if err != nil {
		return true, fmt.Errorf("parse txAmount %q: %w", d.TxAmount, err)
	}
	var symbol string
	if coinChain.Coin != nil {
		symbol = coinChain.Coin.Symbol
	}
	minAmount, err := decimal.NewFromString(coinChain.MinDepositAmount)
	if err != nil {
		procErr, cErr := s.flagAndFinalize(ctx, tx1, evt, &d, userID, coinChain.ChainCode, symbol, coinChain.ID, ReasonInvalidCoinConfig, &alerts)
		committed1 = cErr == nil
		if cErr != nil {
			return true, cErr
		}
		s.fireAlerts(alerts)
		return true, procErr
	}
	if amount.LessThan(minAmount) {
		procErr, cErr := s.flagAndFinalize(ctx, tx1, evt, &d, userID, coinChain.ChainCode, symbol, coinChain.ID, ReasonBelowMinAmount, &alerts)
		committed1 = cErr == nil
		if cErr != nil {
			return true, cErr
		}
		s.fireAlerts(alerts)
		return true, procErr
	}

	// UPSERT deposits with status_rank guard
	row := &DepositRow{
		UserID:             userID,
		SafeheronTxKey:     d.TxKey,
		SafeheronCoinKey:   coinChain.SafeheronCoinKey,
		Amount:             d.TxAmount,
		Asset:              symbol,
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
	dep, err := s.repo.UpsertDeposit(ctx, tx1, row)
	if err != nil {
		upsertErr := fmt.Errorf("upsert deposit: %w", err)
		if markErr := s.repo.MarkEventError(ctx, tx1, evt.ID, upsertErr.Error()); markErr != nil {
			return true, fmt.Errorf("%w: mark-error=%v procErr=%v", ErrMarkErrorFailed, markErr, upsertErr)
		}
		if cErr := tx1.Commit(); cErr != nil {
			return true, fmt.Errorf("commit error state: %w", cErr)
		}
		committed1 = true
		return true, upsertErr
	}

	// KYT initial check trigger condition
	needsKYT := d.TransactionStatus == "COMPLETED" &&
		d.TransactionSubStatus == "CONFIRMED" &&
		dep.Status == DepositStatusPending

	if !needsKYT {
		// Failed terminal branch
		if isFailedStatus(d.TransactionStatus) &&
			dep.Status != DepositStatusCredited &&
			dep.Status != DepositStatusFailed &&
			dep.Status != DepositStatusManualReview {
			if err := s.repo.MarkDepositFailed(ctx, tx1, dep.ID, d.TransactionSubStatus); err != nil {
				return true, fmt.Errorf("mark failed: %w", err)
			}
			alerts = append(alerts, alertPayload{
				level: "WARN",
				title: "Deposit failed",
				fields: map[string]string{
					"userId":            fmt.Sprintf("%d", userID),
					"txKey":             d.TxKey,
					"amount":            d.TxAmount,
					"symbol":            symbol,
					"transactionStatus": d.TransactionStatus,
					"reason":            d.TransactionSubStatus,
				},
			})
		}
		// Intermediate state or already processed — just mark event done
		if err := s.repo.MarkEventDone(ctx, tx1, evt.ID); err != nil {
			return true, fmt.Errorf("mark event done: %w", err)
		}
		if err := tx1.Commit(); err != nil {
			return true, fmt.Errorf("commit: %w", err)
		}
		committed1 = true
		s.fireAlerts(alerts)
		return true, nil
	}

	// KYT_ENABLED=false: direct credit (local/sandbox, D-35)
	if !s.kytEnabled {
		if err := s.creditDepositFromRow(ctx, tx1, dep); err != nil {
			return true, fmt.Errorf("credit deposit: %w", err)
		}
		if err := s.repo.MarkEventDone(ctx, tx1, evt.ID); err != nil {
			return true, fmt.Errorf("mark event done: %w", err)
		}
		if err := tx1.Commit(); err != nil {
			return true, fmt.Errorf("commit: %w", err)
		}
		committed1 = true
		return true, nil
	}

	// KYT_ENABLED=true: move to KYT_PENDING, commit T-α
	if err := s.repo.MoveToKYTPending(ctx, tx1, dep.ID); err != nil {
		return true, fmt.Errorf("move to KYT_PENDING: %w", err)
	}
	if err := tx1.Commit(); err != nil {
		return true, fmt.Errorf("commit T-α: %w", err)
	}
	committed1 = true
	// ========== T-α END (committed, row lock released) ==========

	// ========== T-β START (no DB transaction) ==========
	report, kytErr := s.safeheronClient.KytReport(ctx, d.TxKey)
	if kytErr != nil {
		return s.handleKYTApiFailure(ctx, evt, dep, kytErr)
	}
	// ========== T-β END ==========

	// ========== T-γ START ==========
	tx2, err := s.repo.BeginTx(ctx)
	if err != nil {
		return true, fmt.Errorf("begin tx T-γ: %w", err)
	}
	committed2 := false
	defer func() {
		if !committed2 {
			_ = tx2.Rollback()
		}
	}()

	if err := s.writeAMLFields(ctx, tx2, dep.ID, report.AmlScreeningTriggeredState, report.AmlList); err != nil {
		return true, fmt.Errorf("update AML fields: %w", err)
	}

	// Re-verify deposit is still KYT_PENDING (defense-in-depth for horizontal scaling)
	freshDep, found, err := s.repo.FindDepositByTxKey(ctx, tx2, dep.SafeheronTxKey)
	if err != nil || !found {
		return true, fmt.Errorf("re-read deposit T-γ: found=%v err=%w", found, err)
	}
	if freshDep.Status != DepositStatusKYTPending {
		if err := s.repo.MarkEventDone(ctx, tx2, evt.ID); err != nil {
			return true, fmt.Errorf("mark event done T-γ stale: %w", err)
		}
		if err := tx2.Commit(); err != nil {
			return true, fmt.Errorf("commit T-γ stale: %w", err)
		}
		committed2 = true
		return true, nil
	}

	decision := DecideKYT(report.AmlScreeningTriggeredState, report.AmlList, false)

	switch decision.Action {
	case KytActionCredit:
		if err := s.creditDepositFromRow(ctx, tx2, freshDep); err != nil {
			return true, fmt.Errorf("credit deposit T-γ: %w", err)
		}
	case KytActionKeepPending:
		// Stay in KYT_PENDING — only AML fields updated
	case KytActionManualReview:
		if err := s.repo.MarkDepositManualReview(ctx, tx2, dep.ID, decision.Reason); err != nil {
			return true, fmt.Errorf("mark manual review T-γ: %w", err)
		}
		alerts = append(alerts, alertPayload{
			level: decision.AlertLevel,
			title: "KYT manual review",
			fields: map[string]string{
				"depositId": fmt.Sprintf("%d", dep.ID),
				"txKey":     dep.SafeheronTxKey,
				"riskLevel": decision.RiskLevel,
				"reason":    decision.Reason,
			},
		})
	}

	if err := s.repo.MarkEventDone(ctx, tx2, evt.ID); err != nil {
		return true, fmt.Errorf("mark event done T-γ: %w", err)
	}
	if err := tx2.Commit(); err != nil {
		return true, fmt.Errorf("commit T-γ: %w", err)
	}
	committed2 = true
	// ========== T-γ END ==========

	s.fireAlerts(alerts)
	return true, nil
}

// handleKYTApiFailure handles T-β KYT API call failure.
// Returns (processed=true, err) so worker continues draining.
func (s *Service) handleKYTApiFailure(ctx context.Context, evt *Event, dep *DepositRow, kytErr error) (bool, error) {
	log.Printf("KYT API failed: txKey=%s err=%v attempts=%d", dep.SafeheronTxKey, kytErr, evt.ProcessAttempts+1)

	if err := s.repo.IncrementEventAttemptsNoTx(ctx, evt.ID); err != nil {
		log.Printf("IncrementEventAttempts failed: %v", err)
	}

	if evt.ProcessAttempts+1 < s.kytOrphanMaxRetry {
		// Wrap so the worker can yield to its ticker — prevents a tight retry
		// loop during a Safeheron outage (T10-I-3).
		return true, fmt.Errorf("%w: %v", ErrKYTAPIBackoff, kytErr)
	}

	// Exceeded retry limit — force MANUAL_REVIEW
	tx3, err := s.repo.BeginTx(ctx)
	if err != nil {
		return true, fmt.Errorf("begin tx KYT failure: %w", err)
	}
	committed3 := false
	defer func() {
		if !committed3 {
			_ = tx3.Rollback()
		}
	}()

	if err := s.repo.MarkDepositManualReview(ctx, tx3, dep.ID, ReasonKytApiFailed); err != nil {
		return true, fmt.Errorf("mark manual review KYT failure: %w", err)
	}
	if err := s.repo.MarkEventDone(ctx, tx3, evt.ID); err != nil {
		return true, fmt.Errorf("mark event done KYT failure: %w", err)
	}
	if err := tx3.Commit(); err != nil {
		return true, fmt.Errorf("commit KYT failure: %w", err)
	}
	committed3 = true

	s.fireAlerts([]alertPayload{{
		level: "ERROR",
		title: "KYT API failed after retries",
		fields: map[string]string{
			"depositId": fmt.Sprintf("%d", dep.ID),
			"txKey":     dep.SafeheronTxKey,
			"attempts":  fmt.Sprintf("%d", evt.ProcessAttempts+1),
		},
	}})
	return true, nil
}

const maxAMLListEntries = 50

func (s *Service) writeAMLFields(ctx context.Context, tx Tx, depID int64, state string, amlList []safeheron.AmlReport) error {
	if len(amlList) > maxAMLListEntries {
		amlList = amlList[:maxAMLListEntries]
	}
	amlListJSON, err := json.Marshal(amlList)
	if err != nil {
		return fmt.Errorf("marshal amlList: %w", err)
	}
	return s.repo.UpdateAMLFields(ctx, tx, depID,
		state, SummarizeRiskLevel(amlList), maxLastUpdateTime(amlList), amlListJSON)
}

// creditDepositFromRow is the shared credit helper for T-γ / ScanKYTTimeouts / processKYTAlert.
// Caller must hold a FOR UPDATE lock on the deposit row.
func (s *Service) creditDepositFromRow(ctx context.Context, tx Tx, dep *DepositRow) error {
	accountID, _, err := s.repo.FindOrCreateAccountForUpdate(ctx, tx, dep.UserID, dep.Asset)
	if err != nil {
		return fmt.Errorf("lock account: %w", err)
	}
	newBalance, err := s.repo.CreditAccount(ctx, tx, accountID, dep.Amount)
	if err != nil {
		return fmt.Errorf("credit account: %w", err)
	}
	if err := s.repo.WriteJournal(ctx, tx, &JournalEntry{
		SerialNo:        s.serialFn(),
		UserID:          int64(dep.UserID),
		AccountID:       accountID,
		Amount:          dep.Amount,
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

// processKYTAlert handles AML_KYT_ALERT webhook events (T10.6).
// The caller's tx holds a FOR UPDATE lock on the event row from LockNextPendingEvent;
// any IncrementEventAttemptsNoTx call must run AFTER that tx is closed (commit or
// rollback) — otherwise a fresh-connection UPDATE on the same event row will
// self-deadlock waiting for the lock-holder (this goroutine) to release it.
func (s *Service) processKYTAlert(ctx context.Context, tx Tx, evt *Event, alert *AMLKYTAlertDetail) (bool, error) {
	txClosed := false
	defer func() {
		if !txClosed {
			_ = tx.Rollback()
		}
	}()

	dep, found, err := s.repo.FindDepositByTxKey(ctx, tx, alert.TxKey)
	if err != nil {
		// Release the event-row lock before NoTx increment (C-1 deadlock guard).
		_ = tx.Rollback()
		txClosed = true
		if incErr := s.repo.IncrementEventAttemptsNoTx(ctx, evt.ID); incErr != nil {
			log.Printf("IncrementEventAttempts failed on DB error: %v", incErr)
		}
		return true, fmt.Errorf("find deposit for KYT alert: %w", err)
	}

	if !found {
		// Out-of-order: alert arrived before TRANSACTION_STATUS_CHANGED created the deposit row.
		if evt.ProcessAttempts+1 >= s.kytOrphanMaxRetry {
			processed, err := s.markOrphanAlertDone(ctx, tx, evt)
			if err == nil {
				txClosed = true
			}
			return processed, err
		}
		// Below retry threshold: release row lock first (C-1), then NoTx increment.
		// Leaving the event PENDING is intentional — next worker cycle re-locks it.
		_ = tx.Rollback()
		txClosed = true
		if incErr := s.repo.IncrementEventAttemptsNoTx(ctx, evt.ID); incErr != nil {
			log.Printf("IncrementEventAttempts failed for orphan alert: %v", incErr)
		}
		return true, nil
	}

	amlReports := convertAlertReports(alert.AmlList)

	if err := s.writeAMLFields(ctx, tx, dep.ID, alert.AmlScreeningTriggeredState, amlReports); err != nil {
		return true, fmt.Errorf("update AML fields for alert: %w", err)
	}

	// Only act on KYT_PENDING deposits; terminal states (CREDITED/MANUAL_REVIEW/FAILED) are untouched
	if dep.Status != DepositStatusKYTPending {
		if err := s.repo.MarkEventDone(ctx, tx, evt.ID); err != nil {
			return true, fmt.Errorf("mark event done: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return true, fmt.Errorf("commit: %w", err)
		}
		txClosed = true
		return true, nil
	}

	decision := DecideKYT(alert.AmlScreeningTriggeredState, amlReports, false)

	var alerts []alertPayload
	switch decision.Action {
	case KytActionCredit:
		if err := s.creditDepositFromRow(ctx, tx, dep); err != nil {
			return true, fmt.Errorf("credit deposit KYT alert: %w", err)
		}
	case KytActionKeepPending:
		// Still pending — only AML fields updated
	case KytActionManualReview:
		if err := s.repo.MarkDepositManualReview(ctx, tx, dep.ID, decision.Reason); err != nil {
			return true, fmt.Errorf("mark manual review KYT alert: %w", err)
		}
		alerts = append(alerts, alertPayload{
			level: decision.AlertLevel,
			title: "KYT alert manual review",
			fields: map[string]string{
				"depositId": fmt.Sprintf("%d", dep.ID),
				"txKey":     dep.SafeheronTxKey,
				"riskLevel": decision.RiskLevel,
				"reason":    decision.Reason,
			},
		})
	}

	if err := s.repo.MarkEventDone(ctx, tx, evt.ID); err != nil {
		return true, fmt.Errorf("mark event done: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return true, fmt.Errorf("commit: %w", err)
	}
	txClosed = true
	s.fireAlerts(alerts)
	return true, nil
}

// markOrphanAlertDone handles AML_KYT_ALERT that exceeded retry limit without finding a deposit.
func (s *Service) markOrphanAlertDone(ctx context.Context, tx Tx, evt *Event) (bool, error) {
	if err := s.repo.MarkEventError(ctx, tx, evt.ID, ReasonKytOrphanAlert); err != nil {
		return true, fmt.Errorf("mark orphan alert error: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return true, fmt.Errorf("commit orphan alert: %w", err)
	}
	s.fireAlerts([]alertPayload{{
		level: "ERROR",
		title: "KYT orphan alert exceeded retries",
		fields: map[string]string{
			"eventId":  evt.EventID,
			"txKey":    evt.SafeheronTxKey,
			"attempts": fmt.Sprintf("%d", evt.ProcessAttempts+1),
		},
	}})
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

func (s *Service) flagManualReview(
	ctx context.Context,
	tx Tx,
	evt *Event,
	d *PayloadEventDetail,
	userID int,
	chainCode string,
	symbol string,
	coinChainID int,
	reason string,
	alerts *[]alertPayload,
) error {
	row := &DepositRow{
		UserID:             userID,
		SafeheronTxKey:     d.TxKey,
		SafeheronCoinKey:   d.CoinKey,
		Amount:             d.TxAmount,
		Asset:              symbol,
		ChainCode:          chainCode,
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

// flagAndFinalize calls flagManualReview, marks the event done/error, and commits.
// Returns (procErr, commitOrMarkErr). procErr is the flagManualReview result (nil on
// success). commitOrMarkErr is non-nil only when a DB mark/commit operation itself fails,
// meaning the caller must not set committed=true.
func (s *Service) flagAndFinalize(
	ctx context.Context,
	tx Tx,
	evt *Event,
	d *PayloadEventDetail,
	userID int,
	chainCode string,
	symbol string,
	coinChainID int,
	reason string,
	alerts *[]alertPayload,
) (procErr error, commitOrMarkErr error) {
	procErr = s.flagManualReview(ctx, tx, evt, d, userID, chainCode, symbol, coinChainID, reason, alerts)
	if procErr != nil {
		if markErr := s.repo.MarkEventError(ctx, tx, evt.ID, procErr.Error()); markErr != nil {
			return procErr, fmt.Errorf("%w: mark-error=%v procErr=%v", ErrMarkErrorFailed, markErr, procErr)
		}
	} else {
		if err := s.repo.MarkEventDone(ctx, tx, evt.ID); err != nil {
			return nil, fmt.Errorf("mark event done: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return procErr, fmt.Errorf("commit: %w", err)
	}
	return procErr, nil
}

// ScanKYTTimeouts scans KYT_PENDING deposits that exceeded the timeout threshold (T10.5).
// Processes up to 50 rows per call, each in its own transaction.
func (s *Service) ScanKYTTimeouts(ctx context.Context) {
	const maxPerTick = 50
	for i := 0; i < maxPerTick; i++ {
		if ctx.Err() != nil {
			return
		}
		if err := s.scanOneKYTTimeout(ctx); err != nil {
			if errors.Is(err, ErrNoPending) {
				return
			}
			log.Printf("scan KYT timeout: %v", err)
		}
	}
}

func (s *Service) scanOneKYTTimeout(ctx context.Context) error {
	// Phase 1: lock + read deposit row, then COMMIT (release lock fast)
	tx1, err := s.repo.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("begin tx KYT scan: %w", err)
	}
	dep, err := s.repo.LockOneKYTPendingTimeout(ctx, tx1, s.kytTimeout)
	if err != nil {
		_ = tx1.Rollback()
		if errors.Is(err, ErrNoPending) {
			return ErrNoPending
		}
		return fmt.Errorf("lock KYT timeout: %w", err)
	}
	txKey := dep.SafeheronTxKey
	depID := dep.ID
	if err := tx1.Commit(); err != nil {
		return fmt.Errorf("commit KYT scan phase-1: %w", err)
	}

	// Phase 2: KYT API call outside any DB transaction (mirrors ProcessOne T-β)
	report, kytErr := s.safeheronClient.KytReport(ctx, txKey)
	if kytErr != nil {
		log.Printf("KYT timeout scan API failed: txKey=%s err=%v", txKey, kytErr)
		if mrErr := s.markKYTPendingManualReviewIfStillPending(ctx, txKey, depID, ReasonKytProviderFailedAfterTimeout); mrErr != nil {
			return mrErr
		}
		s.fireAlerts([]alertPayload{{
			level: "ERROR",
			title: "KYT timeout API failure",
			fields: map[string]string{
				"depositId": fmt.Sprintf("%d", depID),
				"txKey":     txKey,
				"error":     kytErr.Error(),
			},
		}})
		return nil
	}

	// Phase 3: write AML fields + decide + credit/MR (new transaction)
	tx2, err := s.repo.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("begin tx KYT scan phase-3: %w", err)
	}
	committed2 := false
	defer func() {
		if !committed2 {
			_ = tx2.Rollback()
		}
	}()

	// C-2 guard: re-read deposit under FOR UPDATE to catch concurrent peers
	// (processKYTAlert / another scanner replica) that already moved this row out
	// of KYT_PENDING. Without this, Phase-3 can double-credit or stomp a CREDITED
	// row back to MANUAL_REVIEW.
	freshDep, found, err := s.repo.FindDepositByTxKey(ctx, tx2, txKey)
	if err != nil {
		return fmt.Errorf("re-read deposit phase-3: %w", err)
	}
	if !found {
		log.Printf("KYT scan phase-3: deposit txKey=%s vanished — skipping", txKey)
		if err := tx2.Commit(); err != nil {
			return fmt.Errorf("commit phase-3 missing: %w", err)
		}
		committed2 = true
		return nil
	}
	if freshDep.Status != DepositStatusKYTPending {
		log.Printf("KYT scan phase-3: deposit txKey=%s status=%s — skipping (concurrent peer handled it)", txKey, freshDep.Status)
		if err := tx2.Commit(); err != nil {
			return fmt.Errorf("commit phase-3 non-pending: %w", err)
		}
		committed2 = true
		return nil
	}

	if err := s.writeAMLFields(ctx, tx2, freshDep.ID, report.AmlScreeningTriggeredState, report.AmlList); err != nil {
		return fmt.Errorf("update AML fields timeout: %w", err)
	}

	decision := DecideKYT(report.AmlScreeningTriggeredState, report.AmlList, true)

	var alerts []alertPayload
	switch decision.Action {
	case KytActionCredit:
		if err := s.creditDepositFromRow(ctx, tx2, freshDep); err != nil {
			return fmt.Errorf("credit deposit timeout: %w", err)
		}
	case KytActionKeepPending:
		// Shouldn't happen with isAfterTimeout=true, but harmless
	case KytActionManualReview:
		if err := s.repo.MarkDepositManualReview(ctx, tx2, freshDep.ID, decision.Reason); err != nil {
			return fmt.Errorf("mark manual review timeout: %w", err)
		}
		alerts = append(alerts, alertPayload{
			level: decision.AlertLevel,
			title: "KYT timeout manual review",
			fields: map[string]string{
				"depositId": fmt.Sprintf("%d", freshDep.ID),
				"txKey":     freshDep.SafeheronTxKey,
				"riskLevel": decision.RiskLevel,
				"reason":    decision.Reason,
			},
		})
	}

	if err := tx2.Commit(); err != nil {
		return fmt.Errorf("commit timeout: %w", err)
	}
	committed2 = true
	s.fireAlerts(alerts)
	return nil
}

// markKYTPendingManualReviewIfStillPending re-reads the deposit under FOR UPDATE
// and only flips KYT_PENDING rows to MANUAL_REVIEW — protects against stomping a
// row a concurrent peer already moved to CREDITED.
func (s *Service) markKYTPendingManualReviewIfStillPending(ctx context.Context, txKey string, depID int64, reason string) error {
	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("begin tx KYT MR guard: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	freshDep, found, err := s.repo.FindDepositByTxKey(ctx, tx, txKey)
	if err != nil {
		return fmt.Errorf("re-read deposit for MR guard: %w", err)
	}
	if !found || freshDep.Status != DepositStatusKYTPending {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit MR guard skip: %w", err)
		}
		committed = true
		return nil
	}
	if err := s.repo.MarkDepositManualReview(ctx, tx, depID, reason); err != nil {
		return fmt.Errorf("mark manual review guarded: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit MR guard: %w", err)
	}
	committed = true
	return nil
}

func convertAlertReports(list []AMLKYTAlertReport) []safeheron.AmlReport {
	out := make([]safeheron.AmlReport, len(list))
	for i, r := range list {
		out[i] = safeheron.AmlReport{
			Provider:       r.Provider,
			Timestamp:      r.Timestamp,
			Status:         r.Status,
			RiskLevel:      r.RiskLevel,
			LastUpdateTime: r.LastUpdateTime,
			Payload:        r.Payload,
		}
	}
	return out
}

func isFailedStatus(s string) bool {
	switch s {
	case "FAILED", "CANCELLED", "REJECTED":
		return true
	}
	return false
}

func defaultSerialNo() string {
	return fmt.Sprintf("DPS%d", time.Now().UnixNano())
}
