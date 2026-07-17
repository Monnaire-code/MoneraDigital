package deposit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// legacyUnsupportedChainIdentity keeps unmapped MANUAL_REVIEW rows compatible
// with the required deposits.chain VARCHAR(50) field. The complete CoinKey is
// retained separately in safeheron_coin_key and the webhook raw_payload.
const legacyUnsupportedChainIdentity = "UNSUPPORTED"

// Tx is the minimal transactional handle the Service threads through its
// repository calls. *sql.Tx satisfies this interface; tests can supply a stub.
type Tx interface {
	Commit() error
	Rollback() error
}

// Repository is the narrow DB surface the deposit Service depends on.
//
// Transactional writes accept a Tx so the Service can run the full SPEC §6.4
// state machine atomically. Methods suffixed NoTx are explicit post-rollback
// finalization paths and must not be called while the event-row lock is held.
type Repository interface {
	InsertEventOrSkip(ctx context.Context, evt *Event) (inserted bool, err error)
	LockNextPendingEvent(ctx context.Context, tx Tx) (*Event, error)
	UpsertDeposit(ctx context.Context, tx Tx, d *DepositRow) (*DepositRow, error)
	FindOrCreateAccountForUpdate(ctx context.Context, tx Tx, userID int, currency string) (accountID int64, balance string, err error)
	CreditAccount(ctx context.Context, tx Tx, accountID int64, amount string) (newBalance string, err error)
	WriteJournal(ctx context.Context, tx Tx, j *JournalEntry) error
	MarkDepositCredited(ctx context.Context, tx Tx, depositID int64) error
	MarkDepositFailed(ctx context.Context, tx Tx, depositID int64, reason string) error
	MarkDepositManualReview(ctx context.Context, tx Tx, depositID int64, reason string) error

	// === AML/KYT (Phase 1 v1.5) ===
	UpdateAMLFields(ctx context.Context, tx Tx, depositID int64, screeningState, riskLevel string, evaluatedAt time.Time, amlListJSON []byte) error
	MoveToKYTPending(ctx context.Context, tx Tx, depositID int64) error
	LockOneKYTPendingTimeout(ctx context.Context, tx Tx, threshold time.Duration) (*DepositRow, error)
	// LockOneAmlPending picks up a KYT_PENDING deposit whose KYT result is still
	// in-flight (aml_risk_level='PENDING') and that has been waiting at least
	// minAge. Pass 0 to skip the time guard (tests / manual backfill).
	LockOneAmlPending(ctx context.Context, tx Tx, minAge time.Duration) (*DepositRow, error)
	FindDepositByTxKey(ctx context.Context, tx Tx, txKey string) (*DepositRow, bool, error)
	IncrementEventAttemptsNoTx(ctx context.Context, eventID int64) error

	MarkEventDone(ctx context.Context, tx Tx, eventID int64) error
	MarkEventError(ctx context.Context, tx Tx, eventID int64, errMsg string) error
	MarkEventErrorNoTx(ctx context.Context, eventID int64, errMsg string) (updated bool, err error)
	LookupAddressOwner(ctx context.Context, address, networkFamily string) (userID int, found bool, err error)
	BeginTx(ctx context.Context) (Tx, error)
}

// DepositRow is the deposit upsert payload.
type DepositRow struct {
	ID                         int64
	UserID                     int
	SafeheronTxKey             string
	SafeheronCoinKey           string // Canonical registry key when mapped; raw webhook key for unsupported evidence.
	Amount                     string
	Asset                      string
	ChainCode                  string // Empty only with CoinChainID=0 on MANUAL_REVIEW; repository persists SQL NULL/NULL.
	CoinChainID                int
	SafeheronStatus            string
	SafeheronSubStatus         string
	StatusRank                 int
	BlockHeight                int64
	BlockHash                  string
	Status                     string
	FromAddress                string
	ToAddress                  string
	TxHash                     string
	AuthorizingRoutingActionID int64
	// AML/KYT fields (v1.5 spec §4.6)
	AMLScreeningState string
	AMLRiskLevel      string
	AMLEvaluatedAt    time.Time
	AMLListJSON       []byte
}

