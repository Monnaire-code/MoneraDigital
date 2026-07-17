package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"
	"monera-digital/internal/companyfund"
	"monera-digital/internal/fundrouting"
	"monera-digital/internal/safeheron"
)

func TestRecoverBatchDryRunClassifiesWithoutMutation(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	snapshot := safeheron.TransactionSnapshot{
		TxKey: "recovery-tx", CoinKey: "ETHEREUM_ETH", TxAmount: "1",
		SourceAddress:        "0x00000000000000000000000000000000000000a1",
		DestinationAddress:   "0x00000000000000000000000000000000000000b2",
		TransactionDirection: "INFLOW", TransactionStatus: "COMPLETED",
		CreateTime: time.Now().UnixMilli(),
	}
	payload, _ := json.Marshal(map[string]any{"eventType": "TRANSACTION_STATUS_CHANGED", "eventDetail": snapshot})
	mock.ExpectQuery("FROM safeheron_webhook_events event").
		WithArgs(int64(0), nil, nil, 10).
		WillReturnRows(sqlmock.NewRows([]string{"id", "event_type", "payload_digest", "raw_payload"}).
			AddRow(7, "TRANSACTION_STATUS_CHANGED", strings.Repeat("a", 64), payload))
	mock.ExpectQuery("FROM coin_chains asset").WithArgs("ETHEREUM_ETH").
		WillReturnRows(sqlmock.NewRows([]string{"network_family"}).AddRow("EVM"))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT decision,reason_code,requires_customer_projection").WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"decision", "reason_code", "requires_customer", "requires_company", "customer_id", "company_id"}))
	mock.ExpectQuery("FROM safeheron_address_ownerships").WithArgs("EVM", strings.ToLower(snapshot.SourceAddress)).
		WillReturnRows(sqlmock.NewRows([]string{"owner_kind", "assigned_user_id", "assigned_at", "account_id", "enabled", "monitoring_at"}))
	mock.ExpectQuery("FROM safeheron_address_ownerships").WithArgs("EVM", strings.ToLower(snapshot.DestinationAddress)).
		WillReturnRows(sqlmock.NewRows([]string{"owner_kind", "assigned_user_id", "assigned_at", "account_id", "enabled", "monitoring_at"}))
	mock.ExpectRollback()
	mock.ExpectQuery("EXISTS\\(SELECT 1 FROM safeheron_transaction_routing_cases").
		WithArgs(sqlmock.AnyArg(), "recovery-tx", int64(7), nil, "ETHEREUM_ETH", "1", strings.ToLower(snapshot.SourceAddress), strings.ToLower(snapshot.DestinationAddress), "EVM", "INFLOW", nil, strings.Repeat("a", 64), "TRANSACTION_STATUS_CHANGED").
		WillReturnRows(sqlmock.NewRows([]string{"has_case", "deposit_id", "company_id", "deposit_exact", "company_exact", "provider_event_id", "has_provider"}).
			AddRow(false, nil, nil, false, false, nil, false))

	report, err := recoverBatch(context.Background(), db, recoveryOptions{Limit: 10})
	if err != nil {
		t.Fatalf("recoverBatch: %v", err)
	}
	if report.RawEventCount != 1 || report.OccurrenceCount != 1 || report.NoOutputCount != 1 || report.AppliedEventCount != 0 || len(report.OccurrenceIdentitySHA) != 64 {
		t.Fatalf("report = %#v", report)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestClassifyRecoveryOccurrenceKeepsProviderOnlyDistinctFromFinancialOutput(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	companyID := int64(33)
	mock.ExpectQuery("EXISTS\\(SELECT 1 FROM safeheron_transaction_routing_cases").
		WithArgs("safeheron-occurrence-v1:key", "tx-key", int64(91), nil, "ETHEREUM_ETH", "1", "0xsource", "0xdest", "EVM", "INFLOW", &companyID, strings.Repeat("a", 64), "TRANSACTION_STATUS_CHANGED").
		WillReturnRows(sqlmock.NewRows([]string{"has_case", "deposit_id", "company_id", "deposit_exact", "company_exact", "provider_event_id", "has_provider"}).
			AddRow(false, nil, nil, false, false, 77, true))
	classification, err := classifyRecoveryOccurrence(context.Background(), db, 91, "TRANSACTION_STATUS_CHANGED", strings.Repeat("a", 64), 1, fundrouting.Candidate{
		RoutingIdentityKey: "safeheron-occurrence-v1:key", SafeheronTxKey: "tx-key", RawCoinKey: "ETHEREUM_ETH",
		NetworkFamily: "EVM", Direction: "INFLOW", Occurrence: companyfund.SafeheronPrincipalOccurrence{
			Amount: decimal.RequireFromString("1"), NormalizedSource: "0xsource", NormalizedDestination: "0xdest",
		},
	}, fundrouting.DecisionResult{Decision: fundrouting.DecisionCompany, RequiresCompanyProjection: true, CompanyFundAccountID: &companyID})
	if err != nil {
		t.Fatalf("classifyRecoveryOccurrence: %v", err)
	}
	if classification.Kind != "PROVIDER_EVENT_ONLY" || classification.ProviderEventID != 77 || classification.DepositID != 0 || classification.CompanyTransactionID != 0 {
		t.Fatalf("classification = %#v", classification)
	}
}

func TestClassifyRecoveryOccurrenceRejectsAmbiguousLegacyProviderEvent(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	companyID := int64(33)
	mock.ExpectQuery("EXISTS\\(SELECT 1 FROM safeheron_transaction_routing_cases").
		WillReturnRows(sqlmock.NewRows([]string{"has_case", "deposit_id", "company_id", "deposit_exact", "company_exact", "provider_event_id", "has_provider"}).
			AddRow(false, nil, nil, false, false, 77, true))
	classification, err := classifyRecoveryOccurrence(context.Background(), db, 91, "TRANSACTION_STATUS_CHANGED", strings.Repeat("a", 64), 2, fundrouting.Candidate{
		RoutingIdentityKey: "safeheron-occurrence-v1:key", SafeheronTxKey: "tx-key", RawCoinKey: "ETHEREUM_ETH",
		NetworkFamily: "EVM", Direction: "INFLOW", Occurrence: companyfund.SafeheronPrincipalOccurrence{
			Amount: decimal.RequireFromString("1"), NormalizedSource: "0xsource", NormalizedDestination: "0xdest",
		},
	}, fundrouting.DecisionResult{Decision: fundrouting.DecisionCompany, CompanyFundAccountID: &companyID})
	if err != nil {
		t.Fatal(err)
	}
	if classification.Kind != "CONFLICT" {
		t.Fatalf("classification=%#v", classification)
	}
}

func TestClassifyRecoveryOccurrenceRejectsMismatchedLedgerOutputDuringDryRun(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	userID := 12
	candidate := fundrouting.Candidate{
		RoutingIdentityKey: "safeheron-occurrence-v1:" + strings.Repeat("a", 64), SafeheronTxKey: "tx-key", RawCoinKey: "ETHEREUM_ETH",
		NetworkFamily: "EVM", Direction: "INFLOW", Occurrence: companyfund.SafeheronPrincipalOccurrence{
			Amount: decimal.RequireFromString("1"), NormalizedSource: "0xsource", NormalizedDestination: "0xdest",
		},
	}
	mock.ExpectQuery("EXISTS\\(SELECT 1 FROM safeheron_transaction_routing_cases").
		WithArgs(candidate.RoutingIdentityKey, "tx-key", int64(92), &userID, "ETHEREUM_ETH", "1", "0xsource", "0xdest", "EVM", "INFLOW", nil, strings.Repeat("b", 64), "TRANSACTION_STATUS_CHANGED").
		WillReturnRows(sqlmock.NewRows([]string{"has_case", "deposit_id", "company_id", "deposit_exact", "company_exact", "provider_event_id", "has_provider"}).
			AddRow(false, 44, nil, false, false, nil, false))
	classification, err := classifyRecoveryOccurrence(context.Background(), db, 92, "TRANSACTION_STATUS_CHANGED", strings.Repeat("b", 64), 1, candidate, fundrouting.DecisionResult{
		Decision: fundrouting.DecisionCustomer, RequiresCustomerProjection: true, CustomerUserID: &userID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if classification.Kind != "CONFLICT" || classification.DepositID != 44 {
		t.Fatalf("classification=%#v", classification)
	}
}

func TestCompleteRecoveryRunMarksEventsAndWritesOneAuditAtomically(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE safeheron_webhook_events").WithArgs(int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO safeheron_transaction_routing_recovery_runs").
		WithArgs(sqlmock.AnyArg(), strings.Repeat("b", 64), 1, 1, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	err = completeRecoveryRun(context.Background(), db, recoveryOptions{Apply: true, Limit: 10}, []int64{7}, recoveryReport{
		RawEventCount: 1, OccurrenceCount: 1, AppliedEventCount: 1, OccurrenceIdentitySHA: strings.Repeat("b", 64),
	})
	if err != nil {
		t.Fatalf("completeRecoveryRun: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestParseOptionalTimeNormalizesUTCAndRejectsInvalid(t *testing.T) {
	value, err := parseOptionalTime("2026-07-17T22:00:00+08:00")
	if err != nil || value.Format(time.RFC3339) != "2026-07-17T14:00:00Z" {
		t.Fatalf("value=%v err=%v", value, err)
	}
	if _, err := parseOptionalTime("not-time"); err == nil {
		t.Fatal("expected invalid time error")
	}
}
