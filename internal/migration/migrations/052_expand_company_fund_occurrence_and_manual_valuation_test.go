package migrations

import (
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"testing"

	"monera-digital/internal/companyfund"
	"monera-digital/internal/migration"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"
)

func runMigration052TestTransaction(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if err := (&ExpandCompanyFundOccurrenceAndManualValuation{}).UpTx(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func TestExpandCompanyFundOccurrenceAndManualValuationMetadata(t *testing.T) {
	t.Parallel()
	var _ migration.Migration = (*ExpandCompanyFundOccurrenceAndManualValuation)(nil)
	var _ migration.ControlledMigration = (*ExpandCompanyFundOccurrenceAndManualValuation)(nil)
	m := &ExpandCompanyFundOccurrenceAndManualValuation{}
	if m.Version() != "052" || m.Description() == "" {
		t.Fatalf("metadata = %q %q", m.Version(), m.Description())
	}
	if m.RequiredPreexistingVersion() != "051" || m.RequiredExpectedCeiling() != "052" {
		t.Fatalf("control boundary = prior %q ceiling %q", m.RequiredPreexistingVersion(), m.RequiredExpectedCeiling())
	}
	if err := m.Up(nil); err == nil || !strings.Contains(err.Error(), "controlled") {
		t.Fatalf("direct Up() = %v", err)
	}
	if err := m.Down(nil); err == nil || !strings.Contains(err.Error(), "forward-only") {
		t.Fatalf("Down() = %v", err)
	}
}

func TestCompanyFundMigrationsPinCanonicalPublicSchema(t *testing.T) {
	t.Parallel()
	for name, query := range map[string]string{
		"052 preflight":           migration052PreflightQuery,
		"052 add columns":         migration052AddOccurrenceColumnsSQL,
		"052 backfill":            migration052BackfillQuery,
		"052 update":              migration052BackfillUpdate,
		"052 missing":             migration052MissingAliasQuery,
		"052 duplicates":          migration052DuplicateAliasQuery,
		"052 schema":              migration052SchemaDDL,
		"053 preflight":           migration053PreflightSQL,
		"053 add constraint":      migration053AddConstraintSQL,
		"053 validate constraint": migration053ValidateConstraintSQL,
	} {
		if !strings.Contains(query, "public.") {
			t.Errorf("%s does not explicitly bind public schema", name)
		}
	}
}

func TestMigration052OccurrenceAndManualDDLContracts(t *testing.T) {
	t.Parallel()
	ddl := migration052AddOccurrenceColumnsSQL + migration052SchemaDDL
	for _, contract := range []string{
		"provider_occurrence_key VARCHAR(256)", "provider_occurrence_algorithm_version VARCHAR(64)",
		"idx_company_fund_transactions_safeheron_occurrence", "WHERE channel = 'SAFEHERON' AND provider_occurrence_key IS NOT NULL",
		"usd_valuation_source IN ('SAFEHERON', 'AIRWALLEX', 'COINGECKO', 'USD_PAR', 'MANUAL')",
		"manual_reason TEXT", "manual_updated_by VARCHAR(256)", "manual_updated_at TIMESTAMPTZ",
		"company_fund_enforce_manual_valuation_projection", "BEFORE UPDATE OF", "ERRCODE = 'P7501'",
		"MANUAL_TOTAL", "MANUAL_OVERRIDE", "MANUAL_V1", "MANUAL_ADMIN",
	} {
		if !strings.Contains(ddl, contract) {
			t.Errorf("DDL missing %q", contract)
		}
	}
	for _, protected := range migration052ProtectedProjectionColumns {
		for _, reference := range []string{"OLD." + protected, "NEW." + protected} {
			if !strings.Contains(ddl, reference) {
				t.Errorf("trigger missing %q", reference)
			}
		}
	}
	for _, transitionContract := range []string{
		") IS NOT DISTINCT FROM ROW(",
		"WHERE id = NEW.current_valuation_history_id AND transaction_id = NEW.id",
		"current_history.supersedes_history_id IS DISTINCT FROM OLD.current_valuation_history_id",
		"current_history.valuation_version IS DISTINCT FROM OLD.usd_valuation_version + 1",
		"id <> current_history.id",
		"current_history.valuation_version IS DISTINCT FROM previous_max_version + 1",
		"NEW.provider_transaction_fact_id IS DISTINCT FROM current_history.provider_transaction_fact_id",
		"NEW.provider_reported_usd_value IS DISTINCT FROM OLD.provider_reported_usd_value",
		"NEW.calculated_usd_value IS DISTINCT FROM OLD.calculated_usd_value",
		"NEW.usd_provider_value_scope IS DISTINCT FROM OLD.usd_provider_value_scope",
		"NEW.provider_transaction_fact_id IS DISTINCT FROM OLD.provider_transaction_fact_id",
	} {
		if !strings.Contains(ddl, transitionContract) {
			t.Errorf("trigger missing transition contract %q", transitionContract)
		}
	}
	for _, cleared := range []string{
		"usd_value IS NOT NULL",
		"usd_valuation_basis IS NULL",
		"usd_valuation_time IS NULL",
		"usd_valuation_price_at IS NULL",
		"usd_valuation_granularity IS NULL",
		"usd_rate_snapshot_id IS NULL",
		"usd_derivation_method IS NULL",
	} {
		if !strings.Contains(ddl, cleared) {
			t.Errorf("MANUAL history check missing %q", cleared)
		}
	}
	for _, forbidden := range []string{
		"provider_occurrence_key VARCHAR(256) NOT NULL", "provider_occurrence_algorithm_version VARCHAR(64) NOT NULL",
		"provider_occurrence_key IS NOT NULL AND provider_occurrence_algorithm_version", "safeheron-occurrence-v1') NOT VALID",
	} {
		if strings.Contains(ddl, forbidden) {
			t.Errorf("Migration A found forbidden required contract %q", forbidden)
		}
	}
}

func TestMigration052BackfillQueriesUseOnlyStableOccurrenceFacts(t *testing.T) {
	t.Parallel()
	for _, query := range []string{migration052PreflightQuery, migration052BackfillQuery} {
		for _, required := range []string{"provider_transaction_id", "movement_kind", "provider_asset_key", "from_address_or_account", "to_address_or_account", "amount", "transfer_mode", "movement_index"} {
			if !strings.Contains(query, required) {
				t.Errorf("query missing %q", required)
			}
		}
		for _, forbidden := range []string{"tx_hash", "currency", "chain_code", "asset_contract"} {
			if strings.Contains(query, forbidden) {
				t.Errorf("query uses forbidden %q", forbidden)
			}
		}
	}
	if !strings.Contains(migration052BackfillUpdate, "provider_occurrence_key") || !strings.Contains(migration052BackfillUpdate, "provider_occurrence_algorithm_version") {
		t.Fatal("backfill alias columns missing")
	}
	for _, preserved := range []string{"movement_key", "identity_algorithm_version", "usd_value", "finance_category_level1_id", "current_valuation_history_id"} {
		if strings.Contains(migration052BackfillUpdate, preserved) {
			t.Errorf("backfill mutates %q", preserved)
		}
	}
}

func TestMigration052UpBackfillsExistingSafeheronV1RowsInOneTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(migration052TimeoutsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(migration052PreflightQuery)).WillReturnRows(sqlmock.NewRows([]string{"missing_count"}).AddRow(0))
	mock.ExpectExec(regexp.QuoteMeta(migration052AddOccurrenceColumnsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(migration052SchemaDDL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(migration052BackfillQuery)).WillReturnRows(sqlmock.NewRows([]string{"id", "provider_transaction_id", "movement_kind", "provider_asset_key", "from_address_or_account", "to_address_or_account", "amount", "transfer_mode", "movement_index", "network_family"}).
		AddRow(10, "tx-1", "PRINCIPAL", "ETHEREUM_USDT", "0xAbC", "0xDeF", "1.230000000000000000", "SINGLE", 0, "EVM").
		AddRow(11, "tx-1", "FEE", "ETHEREUM_ETH", "0xAbC", "0xDeF", "0.001000000000000000", "SINGLE", 1, "EVM"))
	inputs := []companyfund.SafeheronOccurrenceInput{
		{ProviderTransactionKey: "tx-1", MovementKind: companyfund.MovementKindPrincipal, RawCoinKey: "ETHEREUM_USDT", NormalizedSource: "0xabc", NormalizedDestination: "0xdef", Amount: decimal.RequireFromString("1.23"), TransferMode: companyfund.TransferModeSingle, MovementIndex: 0},
		{ProviderTransactionKey: "tx-1", MovementKind: companyfund.MovementKindFee, RawCoinKey: "ETHEREUM_ETH", NormalizedSource: "0xabc", NormalizedDestination: "0xdef", Amount: decimal.RequireFromString("0.001"), TransferMode: companyfund.TransferModeSingle, MovementIndex: 1},
	}
	for i, input := range inputs {
		occurrence, buildErr := companyfund.BuildSafeheronOccurrence(input)
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		mock.ExpectExec(regexp.QuoteMeta(migration052BackfillUpdate)).WithArgs(occurrence.Key, occurrence.AlgorithmVersion, int64(10+i)).WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectQuery(regexp.QuoteMeta(migration052MissingAliasQuery)).WillReturnRows(sqlmock.NewRows([]string{"missing_count"}).AddRow(0))
	mock.ExpectQuery(regexp.QuoteMeta(migration052DuplicateAliasQuery)).WillReturnRows(sqlmock.NewRows([]string{"duplicate_count"}).AddRow(0))
	mock.ExpectCommit()
	if err := runMigration052TestTransaction(db); err != nil {
		t.Fatalf("Up() = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMigration052UpHardStopsBeforeDDLWhenTupleIsIncomplete(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(migration052TimeoutsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(migration052PreflightQuery)).WillReturnRows(sqlmock.NewRows([]string{"missing_count"}).AddRow(1))
	mock.ExpectRollback()
	if err := runMigration052TestTransaction(db); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("Up() = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMigration052UpHardStopsOnDuplicateOccurrence(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(migration052TimeoutsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(migration052PreflightQuery)).WillReturnRows(sqlmock.NewRows([]string{"missing_count"}).AddRow(0))
	mock.ExpectExec(regexp.QuoteMeta(migration052AddOccurrenceColumnsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(migration052SchemaDDL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(migration052BackfillQuery)).WillReturnRows(sqlmock.NewRows([]string{"id", "provider_transaction_id", "movement_kind", "provider_asset_key", "from_address_or_account", "to_address_or_account", "amount", "transfer_mode", "movement_index", "network_family"}))
	mock.ExpectQuery(regexp.QuoteMeta(migration052MissingAliasQuery)).WillReturnRows(sqlmock.NewRows([]string{"missing_count"}).AddRow(0))
	mock.ExpectQuery(regexp.QuoteMeta(migration052DuplicateAliasQuery)).WillReturnRows(sqlmock.NewRows([]string{"duplicate_count"}).AddRow(2))
	mock.ExpectRollback()
	if err := runMigration052TestTransaction(db); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("Up() = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMigration052UpRollsBackOnDatabaseFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(migration052TimeoutsSQL)).WillReturnError(errors.New("timeout setup failed"))
	mock.ExpectRollback()
	if err := runMigration052TestTransaction(db); err == nil || !strings.Contains(err.Error(), "timeouts") {
		t.Fatalf("Up() = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