// JournalEntry mirrors account_journal columns the Service writes.
type JournalEntry struct {
	SerialNo        string
	UserID          int64
	AccountID       int64
	Amount          string
	BalanceSnapshot string
	BizType         string // account_journal.biz_type is VARCHAR(32)
	RefID           int64
}

// DBRepository is the postgres implementation.
type DBRepository struct {
	db                             *sql.DB
	transactionClaimsEnabled       bool
	routingProjectionClaimsEnabled bool
}

func NewRepository(db *sql.DB) *DBRepository {
	return &DBRepository{db: db, transactionClaimsEnabled: true, routingProjectionClaimsEnabled: true}
}

func (r *DBRepository) SetRoutingProjectionClaimsEnabled(enabled bool) {
	if r != nil {
		r.routingProjectionClaimsEnabled = enabled
	}
}

func (r *DBRepository) SetTransactionClaimsEnabled(enabled bool) {
	if r != nil {
		r.transactionClaimsEnabled = enabled
	}
}

func (r *DBRepository) BeginTx(ctx context.Context) (Tx, error) {
	return r.db.BeginTx(ctx, nil)
}

// asSQLTx asserts tx is a *sql.Tx (production path). The Service passes the
// Tx it got from BeginTx straight through, so an assertion failure here means
// a bug in the wiring layer — panic loud.
func asSQLTx(tx Tx) *sql.Tx {
	t, ok := tx.(*sql.Tx)
	if !ok {
		panic(fmt.Sprintf("deposit.DBRepository: expected *sql.Tx, got %T", tx))
	}
	return t
}

