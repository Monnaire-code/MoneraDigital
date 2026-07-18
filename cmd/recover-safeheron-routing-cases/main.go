package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"monera-digital/internal/fundrouting"
	"monera-digital/internal/safeheron"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"
)

type recoveryEvent struct {
	ID            int64
	EventType     string
	PayloadDigest string
	RawPayload    []byte
}

type recoveryReport struct {
	RawEventCount          int      `json:"raw_event_count"`
	OccurrenceCount        int      `json:"occurrence_count"`
	ExistingCaseCount      int      `json:"existing_case_count"`
	NoOutputCount          int      `json:"no_output_count"`
	ExistingDepositCount   int      `json:"existing_deposit_count"`
	ExistingCompanyCount   int      `json:"existing_company_count"`
	ProviderEventOnlyCount int      `json:"provider_event_only_count"`
	ExistingDualCount      int      `json:"existing_dual_count"`
	ConflictingOutputCount int      `json:"conflicting_output_count"`
	AppliedEventCount      int      `json:"applied_event_count"`
	OccurrenceIdentitySHA  string   `json:"occurrence_identity_sha256"`
	ConflictIdentities     []string `json:"conflict_identities,omitempty"`
}

func main() {
	if os.Getenv("APP_ENV") != "production" {
		_ = godotenv.Overload(".env")
	}
	apply := flag.Bool("apply", false, "Apply safe no-output candidates through the authoritative router")
	from := flag.String("from", "", "Inclusive RFC3339 received_at lower bound")
	to := flag.String("to", "", "Exclusive RFC3339 received_at upper bound")
	afterID := flag.Int64("after-id", 0, "Only inspect raw event IDs greater than this value")
	limit := flag.Int("limit", 100, "Raw event batch limit (1-500)")
	alertMode := flag.String("alert-mode", "summary-only", "Recovery alert mode; only summary-only is supported")
	flag.Parse()
	if *limit < 1 || *limit > 500 {
		log.Fatal("limit must be between 1 and 500")
	}
	if *afterID < 0 {
		log.Fatal("after-id must not be negative")
	}
	if *alertMode != "summary-only" {
		log.Fatal("only --alert-mode=summary-only is supported")
	}
	fromTime, err := parseOptionalTime(*from)
	if err != nil {
		log.Fatal("invalid from: ", err)
	}
	toTime, err := parseOptionalTime(*to)
	if err != nil {
		log.Fatal("invalid to: ", err)
	}
	if fromTime != nil && toTime != nil && !fromTime.Before(*toTime) {
		log.Fatal("from must be earlier than to")
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	report, err := recoverBatch(context.Background(), db, recoveryOptions{
		Apply: *apply, From: fromTime, To: toTime, AfterID: *afterID, Limit: *limit,
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
		log.Fatal(err)
	}
}

type recoveryOptions struct {
	Apply   bool
	From    *time.Time
	To      *time.Time
	AfterID int64
	Limit   int
}

func recoverBatch(ctx context.Context, db *sql.DB, options recoveryOptions) (recoveryReport, error) {
	events, err := loadRecoveryEvents(ctx, db, options)
	if err != nil {
		return recoveryReport{}, err
	}
	report := recoveryReport{RawEventCount: len(events)}
	identities := make([]string, 0)
	resolver := fundrouting.NewCatalogNetworkResolver(recoveryCoinLookup{db: db})
	type recoveryApply struct {
		input fundrouting.VerifiedEventInput
	}
	applyInputs := make([]recoveryApply, 0, len(events))
	for _, event := range events {
		var envelope struct {
			EventType   string                        `json:"eventType"`
			EventDetail safeheron.TransactionSnapshot `json:"eventDetail"`
		}
		if err := json.Unmarshal(event.RawPayload, &envelope); err != nil {
			return report, fmt.Errorf("event %d decode: %w", event.ID, err)
		}
		family, err := resolver.ResolveNetworkFamily(ctx, envelope.EventDetail)
		if err != nil {
			return report, err
		}
		if family == "UNKNOWN" {
			return report, fmt.Errorf("event %d network family cannot be proven; recovery is fail-closed", event.ID)
		}
		candidates, err := fundrouting.BuildCandidates(envelope.EventDetail, family)
		if err != nil {
			return report, fmt.Errorf("event %d enumerate: %w", event.ID, err)
		}
		previewInput := fundrouting.VerifiedEventInput{
			WebhookEventID: event.ID, EventType: event.EventType, PayloadDigest: event.PayloadDigest,
			NetworkFamily: family, Snapshot: envelope.EventDetail,
		}
		if envelope.EventType != "" {
			previewInput.EventType = envelope.EventType
		}
		preview, err := fundrouting.NewRepository(db).PreviewVerifiedEvent(ctx, previewInput)
		if err != nil {
			return report, fmt.Errorf("event %d preview: %w", event.ID, err)
		}
		decisions := make(map[string]fundrouting.DecisionResult, len(preview))
		for _, result := range preview {
			decisions[result.RoutingIdentityKey] = result.Decision
		}
		links := make(map[string]recoveryClassification, len(candidates))
		for _, candidate := range candidates {
			report.OccurrenceCount++
			identities = append(identities, candidate.RoutingIdentityKey)
			decision, ok := decisions[candidate.RoutingIdentityKey]
			if !ok {
				return report, fmt.Errorf("event %d occurrence %s has no authoritative preview", event.ID, candidate.RoutingIdentityKey)
			}
			classification, err := classifyRecoveryOccurrence(ctx, db, event.ID, event.EventType, event.PayloadDigest, len(candidates), candidate, decision)
			if err != nil {
				return report, err
			}
			links[candidate.RoutingIdentityKey] = classification
			switch classification.Kind {
			case "CASE":
				report.ExistingCaseCount++
			case "NONE":
				report.NoOutputCount++
			case "DEPOSIT":
				report.ExistingDepositCount++
			case "COMPANY":
				report.ExistingCompanyCount++
			case "PROVIDER_EVENT_ONLY":
				report.ProviderEventOnlyCount++
			case "DUAL":
				report.ExistingDualCount++
			default:
				report.ConflictingOutputCount++
				report.ConflictIdentities = append(report.ConflictIdentities, candidate.RoutingIdentityKey)
			}
		}
		existingLinks := make(map[string]fundrouting.ExistingProjectionLink)
		for identity, classification := range links {
			if classification.DepositID > 0 || classification.CompanyTransactionID > 0 || classification.ProviderEventID > 0 {
				existingLinks[identity] = fundrouting.ExistingProjectionLink{
					RoutingIdentityKey: identity, DepositID: classification.DepositID,
					CompanyFundTransactionID: classification.CompanyTransactionID,
					ProviderEventID:          classification.ProviderEventID,
				}
			}
		}
		applyInputs = append(applyInputs, recoveryApply{
			input: fundrouting.VerifiedEventInput{
				WebhookEventID: event.ID, EventType: previewInput.EventType, PayloadDigest: event.PayloadDigest,
				NetworkFamily: family, Snapshot: envelope.EventDetail,
				SuppressOpenAlert: true, PreserveRawEventStatus: true, ExistingProjectionLinks: existingLinks,
			},
		})
	}
	sort.Strings(identities)
	sum := sha256.Sum256([]byte(strings.Join(identities, "\n")))
	report.OccurrenceIdentitySHA = hex.EncodeToString(sum[:])
	if options.Apply && len(report.ConflictIdentities) > 0 {
		return report, fmt.Errorf("recovery stopped: %d occurrences have conflicting ledger evidence", len(report.ConflictIdentities))
	}
	if options.Apply {
		router := fundrouting.NewRepository(db)
		recoveredEventIDs := make([]int64, 0, len(applyInputs))
		for _, applyInput := range applyInputs {
			results, err := router.RouteVerifiedEvent(ctx, applyInput.input)
			if err != nil {
				return report, fmt.Errorf("event %d route: %w", applyInput.input.WebhookEventID, err)
			}
			_ = results
			recoveredEventIDs = append(recoveredEventIDs, applyInput.input.WebhookEventID)
			report.AppliedEventCount++
		}
		if len(recoveredEventIDs) > 0 {
			if err := completeRecoveryRun(ctx, db, options, recoveredEventIDs, report); err != nil {
				return report, err
			}
		}
	}
	return report, nil
}

func completeRecoveryRun(ctx context.Context, db *sql.DB, options recoveryOptions, eventIDs []int64, report recoveryReport) (err error) {
	optionsJSON, err := json.Marshal(options)
	if err != nil {
		return err
	}
	reportJSON, err := json.Marshal(report)
	if err != nil {
		return err
	}
	keyMaterial := fmt.Sprintf("%s\x1f%s\x1f%v", report.OccurrenceIdentitySHA, optionsJSON, eventIDs)
	runSum := sha256.Sum256([]byte(keyMaterial))
	runKey := hex.EncodeToString(runSum[:])
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	for _, eventID := range eventIDs {
		result, updateErr := tx.ExecContext(ctx, `UPDATE safeheron_webhook_events
SET process_status='DONE',processed_at=now(),error_message=NULL
WHERE id=$1 AND process_status='ERROR'`, eventID)
		if updateErr != nil {
			return updateErr
		}
		if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
			return fmt.Errorf("mark recovered event %d DONE affected %d rows", eventID, rows)
		}
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO safeheron_transaction_routing_recovery_runs
  (run_key,occurrence_identity_digest,event_count,occurrence_count,recovery_options,recovery_report,status)
VALUES ($1,$2,$3,$4,$5::jsonb,$6::jsonb,'APPLIED') ON CONFLICT (run_key) DO NOTHING`,
		runKey, report.OccurrenceIdentitySHA, len(eventIDs), report.OccurrenceCount, optionsJSON, reportJSON)
	if err != nil {
		return err
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
		return fmt.Errorf("recovery audit run %s already exists or conflicts", runKey)
	}
	return tx.Commit()
}

type recoveryCoinLookup struct{ db *sql.DB }

func (lookup recoveryCoinLookup) Lookup(coinKey string) (safeheron.Coin, error) {
	if lookup.db == nil {
		return safeheron.Coin{}, fmt.Errorf("recovery coin catalog database is unavailable")
	}
	var family string
	err := lookup.db.QueryRow(`SELECT chain.network_family
FROM coin_chains asset
JOIN chains chain ON chain.code=asset.chain_code
WHERE asset.safeheron_coin_key=$1 AND asset.deposit_enabled=true`, strings.TrimSpace(coinKey)).Scan(&family)
	if err != nil {
		return safeheron.Coin{}, fmt.Errorf("resolve recovery CoinKey %q: %w", coinKey, err)
	}
	return safeheron.Coin{CoinKey: coinKey, BlockchainType: family}, nil
}

func loadRecoveryEvents(ctx context.Context, db *sql.DB, options recoveryOptions) ([]recoveryEvent, error) {
	rows, err := db.QueryContext(ctx, `
SELECT event.id,event.event_type,event.payload_digest,event.raw_payload
FROM safeheron_webhook_events event
WHERE event.id>$1 AND event.process_status='ERROR'
  AND event.event_type IN ('TRANSACTION_CREATED','TRANSACTION_STATUS_CHANGED')
  AND position('deposits_user_id_users_id_fk' in COALESCE(event.error_message,''))>0
  AND ($2::timestamptz IS NULL OR event.received_at >= $2)
  AND ($3::timestamptz IS NULL OR event.received_at < $3)
ORDER BY event.id LIMIT $4`, options.AfterID, options.From, options.To, options.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]recoveryEvent, 0)
	for rows.Next() {
		var event recoveryEvent
		if err := rows.Scan(&event.ID, &event.EventType, &event.PayloadDigest, &event.RawPayload); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}

type recoveryClassification struct {
	Kind                 string
	DepositID            int64
	CompanyTransactionID int64
	ProviderEventID      int64
}

func classifyRecoveryOccurrence(ctx context.Context, db *sql.DB, eventID int64, eventType, payloadDigest string, candidateCount int, candidate fundrouting.Candidate, decision fundrouting.DecisionResult) (recoveryClassification, error) {
	var hasCase, hasProvider, depositExact, companyExact bool
	var depositID, companyID, providerEventID sql.NullInt64
	err := db.QueryRowContext(ctx, `SELECT
  EXISTS(SELECT 1 FROM safeheron_transaction_routing_cases WHERE routing_identity_key=$1),
  (SELECT id FROM deposits WHERE safeheron_tx_key=$2),
  (SELECT id FROM company_fund_transactions WHERE channel='SAFEHERON' AND provider_occurrence_key=$1),
  COALESCE((SELECT deposit.user_id=$4
    AND deposit.safeheron_coin_key=$5
    AND deposit.amount=$6::numeric
    AND lower(COALESCE(deposit.from_address,''))=$7
    AND lower(COALESCE(deposit.to_address,''))=$8
    AND EXISTS (SELECT 1 FROM coin_chains asset JOIN chains chain ON chain.code=asset.chain_code
      WHERE asset.id=deposit.coin_chain_id AND asset.safeheron_coin_key=$5
        AND upper(chain.network_family)=$9)
    FROM deposits deposit WHERE deposit.safeheron_tx_key=$2),false),
  COALESCE((SELECT movement.provider_asset_key=$5
    AND movement.amount=$6::numeric
    AND lower(COALESCE(movement.from_address_or_account,''))=$7
    AND lower(COALESCE(movement.to_address_or_account,''))=$8
    AND movement.transaction_direction=$10
    AND (movement.from_company_fund_account_id=$11 OR movement.to_company_fund_account_id=$11)
    FROM company_fund_transactions movement
    WHERE movement.channel='SAFEHERON' AND movement.provider_occurrence_key=$1),false),
  (SELECT MIN(id) FROM company_fund_provider_events
    WHERE safeheron_webhook_event_id=$3
      AND source_kind='EXISTING_SAFEHERON_WEBHOOK_REF'
      AND source_payload_digest=$12 AND event_type=$13
      AND event_state IN ('PENDING','FAILED')
      AND authorized_safeheron_occurrence_key IS NULL
      AND authorizing_routing_action_id IS NULL),
  EXISTS(SELECT 1 FROM company_fund_provider_events WHERE safeheron_webhook_event_id=$3)`,
		candidate.RoutingIdentityKey, candidate.SafeheronTxKey, eventID,
		decision.CustomerUserID, candidate.RawCoinKey, candidate.Occurrence.Amount.String(),
		candidate.Occurrence.NormalizedSource, candidate.Occurrence.NormalizedDestination,
		candidate.NetworkFamily, candidate.Direction, decision.CompanyFundAccountID, payloadDigest, eventType).
		Scan(&hasCase, &depositID, &companyID, &depositExact, &companyExact, &providerEventID, &hasProvider)
	if err != nil {
		return recoveryClassification{}, err
	}
	result := recoveryClassification{DepositID: depositID.Int64, CompanyTransactionID: companyID.Int64, ProviderEventID: providerEventID.Int64}
	switch {
	case depositID.Valid && companyID.Valid && depositExact && companyExact && decision.Decision == fundrouting.DecisionDual:
		result.Kind = "DUAL"
	case depositID.Valid && !companyID.Valid && depositExact &&
		(decision.Decision == fundrouting.DecisionCustomer || decision.Decision == fundrouting.DecisionDual):
		result.Kind = "DEPOSIT"
	case companyID.Valid && !depositID.Valid && companyExact &&
		(decision.Decision == fundrouting.DecisionCompany || decision.Decision == fundrouting.DecisionDual):
		result.Kind = "COMPANY"
	case depositID.Valid || companyID.Valid:
		result.Kind = "CONFLICT"
	case hasCase:
		result.Kind = "CASE"
	case hasProvider && (!providerEventID.Valid || candidateCount != 1 ||
		(decision.Decision != fundrouting.DecisionCompany && decision.Decision != fundrouting.DecisionDual)):
		result.Kind = "CONFLICT"
	case providerEventID.Valid:
		result.Kind = "PROVIDER_EVENT_ONLY"
	default:
		result.Kind = "NONE"
	}
	return result, nil
}

func parseOptionalTime(value string) (*time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	parsed = parsed.UTC()
	return &parsed, nil
}
