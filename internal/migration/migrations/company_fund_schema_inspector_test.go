package migrations

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestLiveSchemaInspectorIntegrationGatePrecedesEnvironmentAndDatabaseOpen(t *testing.T) {
	source, err := os.ReadFile("company_fund_schema_inspector_integration_test.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	gate := strings.Index(text, `os.Getenv("RUN_COMPANY_FUND_SCHEMA_INSPECTOR_INTEGRATION")`)
	databaseURL := strings.Index(text, `os.Getenv("DATABASE_URL")`)
	open := strings.Index(text, `sql.Open("pgx", databaseURL)`)
	if gate < 0 || databaseURL <= gate || open <= databaseURL {
		t.Fatalf("integration gate ordering gate=%d env=%d open=%d", gate, databaseURL, open)
	}
}

func TestLiveSchemaInspectorNeverTrustsSearchPath(t *testing.T) {
	t.Parallel()
	for name, query := range map[string]string{
		"columns":     companyFundOccurrenceColumnsCatalogSQL,
		"index":       companyFundOccurrenceIndexCatalogSQL,
		"constraints": companyFundConstraintsCatalogSQL,
		"function":    companyFundFunctionCatalogSQL,
		"trigger":     companyFundTriggerCatalogSQL,
		"provenance":  companyFundMigrationProvenanceCatalogSQL,
	} {
		if strings.Contains(query, "current_schema()") {
			t.Errorf("%s catalog query trusts current_schema()", name)
		}
		if !strings.Contains(query, "public") {
			t.Errorf("%s catalog query does not bind public", name)
		}
	}
}

func TestInspectLiveCompanyFundSchemaClassifiesAAndBFromCatalogAndProvenance(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		includeB    bool
		recorded052 bool
		recorded053 bool
		wantState   CompanyFundSchemaState
		fingerprint bool
	}{
		{name: "schema A committed", recorded052: true, wantState: CompanyFundSchemaStateA},
		{name: "schema A without provenance", wantState: CompanyFundSchemaStateA},
		{name: "schema A with impossible B provenance", recorded052: true, recorded053: true, wantState: CompanyFundSchemaStatePartial},
		{name: "schema B committed", includeB: true, recorded052: true, recorded053: true, wantState: CompanyFundSchemaStateB, fingerprint: true},
		{name: "schema B provenance missing", includeB: true, recorded052: true, wantState: CompanyFundSchemaStateB, fingerprint: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			expectLiveCompanyFundCatalog(mock, testCase.includeB, testCase.recorded052, testCase.recorded053)
			report, err := InspectLiveCompanyFundSchema(context.Background(), db)
			if err != nil {
				t.Fatal(err)
			}
			if report.State != testCase.wantState || report.Migration052Recorded != testCase.recorded052 || report.Migration053Recorded != testCase.recorded053 || (report.Fingerprint != nil) != testCase.fingerprint || len(report.Digest) != 64 {
				t.Fatalf("report = %#v", report)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInspectLiveCompanyFundSchemaClassifiesInvalidBConstraintAsPartial(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	expectOccurrenceColumns(mock)
	expectOccurrenceIndex(mock)
	valid := validFinalCompanyFundSchemaSnapshot()
	rows := sqlmock.NewRows([]string{"schema", "table", "name", "type", "validated", "definition"})
	for _, name := range valid.ManualConstraintNames {
		rows.AddRow(valid.ConstraintSchemas[name], valid.ConstraintTables[name], name, "c", true, valid.ManualConstraintDefinitions[name])
	}
	rows.AddRow(valid.ConstraintSchemas[migration053ConstraintName], valid.ConstraintTables[migration053ConstraintName], valid.SafeheronRequiredConstraintName, "c", true, "CHECK (true)")
	mock.ExpectQuery(regexp.QuoteMeta(companyFundConstraintsCatalogSQL)).WillReturnRows(rows)
	expectFunctionTriggerAndProvenance(mock, true, false)
	report, err := InspectLiveCompanyFundSchema(context.Background(), db)
	if err != nil || report.State != CompanyFundSchemaStatePartial {
		t.Fatalf("invalid B report = %#v, %v", report, err)
	}
}

func TestInspectLiveCompanyFundSchemaFailsOnCatalogScanAndRowsErrors(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		setup func(sqlmock.Sqlmock)
	}{
		{name: "column scan", setup: func(mock sqlmock.Sqlmock) {
			mock.ExpectQuery(regexp.QuoteMeta(companyFundOccurrenceColumnsCatalogSQL)).WillReturnRows(sqlmock.NewRows([]string{"only_one"}).AddRow("value"))
		}},
		{name: "column rows", setup: func(mock sqlmock.Sqlmock) {
			mock.ExpectQuery(regexp.QuoteMeta(companyFundOccurrenceColumnsCatalogSQL)).WillReturnRows(sqlmock.NewRows([]string{"schema", "table", "column_name", "data_type", "length", "nullable"}).AddRow("public", finalCompanyFundTransactionsTable, "provider_occurrence_key", "character varying", int64(256), "YES").RowError(0, errors.New("row failed")))
		}},
		{name: "constraint scan", setup: func(mock sqlmock.Sqlmock) {
			expectOccurrenceColumns(mock)
			expectOccurrenceIndex(mock)
			mock.ExpectQuery(regexp.QuoteMeta(companyFundConstraintsCatalogSQL)).WillReturnRows(sqlmock.NewRows([]string{"only_one"}).AddRow("value"))
		}},
		{name: "constraint rows", setup: func(mock sqlmock.Sqlmock) {
			expectOccurrenceColumns(mock)
			expectOccurrenceIndex(mock)
			mock.ExpectQuery(regexp.QuoteMeta(companyFundConstraintsCatalogSQL)).WillReturnRows(sqlmock.NewRows([]string{"schema", "table", "name", "type", "validated", "definition"}).AddRow("public", finalCompanyFundTransactionsTable, "constraint", "c", true, "CHECK true").RowError(0, errors.New("row failed")))
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			testCase.setup(mock)
			if _, err := InspectLiveCompanyFundSchema(context.Background(), db); err == nil {
				t.Fatal("catalog row failure accepted")
			}
		})
	}
}

func TestInspectLiveCompanyFundSchemaClassifiesCatalogDriftAsPartial(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	expectOccurrenceColumns(mock)
	mock.ExpectQuery(regexp.QuoteMeta(companyFundOccurrenceIndexCatalogSQL)).WillReturnRows(sqlmock.NewRows([]string{"schema", "table", "name", "unique", "valid", "ready", "columns", "predicate", "definition"}))
	expectConstraints(mock, false)
	expectFunctionTriggerAndProvenance(mock, true, false)
	report, err := InspectLiveCompanyFundSchema(context.Background(), db)
	if err != nil || report.State != CompanyFundSchemaStatePartial {
		t.Fatalf("partial report = %#v, %v", report, err)
	}
}

func TestInspectLiveCompanyFundSchemaFailsClosedOnEveryCatalogQueryError(t *testing.T) {
	for _, step := range []string{"columns", "index", "constraints", "function", "trigger", "provenance"} {
		t.Run(step, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			failure := errors.New(step + " failed")
			if step == "columns" {
				mock.ExpectQuery(regexp.QuoteMeta(companyFundOccurrenceColumnsCatalogSQL)).WillReturnError(failure)
			} else {
				expectOccurrenceColumns(mock)
			}
			if step == "index" {
				mock.ExpectQuery(regexp.QuoteMeta(companyFundOccurrenceIndexCatalogSQL)).WillReturnError(failure)
			} else if step != "columns" {
				expectOccurrenceIndex(mock)
			}
			if step == "constraints" {
				mock.ExpectQuery(regexp.QuoteMeta(companyFundConstraintsCatalogSQL)).WillReturnError(failure)
			} else if step != "columns" && step != "index" {
				expectConstraints(mock, true)
			}
			if step == "function" {
				mock.ExpectQuery(regexp.QuoteMeta(companyFundFunctionCatalogSQL)).WillReturnError(failure)
			} else if step != "columns" && step != "index" && step != "constraints" {
				valid := validFinalCompanyFundSchemaSnapshot()
				mock.ExpectQuery(regexp.QuoteMeta(companyFundFunctionCatalogSQL)).WillReturnRows(functionCatalogRows(valid))
			}
			if step == "trigger" {
				mock.ExpectQuery(regexp.QuoteMeta(companyFundTriggerCatalogSQL)).WillReturnError(failure)
			} else if step == "provenance" {
				mock.ExpectQuery(regexp.QuoteMeta(companyFundTriggerCatalogSQL)).WillReturnRows(triggerCatalogRows(validFinalCompanyFundSchemaSnapshot()))
			}
			if step == "provenance" {
				mock.ExpectQuery(regexp.QuoteMeta(companyFundMigrationProvenanceCatalogSQL)).WillReturnError(failure)
			}
			if _, err := InspectLiveCompanyFundSchema(context.Background(), db); err == nil {
				t.Fatal("catalog failure accepted")
			}
		})
	}
}

func TestManualFunctionAndTriggerCatalogAbsenceAndEmptyAttributesFailClosedWithoutQueryErrors(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	snapshot := FinalCompanyFundSchemaSnapshot{}
	mock.ExpectQuery(regexp.QuoteMeta(companyFundFunctionCatalogSQL)).WillReturnRows(sqlmock.NewRows([]string{"schema", "name", "args", "result", "language", "kind", "oid", "source"}))
	if err := inspectManualProjectionFunction(context.Background(), db, &snapshot); err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(regexp.QuoteMeta(companyFundTriggerCatalogSQL)).WillReturnRows(sqlmock.NewRows([]string{"schema", "table", "function_schema", "function_name", "function_oid", "internal", "enabled", "type", "columns"}))
	if err := inspectManualProjectionTrigger(context.Background(), db, &snapshot); err != nil {
		t.Fatal(err)
	}
	valid := validFinalCompanyFundSchemaSnapshot()
	rows := triggerCatalogRows(valid)
	rows = sqlmock.NewRows([]string{"schema", "table", "function_schema", "function_name", "function_oid", "internal", "enabled", "type", "columns"}).AddRow(
		valid.ManualProjectionTriggerSchema, valid.ManualProjectionTriggerTable,
		valid.ManualProjectionTriggerFunctionSchema, valid.ManualProjectionTriggerFunctionName,
		valid.ManualProjectionTriggerFunctionOID, false, "O", finalManualProjectionTriggerType, "",
	)
	mock.ExpectQuery(regexp.QuoteMeta(companyFundTriggerCatalogSQL)).WillReturnRows(rows)
	if err := inspectManualProjectionTrigger(context.Background(), db, &snapshot); err != nil || len(snapshot.ManualProjectionTriggerColumns) != 0 {
		t.Fatalf("empty tgattr handling = %#v, %v", snapshot, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func expectLiveCompanyFundCatalog(mock sqlmock.Sqlmock, includeB, recorded052, recorded053 bool) {
	expectOccurrenceColumns(mock)
	expectOccurrenceIndex(mock)
	expectConstraints(mock, includeB)
	expectFunctionTriggerAndProvenance(mock, recorded052, recorded053)
}

func expectOccurrenceColumns(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(regexp.QuoteMeta(companyFundOccurrenceColumnsCatalogSQL)).WillReturnRows(
		sqlmock.NewRows([]string{"schema", "table", "column_name", "data_type", "length", "nullable"}).
			AddRow("public", finalCompanyFundTransactionsTable, "provider_occurrence_algorithm_version", "character varying", int64(64), "YES").
			AddRow("public", finalCompanyFundTransactionsTable, "provider_occurrence_key", "character varying", int64(256), "YES"),
	)
}

func expectOccurrenceIndex(mock sqlmock.Sqlmock) {
	valid := validFinalCompanyFundSchemaSnapshot()
	mock.ExpectQuery(regexp.QuoteMeta(companyFundOccurrenceIndexCatalogSQL)).WillReturnRows(
		sqlmock.NewRows([]string{"schema", "table", "name", "unique", "valid", "ready", "columns", "predicate", "definition"}).AddRow(
			valid.SafeheronOccurrenceIndexSchema, valid.SafeheronOccurrenceIndexTable, valid.SafeheronOccurrenceIndexName,
			true, true, true, "provider_occurrence_key", valid.SafeheronOccurrenceIndexPredicate, valid.SafeheronOccurrenceIndexDefinition,
		),
	)
}

func expectConstraints(mock sqlmock.Sqlmock, includeB bool) {
	valid := validFinalCompanyFundSchemaSnapshot()
	rows := sqlmock.NewRows([]string{"schema", "table", "name", "type", "validated", "definition"})
	for _, name := range valid.ManualConstraintNames {
		rows.AddRow(valid.ConstraintSchemas[name], valid.ConstraintTables[name], name, "c", true, valid.ManualConstraintDefinitions[name])
	}
	if includeB {
		rows.AddRow(valid.ConstraintSchemas[migration053ConstraintName], valid.ConstraintTables[migration053ConstraintName], valid.SafeheronRequiredConstraintName, "c", true, valid.SafeheronRequiredConstraintDefinition)
	}
	mock.ExpectQuery(regexp.QuoteMeta(companyFundConstraintsCatalogSQL)).WillReturnRows(rows)
}

func expectFunctionTriggerAndProvenance(mock sqlmock.Sqlmock, recorded052, recorded053 bool) {
	valid := validFinalCompanyFundSchemaSnapshot()
	mock.ExpectQuery(regexp.QuoteMeta(companyFundFunctionCatalogSQL)).WillReturnRows(functionCatalogRows(valid))
	mock.ExpectQuery(regexp.QuoteMeta(companyFundTriggerCatalogSQL)).WillReturnRows(triggerCatalogRows(valid))
	mock.ExpectQuery(regexp.QuoteMeta(companyFundMigrationProvenanceCatalogSQL)).WillReturnRows(sqlmock.NewRows([]string{"migration_052_recorded", "migration_053_recorded"}).AddRow(recorded052, recorded053))
}

func functionCatalogRows(valid FinalCompanyFundSchemaSnapshot) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"schema", "name", "args", "result", "language", "kind", "oid", "source"}).AddRow(
		valid.ManualProjectionFunctionSchema, valid.ManualProjectionFunctionName,
		valid.ManualProjectionFunctionArgumentCount, valid.ManualProjectionFunctionResult,
		valid.ManualProjectionFunctionLanguage, valid.ManualProjectionFunctionKind,
		valid.ManualProjectionFunctionOID, valid.ManualProjectionFunctionSource,
	)
}

func triggerCatalogRows(valid FinalCompanyFundSchemaSnapshot) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"schema", "table", "function_schema", "function_name", "function_oid", "internal", "enabled", "type", "columns"}).AddRow(
		valid.ManualProjectionTriggerSchema, valid.ManualProjectionTriggerTable,
		valid.ManualProjectionTriggerFunctionSchema, valid.ManualProjectionTriggerFunctionName,
		valid.ManualProjectionTriggerFunctionOID, valid.ManualProjectionTriggerInternal,
		valid.ManualProjectionTriggerEnabled, valid.ManualProjectionTriggerType,
		strings.Join(valid.ManualProjectionTriggerColumns, ","),
	)
}

var _ companyFundCatalog = (*sql.DB)(nil)