func (r *DBRepository) InsertEventOrSkip(ctx context.Context, evt *Event) (bool, error) {
	payloadDigest := eventPayloadDigest(evt.RawPayload)
	if evt.PayloadDigest != "" && evt.PayloadDigest != payloadDigest {
		return false, fmt.Errorf("webhook event payload digest does not match raw payload")
	}
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO safeheron_webhook_events
		   (event_id, event_type, safeheron_tx_key, customer_ref_id, raw_payload, payload_digest, process_status)
		 VALUES ($1, $2, $3, $4, $5, $6, 'PENDING')
		 ON CONFLICT (event_id) DO NOTHING`,
		evt.EventID, evt.EventType, evt.SafeheronTxKey, evt.CustomerRefID, evt.RawPayload, payloadDigest,
	)
	if err != nil {
		return false, fmt.Errorf("insert webhook event: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rows affected: %w", err)
	}
	if n > 0 {
		return true, nil
	}
	return r.validateExistingEventPayloadDigest(ctx, evt.EventID, payloadDigest)
}

func eventPayloadDigest(rawPayload []byte) string {
	sum := sha256.Sum256(rawPayload)
	return hex.EncodeToString(sum[:])
}

func (r *DBRepository) LockNextPendingEvent(ctx context.Context, tx Tx) (*Event, error) {
	query := `SELECT id, event_id, event_type,
			        COALESCE(safeheron_tx_key, ''), COALESCE(customer_ref_id, ''),
			        raw_payload, process_status, process_attempts,
			        COALESCE(error_message, ''), COALESCE(authorizing_routing_action_id, 0)
		 FROM safeheron_webhook_events
		 WHERE process_status = 'PENDING'`
	if !r.transactionClaimsEnabled {
		query += ` AND event_type = 'AML_KYT_ALERT'`
		if r.routingProjectionClaimsEnabled {
			query += ` OR (process_status = 'PENDING' AND event_id LIKE 'routing-customer:%'
			  AND EXISTS (
			    SELECT 1 FROM safeheron_transaction_routing_case_actions action
			    JOIN safeheron_transaction_routing_case_commands command ON command.id=action.command_id
			    JOIN safeheron_transaction_routing_cases routing
			      ON routing.id=command.case_id AND routing.pending_command_id=command.id
			    WHERE action.id=safeheron_webhook_events.authorizing_routing_action_id
			      AND action.status IN ('PENDING','RETRYABLE') AND command.status='PENDING'
			  ))`
		}
	}
	query += `
		 ORDER BY received_at
		 FOR UPDATE SKIP LOCKED
		 LIMIT 1`
	row := asSQLTx(tx).QueryRowContext(ctx, query)
	var e Event
	if err := row.Scan(
		&e.ID, &e.EventID, &e.EventType, &e.SafeheronTxKey, &e.CustomerRefID,
		&e.RawPayload, &e.ProcessStatus, &e.ProcessAttempts, &e.ErrorMessage,
		&e.AuthorizingRoutingActionID,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNoPending
		}
		return nil, fmt.Errorf("lock pending event: %w", err)
	}
	return &e, nil
}

func (r *DBRepository) UpsertDeposit(ctx context.Context, tx Tx, d *DepositRow) (*DepositRow, error) {
	chainCodeArg, coinChainIDArg, err := optionalDepositMappingArgs(
		d.ChainCode,
		d.CoinChainID,
		d.Status,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert deposit: %w", err)
	}
	sqlTx := asSQLTx(tx)
	if err := ensureCustomerRoutingAction(ctx, sqlTx, d.AuthorizingRoutingActionID); err != nil {
		return nil, err
	}
	row := sqlTx.QueryRowContext(ctx,
		`INSERT INTO deposits
		   (user_id, tx_hash, amount, asset, chain,
		    safeheron_tx_key, safeheron_coin_key, chain_code, coin_chain_id,
		    safeheron_status, safeheron_sub_status, status_rank,
		    block_height, block_hash, status,
		    from_address, to_address)
		 VALUES
		   ($1, $2, $3::numeric, $4, $5,
		    $6, $7, $8, $9,
		    $10, $11, $12,
		    $13, $14, $15,
		    $16, $17)
		 ON CONFLICT (safeheron_tx_key)
		   WHERE safeheron_tx_key IS NOT NULL
		 DO UPDATE SET
		   safeheron_coin_key   = EXCLUDED.safeheron_coin_key,
		   safeheron_status     = EXCLUDED.safeheron_status,
		   safeheron_sub_status = EXCLUDED.safeheron_sub_status,
		   status_rank          = EXCLUDED.status_rank,
		   block_height         = EXCLUDED.block_height,
		   block_hash           = EXCLUDED.block_hash,
		   amount               = EXCLUDED.amount,
		   tx_hash              = COALESCE(EXCLUDED.tx_hash, deposits.tx_hash),
		   updated_at           = NOW()
		 WHERE deposits.status_rank <= EXCLUDED.status_rank
		 RETURNING id, user_id, COALESCE(safeheron_tx_key, ''),
		           COALESCE(safeheron_coin_key, ''), amount, asset,
		           COALESCE(chain_code, ''), COALESCE(coin_chain_id, 0),
		           COALESCE(safeheron_status, ''), COALESCE(safeheron_sub_status, ''),
		           status_rank, COALESCE(block_height, 0), COALESCE(block_hash, ''),
		           status`,
		d.UserID, nullableTxHash(d.TxHash, d.SafeheronTxKey), d.Amount, d.Asset, depositChainIdentity(d),
		d.SafeheronTxKey, d.SafeheronCoinKey, chainCodeArg, coinChainIDArg,
		d.SafeheronStatus, d.SafeheronSubStatus, d.StatusRank,
		d.BlockHeight, d.BlockHash, d.Status,
		d.FromAddress, d.ToAddress,
	)
	out := &DepositRow{}
	if err := row.Scan(
		&out.ID, &out.UserID, &out.SafeheronTxKey, &out.SafeheronCoinKey,
		&out.Amount, &out.Asset,
		&out.ChainCode, &out.CoinChainID,
		&out.SafeheronStatus, &out.SafeheronSubStatus,
		&out.StatusRank, &out.BlockHeight, &out.BlockHash, &out.Status,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return r.fetchDepositByTxKey(ctx, sqlTx, d.SafeheronTxKey)
		}
		return nil, fmt.Errorf("upsert deposit: %w", err)
	}
	return out, nil
}

func ensureCustomerRoutingAction(ctx context.Context, tx *sql.Tx, actionID int64) error {
	if actionID <= 0 {
		return nil
	}
	var locked int64
	err := tx.QueryRowContext(ctx, `SELECT action.id
FROM safeheron_transaction_routing_case_actions action
JOIN safeheron_transaction_routing_case_commands command ON command.id=action.command_id
JOIN safeheron_transaction_routing_cases routing
  ON routing.id=command.case_id AND routing.pending_command_id=command.id
WHERE action.id=$1 AND action.action_type='APPLY_CUSTOMER'
  AND action.projection_kind='CUSTOMER' AND action.status IN ('PENDING','RETRYABLE')
  AND command.status='PENDING'
FOR UPDATE OF action,command,routing`, actionID).Scan(&locked)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("customer routing action %d is no longer authorized", actionID)
	}
	if err != nil {
		return fmt.Errorf("lock customer routing action %d: %w", actionID, err)
	}
	return nil
}

func (r *DBRepository) fetchDepositByTxKey(ctx context.Context, tx *sql.Tx, txKey string) (*DepositRow, error) {
	row := tx.QueryRowContext(ctx,
		`SELECT id, user_id, COALESCE(safeheron_tx_key, ''),
		        COALESCE(safeheron_coin_key, ''), amount, asset,
		        COALESCE(chain_code, ''), COALESCE(coin_chain_id, 0),
		        COALESCE(safeheron_status, ''), COALESCE(safeheron_sub_status, ''),
		        status_rank, COALESCE(block_height, 0), COALESCE(block_hash, ''),
		        status,
		        COALESCE(from_address, ''), COALESCE(to_address, ''), COALESCE(tx_hash, '')
		 FROM deposits WHERE safeheron_tx_key = $1
		 FOR UPDATE`,
		txKey,
	)
	out := &DepositRow{}
	err := row.Scan(
		&out.ID, &out.UserID, &out.SafeheronTxKey, &out.SafeheronCoinKey,
		&out.Amount, &out.Asset,
		&out.ChainCode, &out.CoinChainID,
		&out.SafeheronStatus, &out.SafeheronSubStatus,
		&out.StatusRank, &out.BlockHeight, &out.BlockHash, &out.Status,
		&out.FromAddress, &out.ToAddress, &out.TxHash,
	)
	if err != nil {
		return nil, fmt.Errorf("fetch deposit by tx_key: %w", err)
	}
	return out, nil
}

// FindOrCreateAccountForUpdate inserts or locates the account row and holds a
// row-level exclusive lock until tx commits. The no-op DO UPDATE clause is
// intentional: PostgreSQL's INSERT ON CONFLICT DO UPDATE acquires an exclusive
// row lock identical to SELECT FOR UPDATE, serialising concurrent credits.
func (r *DBRepository) FindOrCreateAccountForUpdate(ctx context.Context, tx Tx, userID int, currency string) (int64, string, error) {
	row := asSQLTx(tx).QueryRowContext(ctx,
		`INSERT INTO account (user_id, type, currency, balance, frozen_balance)
		 VALUES ($1, 'FUND', $2, 0, 0)
		 ON CONFLICT (user_id, currency) DO UPDATE
		   SET updated_at = account.updated_at
		 RETURNING id, balance::text`,
		userID, currency,
	)
	var id int64
	var balance string
	if err := row.Scan(&id, &balance); err != nil {
		return 0, "", fmt.Errorf("find or create account: %w", err)
	}
	return id, balance, nil
}

func (r *DBRepository) CreditAccount(ctx context.Context, tx Tx, accountID int64, amount string) (string, error) {
	row := asSQLTx(tx).QueryRowContext(ctx,
		`UPDATE account
		 SET balance = balance + $2::numeric,
		     version = version + 1,
		     updated_at = NOW()
		 WHERE id = $1
		 RETURNING balance::text`,
		accountID, amount,
	)
	var newBalance string
	if err := row.Scan(&newBalance); err != nil {
		return "", fmt.Errorf("credit account: %w", err)
	}
	return newBalance, nil
}

func (r *DBRepository) WriteJournal(ctx context.Context, tx Tx, j *JournalEntry) error {
	_, err := asSQLTx(tx).ExecContext(ctx,
		`INSERT INTO account_journal
		   (serial_no, user_id, account_id, amount, balance_snapshot, biz_type, ref_id, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())`,
		j.SerialNo, j.UserID, j.AccountID, j.Amount, j.BalanceSnapshot, j.BizType, j.RefID,
	)
	if err != nil {
		return fmt.Errorf("write journal: %w", err)
	}
	return nil
}

func (r *DBRepository) MarkDepositCredited(ctx context.Context, tx Tx, depositID int64) error {
	res, err := asSQLTx(tx).ExecContext(ctx,
		`UPDATE deposits SET status = $1, credited_at = NOW(), updated_at = NOW()
		 WHERE id = $2 AND status IN ($3, $4)`,
		DepositStatusCredited, depositID, DepositStatusPending, DepositStatusKYTPending,
	)
	if err != nil {
		return fmt.Errorf("mark deposit credited: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("mark deposit credited: rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("mark deposit credited: no rows affected (id=%d, status precondition failed)", depositID)
	}
	return nil
}

func (r *DBRepository) MarkDepositFailed(ctx context.Context, tx Tx, depositID int64, reason string) error {
	res, err := asSQLTx(tx).ExecContext(ctx,
		`UPDATE deposits
		 SET status = $1, failed_reason = $2, updated_at = NOW()
		 WHERE id = $3 AND status NOT IN ('CREDITED', 'MANUAL_REVIEW')`,
		DepositStatusFailed, reason, depositID,
	)
	if err != nil {
		return fmt.Errorf("mark deposit failed: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("mark deposit failed: rows affected: %w", err)
	}
	if n == 0 {
		return ErrDepositTerminalState
	}
	return nil
}

func (r *DBRepository) MarkDepositManualReview(ctx context.Context, tx Tx, depositID int64, reason string) error {
	res, err := asSQLTx(tx).ExecContext(ctx,
		`UPDATE deposits
		 SET status = $1, failed_reason = $2, updated_at = NOW()
		 WHERE id = $3 AND status NOT IN ('CREDITED', 'FAILED')`,
		DepositStatusManualReview, reason, depositID,
	)
	if err != nil {
		return fmt.Errorf("mark deposit manual review: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("mark deposit manual review: rows affected: %w", err)
	}
	if n == 0 {
		return ErrDepositTerminalState
	}
	return nil
}

func (r *DBRepository) MarkEventDone(ctx context.Context, tx Tx, eventID int64) error {
	_, err := asSQLTx(tx).ExecContext(ctx,
		`UPDATE safeheron_webhook_events
		 SET process_status = 'DONE', processed_at = NOW(),
		     process_attempts = process_attempts + 1
		 WHERE id = $1`,
		eventID,
	)
	if err != nil {
		return fmt.Errorf("mark event done: %w", err)
	}
	return nil
}

func (r *DBRepository) MarkEventError(ctx context.Context, tx Tx, eventID int64, errMsg string) error {
	_, err := asSQLTx(tx).ExecContext(ctx,
		`UPDATE safeheron_webhook_events
		 SET process_status = 'ERROR', error_message = $2,
		     processed_at = NOW(),
		     process_attempts = process_attempts + 1
		 WHERE id = $1`,
		eventID, errMsg,
	)
	if err != nil {
		return fmt.Errorf("mark event error: %w", err)
	}
	return nil
}

// MarkEventErrorNoTx conditionally finalizes a raw event after its owning
// transaction has been rolled back. The PENDING guard prevents a stale worker
// from overwriting a concurrent owner's terminal state.
func (r *DBRepository) MarkEventErrorNoTx(ctx context.Context, eventID int64, errMsg string) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE safeheron_webhook_events
		 SET process_status = 'ERROR', error_message = $2,
		     processed_at = NOW(),
		     process_attempts = process_attempts + 1
		 WHERE id = $1 AND process_status = 'PENDING'`,
		eventID, errMsg,
	)
	if err != nil {
		return false, fmt.Errorf("mark event error without transaction: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("mark event error without transaction: rows affected: %w", err)
	}
	return n > 0, nil
}

func (r *DBRepository) LookupAddressOwner(ctx context.Context, address, networkFamily string) (int, bool, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT COALESCE(assigned_user_id, 0)
		 FROM address_pool WHERE address = $1 AND network_family = $2 LIMIT 1`,
		address, networkFamily,
	)
	var uid int
	if err := row.Scan(&uid); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("lookup address owner: %w", err)
	}
	if uid == 0 {
		return 0, false, nil
	}
	return uid, true, nil
}

// === AML/KYT (Phase 1 v1.5) — DBRepository implementations ===

func (r *DBRepository) UpdateAMLFields(ctx context.Context, tx Tx, depositID int64, screeningState, riskLevel string, evaluatedAt time.Time, amlListJSON []byte) error {
	_, err := asSQLTx(tx).ExecContext(ctx,
		`UPDATE deposits
		 SET aml_screening_state = $1, aml_risk_level = $2,
		     aml_evaluated_at = $3, aml_list = $4, updated_at = NOW()
		 WHERE id = $5`,
		screeningState, riskLevel, evaluatedAt, amlListJSON, depositID,
	)
	if err != nil {
		return fmt.Errorf("update AML fields: %w", err)
	}
	return nil
}

func (r *DBRepository) MoveToKYTPending(ctx context.Context, tx Tx, depositID int64) error {
	res, err := asSQLTx(tx).ExecContext(ctx,
		`UPDATE deposits SET status = $1, updated_at = NOW()
		 WHERE id = $2 AND status = 'PENDING'`,
		DepositStatusKYTPending, depositID,
	)
	if err != nil {
		return fmt.Errorf("move to KYT_PENDING: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("move to KYT_PENDING: rows affected: %w", err)
	}
	if n == 0 {
		return ErrDepositNotPending
	}
	return nil
}

func (r *DBRepository) LockOneKYTPendingTimeout(ctx context.Context, tx Tx, threshold time.Duration) (*DepositRow, error) {
	row := asSQLTx(tx).QueryRowContext(ctx,
		`SELECT id, user_id, COALESCE(safeheron_tx_key, ''),
		        COALESCE(safeheron_coin_key, ''), amount, asset,
		        COALESCE(chain_code, ''), COALESCE(coin_chain_id, 0),
		        COALESCE(safeheron_status, ''), COALESCE(safeheron_sub_status, ''),
		        status_rank, COALESCE(block_height, 0), COALESCE(block_hash, ''),
		        status,
		        COALESCE(from_address, ''), COALESCE(to_address, ''), COALESCE(tx_hash, '')
		 FROM deposits
		 WHERE status = 'KYT_PENDING' AND updated_at < NOW() - $1::interval
		 ORDER BY updated_at ASC
		 FOR UPDATE SKIP LOCKED
		 LIMIT 1`,
		fmt.Sprintf("%d seconds", int(threshold.Seconds())),
	)
	out := &DepositRow{}
	if err := row.Scan(
		&out.ID, &out.UserID, &out.SafeheronTxKey, &out.SafeheronCoinKey,
		&out.Amount, &out.Asset,
		&out.ChainCode, &out.CoinChainID,
		&out.SafeheronStatus, &out.SafeheronSubStatus,
		&out.StatusRank, &out.BlockHeight, &out.BlockHash, &out.Status,
		&out.FromAddress, &out.ToAddress, &out.TxHash,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNoPending
		}
		return nil, fmt.Errorf("lock KYT_PENDING timeout: %w", err)
	}
	return out, nil
}

func (r *DBRepository) LockOneAmlPending(ctx context.Context, tx Tx, minAge time.Duration) (*DepositRow, error) {
	// Intentionally does NOT update updated_at: callers that skip (KYT still IN_PROGRESS)
	// must not reset the 20-min clock used by LockOneKYTPendingTimeout.
	// minAge gates how long a deposit must sit in KYT_PENDING before the safety-net
	// poll fires. AML_KYT_ALERT webhook typically arrives within ~78s; setting
	// minAge=5m avoids redundant KYT API calls when the webhook is the primary path.
	row := asSQLTx(tx).QueryRowContext(ctx,
		`SELECT id, user_id, COALESCE(safeheron_tx_key, ''),
		        COALESCE(safeheron_coin_key, ''), amount, asset,
		        COALESCE(chain_code, ''), COALESCE(coin_chain_id, 0),
		        COALESCE(safeheron_status, ''), COALESCE(safeheron_sub_status, ''),
		        status_rank, COALESCE(block_height, 0), COALESCE(block_hash, ''),
		        status,
		        COALESCE(from_address, ''), COALESCE(to_address, ''), COALESCE(tx_hash, '')
		 FROM deposits
		 WHERE status = 'KYT_PENDING'
		   AND aml_risk_level = 'PENDING'
		   AND updated_at < NOW() - $1::interval
		 ORDER BY updated_at ASC
		 FOR UPDATE SKIP LOCKED
		 LIMIT 1`,
		fmt.Sprintf("%d seconds", int(minAge.Seconds())),
	)
	out := &DepositRow{}
	if err := row.Scan(
		&out.ID, &out.UserID, &out.SafeheronTxKey, &out.SafeheronCoinKey,
		&out.Amount, &out.Asset,
		&out.ChainCode, &out.CoinChainID,
		&out.SafeheronStatus, &out.SafeheronSubStatus,
		&out.StatusRank, &out.BlockHeight, &out.BlockHash, &out.Status,
		&out.FromAddress, &out.ToAddress, &out.TxHash,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNoPending
		}
		return nil, fmt.Errorf("lock AML pending: %w", err)
	}
	return out, nil
}

func (r *DBRepository) FindDepositByTxKey(ctx context.Context, tx Tx, txKey string) (*DepositRow, bool, error) {
	row := asSQLTx(tx).QueryRowContext(ctx,
		`SELECT id, user_id, COALESCE(safeheron_tx_key, ''),
		        COALESCE(safeheron_coin_key, ''), amount, asset,
		        COALESCE(chain_code, ''), COALESCE(coin_chain_id, 0),
		        COALESCE(safeheron_status, ''), COALESCE(safeheron_sub_status, ''),
		        status_rank, COALESCE(block_height, 0), COALESCE(block_hash, ''),
		        status,
		        COALESCE(from_address, ''), COALESCE(to_address, ''), COALESCE(tx_hash, '')
		 FROM deposits WHERE safeheron_tx_key = $1
		 FOR UPDATE`,
		txKey,
	)
	out := &DepositRow{}
	if err := row.Scan(
		&out.ID, &out.UserID, &out.SafeheronTxKey, &out.SafeheronCoinKey,
		&out.Amount, &out.Asset,
		&out.ChainCode, &out.CoinChainID,
		&out.SafeheronStatus, &out.SafeheronSubStatus,
		&out.StatusRank, &out.BlockHeight, &out.BlockHash, &out.Status,
		&out.FromAddress, &out.ToAddress, &out.TxHash,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("find deposit by tx_key: %w", err)
	}
	return out, true, nil
}

// IncrementEventAttemptsNoTx 独立非事务 UPDATE — 用 r.db 不挂 tx。
// 调用时机：主事务 ROLLBACK 之后，必须脱离外层事务才能持久化。
func (r *DBRepository) IncrementEventAttemptsNoTx(ctx context.Context, eventID int64) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE safeheron_webhook_events SET process_attempts = process_attempts + 1 WHERE id = $1`,
		eventID,
	)
	if err != nil {
		return fmt.Errorf("increment event attempts: %w", err)
	}
	return nil
}

func optionalDepositMappingArgs(chainCode string, coinChainID int, status string) (any, any, error) {
	hasChainCode := strings.TrimSpace(chainCode) != ""
	hasCoinChainID := coinChainID != 0
	if coinChainID < 0 || hasChainCode != hasCoinChainID {
		return nil, nil, fmt.Errorf("optional FK mapping requires chain_code and coin_chain_id together")
	}
	if !hasChainCode {
		if status != DepositStatusManualReview {
			return nil, nil, fmt.Errorf("optional FK mapping may be absent only for MANUAL_REVIEW deposits")
		}
		return nil, nil, nil
	}
	return chainCode, coinChainID, nil
}

func depositChainIdentity(d *DepositRow) string {
	// deposits.chain is the legacy non-FK identity field. Keep the canonical
	// chain for mapped rows and use a bounded sentinel for unsupported rows.
	if strings.TrimSpace(d.ChainCode) != "" {
		return d.ChainCode
	}
	return legacyUnsupportedChainIdentity
}

// nullableTxHash satisfies the legacy deposits.tx_hash NOT NULL UNIQUE
// constraint by falling back to the Safeheron txKey when the on-chain hash
// hasn't surfaced yet (CREATED/CONFIRMING events).
func nullableTxHash(txHash, txKey string) string {
	if txHash != "" {
		return txHash
	}
	return "sh:" + txKey
}

var _ Repository = (*DBRepository)(nil)
