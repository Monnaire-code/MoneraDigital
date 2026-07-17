package companyfund

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"
	"monera-digital/internal/companyfundcontract"
)

func TestPlanSafeheronAliasRepairRequiresExactQuiescedV2Evidence(t *testing.T) {
	valid := validSafeheronAliasRepairRequest()
	mutations := []struct {
		name   string
		mutate func(*SafeheronAliasRepairRequest)
	}{
		{"invalid server SHA", func(request *SafeheronAliasRepairRequest) { request.Evidence.V2ServerSHA = "short" }},
		{"invalid frozen hash", func(request *SafeheronAliasRepairRequest) { request.Evidence.FrozenAccountHash = "short" }},
		{"unstable account hash", func(request *SafeheronAliasRepairRequest) {
			request.Evidence.AccountHashSamples[2].SHA256 = strings.Repeat("d", 64)
		}},
		{"too few account samples", func(request *SafeheronAliasRepairRequest) {
			request.Evidence.AccountHashSamples = request.Evidence.AccountHashSamples[:2]
		}},
		{"short stable window", func(request *SafeheronAliasRepairRequest) { request.Evidence.AccountHashStableFor = 5 * time.Second }},
		{"too few drain samples", func(request *SafeheronAliasRepairRequest) {
			request.Evidence.DrainSamples = request.Evidence.DrainSamples[:2]
		}},
	}
	for _, testCase := range mutations {
		t.Run(testCase.name, func(t *testing.T) {
			request := valid
			request.Evidence.AccountHashSamples = append([]SafeheronAliasHashSample(nil), valid.Evidence.AccountHashSamples...)
			request.Evidence.DrainSamples = append([]SafeheronAliasDrainSample(nil), valid.Evidence.DrainSamples...)
			testCase.mutate(&request)
			if _, err := PlanSafeheronAliasRepair(request); err == nil {
				t.Fatal("unsafe repair evidence unexpectedly accepted")
			}
		})
	}
}

func TestPlanSafeheronAliasRepairIsBoundedAndUsesCompleteOccurrenceFacts(t *testing.T) {
	request := validSafeheronAliasRepairRequest()
	plan, err := PlanSafeheronAliasRepair(request)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Scanned != 1 || plan.Repairable != 1 || len(plan.Patches) != 1 || plan.Patches[0].TransactionID != 101 || plan.Patches[0].OccurrenceKey == "" || plan.Patches[0].OccurrenceAlgorithmVersion != SafeheronOccurrenceAlgorithmVersion {
		t.Fatalf("alias repair plan = %#v", plan)
	}
	if strings.Contains(plan.Patches[0].OccurrenceKey, "0xignored-tx-hash") {
		t.Fatal("TxHash must never participate in alias repair")
	}
	request.Limit = 0
	if _, err := PlanSafeheronAliasRepair(request); err == nil {
		t.Fatal("unbounded repair limit must fail")
	}
	request = validSafeheronAliasRepairRequest()
	request.Rows = append(request.Rows, request.Rows[0])
	if _, err := PlanSafeheronAliasRepair(request); err == nil {
		t.Fatal("window larger than limit must fail")
	}
}

func TestPlanSafeheronAliasRepairHardStopsMissingAmbiguousAndDuplicateFacts(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		mutate func(*SafeheronAliasRepairRequest)
	}{
		{"missing", func(request *SafeheronAliasRepairRequest) { delete(request.FactsByTransactionID, 101) }},
		{"ambiguous", func(request *SafeheronAliasRepairRequest) {
			request.FactsByTransactionID[101] = append(request.FactsByTransactionID[101], request.FactsByTransactionID[101][0])
		}},
		{"existing duplicate", func(request *SafeheronAliasRepairRequest) {
			occurrence, _ := BuildSafeheronOccurrence(request.FactsByTransactionID[101][0].Occurrence)
			request.ExistingOccurrenceOwners[occurrence.Key] = 999
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := validSafeheronAliasRepairRequest()
			testCase.mutate(&request)
			if _, err := PlanSafeheronAliasRepair(request); err == nil {
				t.Fatal("identity ambiguity unexpectedly accepted")
			}
		})
	}
}

func TestPlanSafeheronAliasRepairRejectsMalformedWindowsAndFacts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*SafeheronAliasRepairRequest)
	}{
		{"negative cursor", func(request *SafeheronAliasRepairRequest) { request.AfterID = -1 }},
		{"oversized limit", func(request *SafeheronAliasRepairRequest) { request.Limit = maxSafeheronAliasRepairWindow + 1 }},
		{"row not after cursor", func(request *SafeheronAliasRepairRequest) { request.Rows[0].TransactionID = request.AfterID }},
		{"missing movement", func(request *SafeheronAliasRepairRequest) { request.Rows[0].MovementKey = " " }},
		{"wrong identity version", func(request *SafeheronAliasRepairRequest) {
			request.Rows[0].IdentityAlgorithmVersion = SafeheronMovementIdentityAlgorithmVersion
		}},
		{"fact row mismatch", func(request *SafeheronAliasRepairRequest) { request.FactsByTransactionID[101][0].TransactionID = 102 }},
		{"incomplete occurrence", func(request *SafeheronAliasRepairRequest) {
			request.FactsByTransactionID[101][0].Occurrence.RawCoinKey = " "
		}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			request := validSafeheronAliasRepairRequest()
			testCase.mutate(&request)
			if _, err := PlanSafeheronAliasRepair(request); err == nil {
				t.Fatal("malformed repair input unexpectedly accepted")
			}
		})
	}

	request := validSafeheronAliasRepairRequest()
	occurrence, _ := BuildSafeheronOccurrence(request.FactsByTransactionID[101][0].Occurrence)
	request.ExistingOccurrenceOwners[occurrence.Key] = 101
	if _, err := PlanSafeheronAliasRepair(request); err != nil {
		t.Fatalf("same-row occurrence owner should be idempotent: %v", err)
	}

	request = validSafeheronAliasRepairRequest()
	request.Limit = 2
	request.Rows = append(request.Rows, SafeheronAliasNullRow{TransactionID: 102, MovementKey: "v1:other", IdentityAlgorithmVersion: MovementIdentityAlgorithmVersion})
	secondFact := request.FactsByTransactionID[101][0]
	secondFact.TransactionID = 102
	request.FactsByTransactionID[102] = []SafeheronAliasOccurrenceFact{secondFact}
	if _, err := PlanSafeheronAliasRepair(request); err == nil {
		t.Fatal("two repair rows deriving one occurrence must hard stop")
	}
}

func TestPlanSafeheronAliasPatchFailsClosedWhenAuthoritativeResolverRejects(t *testing.T) {
	request := validSafeheronAliasRepairRequest()
	row := request.Rows[0]
	resolverError := errors.New("identity conflict")
	_, err := planSafeheronAliasPatch(row, request.FactsByTransactionID[row.TransactionID], request.ExistingOccurrenceOwners, nil, func([]persistedCompanyFundTransaction, TransactionUpsertInput) (persistedCompanyFundTransaction, bool, error) {
		return persistedCompanyFundTransaction{}, false, resolverError
	})
	if !errors.Is(err, resolverError) {
		t.Fatalf("authoritative resolver rejection = %v", err)
	}
}

func TestSafeheronAliasRepairSQLIsBoundedAndNeverUsesTxHash(t *testing.T) {
	lower := strings.ToLower(selectSafeheronAliasSchemaAReadySQL + selectSafeheronAliasQuiescenceSQL + selectSafeheronAliasNullRepairFactsSQL + selectSafeheronOccurrenceOwnerSQL + updateSafeheronOccurrenceAliasSQL)
	for _, required := range []string{"movement.id > $1", "limit $2", "left join", "provider_extras", "coinkey", "provider_occurrence_key is null", "identity_algorithm_version = 'v1'", "information_schema.columns"} {
		if !strings.Contains(lower, required) {
			t.Fatalf("scanner SQL missing %q", required)
		}
	}
	for _, forbidden := range []string{"tx_hash", "delete from", "merge into"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("scanner SQL contains forbidden %q", forbidden)
		}
	}
}

func TestDBSafeheronAliasScannerReadsBoundedWindowAndApplyRescansAtomically(t *testing.T) {
	request := validSafeheronAliasRepairRequest()
	occurrence, err := BuildSafeheronOccurrence(request.FactsByTransactionID[101][0].Occurrence)
	if err != nil {
		t.Fatal(err)
	}

	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	mock.ExpectBegin()
	expectSafeheronAliasWindow(mock, safeheronAliasFactRows(1))
	mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronOccurrenceOwnerSQL)).WithArgs(occurrence.Key).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	expectSafeheronAliasSafety(mock)
	mock.ExpectCommit()
	result, err := newDBSafeheronAliasRepairScanner(t, db).ScanSafeheronAliasNull(context.Background(), request.Evidence, request.AfterID, request.Limit)
	if err != nil || result.AliasNull != 1 || result.Repairable != 1 || result.Missing != 0 || result.Duplicate != 0 || result.Ambiguous != 0 {
		t.Fatalf("alias scan = %#v, %v", result, err)
	}
	assertCompanyFundMockExpectations(t, mock)

	db, mock = newCompanyFundMockDB(t)
	defer db.Close()
	mock.ExpectBegin()
	expectSafeheronAliasWindow(mock, safeheronAliasFactRows(1))
	mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronOccurrenceOwnerSQL)).WithArgs(occurrence.Key).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	patch := result.Plan.Patches[0]
	mock.ExpectQuery(regexp.QuoteMeta(updateSafeheronOccurrenceAliasSQL)).WithArgs(patch.TransactionID, patch.OccurrenceKey, patch.OccurrenceAlgorithmVersion).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(patch.TransactionID))
	input := safeheronV2QualityInput(patch.expectedMovementKey, patch.OccurrenceKey, testSafeheronPrincipalAsset(), false)
	expectSafeheronIdentityLookup(mock, input, safeheronIdentityRows(patch.TransactionID, request.Rows[0].MovementKey, MovementIdentityAlgorithmVersion, patch.OccurrenceKey, testSafeheronPrincipalAsset(), false))
	expectSafeheronAliasSafety(mock)
	mock.ExpectCommit()
	applied, err := newDBSafeheronAliasRepairScanner(t, db).ScanAndApplySafeheronAliasNull(context.Background(), request.Evidence, request.AfterID, request.Limit)
	if err != nil || applied.Repairable != 1 {
		t.Fatalf("atomic rescan/apply = %#v, %v", applied, err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestDBSafeheronAliasScannerRejectsCurrentAccountHashMismatch(t *testing.T) {
	request := validSafeheronAliasRepairRequest()
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronAliasSchemaAReadySQL)).WillReturnRows(sqlmock.NewRows([]string{"ready"}).AddRow(true))
	expectSafeheronAliasQuiescence(mock)
	mock.ExpectQuery(regexp.QuoteMeta(selectCanonicalAccountPolicyRecordsSQL)).WillReturnRows(sqlmock.NewRows([]string{"account_id", "channel", "provider_account_key", "address", "network_family", "account_enabled", "asset_key", "policy_enabled"}).AddRow(2, "SAFEHERON", "changed-account", "0xdef", "EVM", true, "", false))
	mock.ExpectRollback()
	if _, err := newDBSafeheronAliasRepairScanner(t, db).ScanSafeheronAliasNull(context.Background(), request.Evidence, 100, 1); err == nil {
		t.Fatal("current DB account hash mismatch accepted")
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestDBSafeheronAliasApplyRollsBackManifestOrEnvironmentDriftBeforeCommit(t *testing.T) {
	request := validSafeheronAliasRepairRequest()
	occurrence, _ := BuildSafeheronOccurrence(request.FactsByTransactionID[101][0].Occurrence)
	plan, _ := PlanSafeheronAliasRepair(request)
	patch := plan.Patches[0]
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	mock.ExpectBegin()
	expectSafeheronAliasWindow(mock, safeheronAliasFactRows(1))
	mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronOccurrenceOwnerSQL)).WithArgs(occurrence.Key).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(regexp.QuoteMeta(updateSafeheronOccurrenceAliasSQL)).WithArgs(patch.TransactionID, patch.OccurrenceKey, patch.OccurrenceAlgorithmVersion).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(patch.TransactionID))
	input := safeheronV2QualityInput(patch.expectedMovementKey, patch.OccurrenceKey, testSafeheronPrincipalAsset(), false)
	expectSafeheronIdentityLookup(mock, input, safeheronIdentityRows(patch.TransactionID, request.Rows[0].MovementKey, MovementIdentityAlgorithmVersion, patch.OccurrenceKey, testSafeheronPrincipalAsset(), false))
	mock.ExpectRollback()
	scanner := newDBSafeheronAliasRepairScanner(t, db)
	scanner.beforeCommit = func() {
		if err := os.WriteFile(scanner.probe.EnvironmentPath, []byte("COMPANY_FUND_START_BACKGROUND_WORKERS=true\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := scanner.ScanAndApplySafeheronAliasNull(context.Background(), request.Evidence, 100, 1); err == nil {
		t.Fatal("live environment drift committed")
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestDBSafeheronAliasDryRunRollsBackManifestOrEnvironmentDriftBeforeCommit(t *testing.T) {
	request := validSafeheronAliasRepairRequest()
	occurrence, _ := BuildSafeheronOccurrence(request.FactsByTransactionID[101][0].Occurrence)
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	mock.ExpectBegin()
	expectSafeheronAliasWindow(mock, safeheronAliasFactRows(1))
	mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronOccurrenceOwnerSQL)).WithArgs(occurrence.Key).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectRollback()
	scanner := newDBSafeheronAliasRepairScanner(t, db)
	scanner.beforeCommit = func() {
		if err := os.WriteFile(scanner.probe.ManifestPath, []byte(`{"server_sha":"`+strings.Repeat("b", 40)+`"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := scanner.ScanSafeheronAliasNull(context.Background(), request.Evidence, 100, 1); err == nil {
		t.Fatal("live manifest drift committed")
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestDBSafeheronAliasDryRunRollsBackWhenEvidenceExpiresBeforeCommit(t *testing.T) {
	request := validSafeheronAliasRepairRequest()
	last := request.Evidence.DrainSamples[len(request.Evidence.DrainSamples)-1].At
	occurrence, _ := BuildSafeheronOccurrence(request.FactsByTransactionID[101][0].Occurrence)
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	mock.ExpectBegin()
	expectSafeheronAliasWindow(mock, safeheronAliasFactRows(1))
	mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronOccurrenceOwnerSQL)).WithArgs(occurrence.Key).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectRollback()
	scanner := newDBSafeheronAliasRepairScanner(t, db)
	trustedNow := last.Add(SafeheronAliasEvidenceFreshnessWindow - time.Second)
	scanner.now = func() time.Time { return trustedNow }
	scanner.beforeCommit = func() { trustedNow = last.Add(SafeheronAliasEvidenceFreshnessWindow + time.Nanosecond) }
	if _, err := scanner.ScanSafeheronAliasNull(context.Background(), request.Evidence, 100, 1); err == nil {
		t.Fatal("evidence that expired during the scan committed")
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestDBSafeheronAliasApplyRejectsStaleEvidenceBeforeTransaction(t *testing.T) {
	request := validSafeheronAliasRepairRequest()
	last := request.Evidence.DrainSamples[len(request.Evidence.DrainSamples)-1].At
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	scanner := newDBSafeheronAliasRepairScanner(t, db)
	scanner.now = func() time.Time { return last.Add(SafeheronAliasEvidenceFreshnessWindow + time.Nanosecond) }
	if _, err := scanner.ScanAndApplySafeheronAliasNull(context.Background(), request.Evidence, 100, 1); err == nil {
		t.Fatal("stale apply evidence opened a transaction")
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestDBSafeheronAliasApplyRollsBackWhenEvidenceExpiresOrLiveSafetyChangesBeforeCommit(t *testing.T) {
	request := validSafeheronAliasRepairRequest()
	last := request.Evidence.DrainSamples[len(request.Evidence.DrainSamples)-1].At
	occurrence, _ := BuildSafeheronOccurrence(request.FactsByTransactionID[101][0].Occurrence)
	plan, _ := PlanSafeheronAliasRepair(request)
	patch := plan.Patches[0]
	for _, testCase := range []struct {
		name         string
		beforeCommit func(*DBSafeheronAliasRepairScanner)
		expectFinal  func(sqlmock.Sqlmock)
	}{
		{name: "evidence expires", beforeCommit: func(scanner *DBSafeheronAliasRepairScanner) {
			scanner.now = func() time.Time { return last.Add(SafeheronAliasEvidenceFreshnessWindow + time.Nanosecond) }
		}},
		{name: "live schema changes", expectFinal: func(mock sqlmock.Sqlmock) {
			mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronAliasSchemaAReadySQL)).WillReturnRows(sqlmock.NewRows([]string{"ready"}).AddRow(false))
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db, mock := newCompanyFundMockDB(t)
			defer db.Close()
			mock.ExpectBegin()
			expectSafeheronAliasWindow(mock, safeheronAliasFactRows(1))
			mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronOccurrenceOwnerSQL)).WithArgs(occurrence.Key).WillReturnRows(sqlmock.NewRows([]string{"id"}))
			mock.ExpectQuery(regexp.QuoteMeta(updateSafeheronOccurrenceAliasSQL)).WithArgs(patch.TransactionID, patch.OccurrenceKey, patch.OccurrenceAlgorithmVersion).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(patch.TransactionID))
			input := safeheronV2QualityInput(patch.expectedMovementKey, patch.OccurrenceKey, testSafeheronPrincipalAsset(), false)
			expectSafeheronIdentityLookup(mock, input, safeheronIdentityRows(patch.TransactionID, request.Rows[0].MovementKey, MovementIdentityAlgorithmVersion, patch.OccurrenceKey, testSafeheronPrincipalAsset(), false))
			if testCase.expectFinal != nil {
				testCase.expectFinal(mock)
			}
			mock.ExpectRollback()
			scanner := newDBSafeheronAliasRepairScanner(t, db)
			if testCase.beforeCommit != nil {
				scanner.beforeCommit = func() { testCase.beforeCommit(scanner) }
			}
			if _, err := scanner.ScanAndApplySafeheronAliasNull(context.Background(), request.Evidence, 100, 1); err == nil {
				t.Fatal("unsafe commit-time state accepted")
			}
			assertCompanyFundMockExpectations(t, mock)
		})
	}
}

func TestSafeheronAliasTrustedClockAndTransactionalFreshnessFailClosed(t *testing.T) {
	scanner := &DBSafeheronAliasRepairScanner{}
	if !scanner.trustedNow().IsZero() {
		t.Fatal("missing trusted clock returned a usable time")
	}
	if err := revalidateSafeheronAliasSafetyTx(context.Background(), nil, SafeheronAliasRepairEvidence{}, time.Now()); err == nil {
		t.Fatal("invalid evidence reached transactional queries")
	}
	evidence := validSafeheronAliasRepairRequest().Evidence
	last := evidence.DrainSamples[len(evidence.DrainSamples)-1].At
	if err := revalidateSafeheronAliasSafetyTx(context.Background(), nil, evidence, last.Add(SafeheronAliasEvidenceFreshnessWindow+time.Nanosecond)); err == nil {
		t.Fatal("stale evidence reached transactional queries")
	}
}

func TestSafeheronAliasCanonicalHashAndPatchHelpersFailClosed(t *testing.T) {
	t.Run("canonical query failures", func(t *testing.T) {
		for _, rows := range []*sqlmock.Rows{
			sqlmock.NewRows([]string{"bad"}).AddRow(1),
			sqlmock.NewRows([]string{"account_id", "channel", "provider_account_key", "address", "network_family", "account_enabled", "asset_key", "policy_enabled"}).AddRow(1, "SAFEHERON", "account", "0xabc", "EVM", true, "", false).RowError(0, errors.New("rows failed")),
			sqlmock.NewRows([]string{"account_id", "channel", "provider_account_key", "address", "network_family", "account_enabled", "asset_key", "policy_enabled"}).AddRow(1, "SAFEHERON", "", "0xabc", "EVM", true, "", false),
		} {
			db, mock := newCompanyFundMockDB(t)
			mock.ExpectBegin()
			mock.ExpectQuery(regexp.QuoteMeta(selectCanonicalAccountPolicyRecordsSQL)).WillReturnRows(rows)
			mock.ExpectRollback()
			tx, err := db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := loadCanonicalAccountPolicyHash(context.Background(), tx); err == nil {
				t.Fatal("invalid canonical query result accepted")
			}
			_ = tx.Rollback()
			assertCompanyFundMockExpectations(t, mock)
			db.Close()
		}
		db, mock := newCompanyFundMockDB(t)
		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(selectCanonicalAccountPolicyRecordsSQL)).WillReturnError(errors.New("query failed"))
		mock.ExpectRollback()
		tx, _ := db.BeginTx(context.Background(), nil)
		if _, err := loadCanonicalAccountPolicyHash(context.Background(), tx); err == nil {
			t.Fatal("canonical query failure ignored")
		}
		_ = tx.Rollback()
		assertCompanyFundMockExpectations(t, mock)
		db.Close()
	})

	t.Run("conditional patch failures", func(t *testing.T) {
		request := validSafeheronAliasRepairRequest()
		plan, _ := PlanSafeheronAliasRepair(request)
		patch := plan.Patches[0]
		for _, testCase := range []struct {
			name  string
			patch SafeheronAliasPatch
			setup func(sqlmock.Sqlmock)
		}{
			{name: "incomplete", patch: SafeheronAliasPatch{}},
			{name: "changed", patch: patch, setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(updateSafeheronOccurrenceAliasSQL)).WithArgs(patch.TransactionID, patch.OccurrenceKey, patch.OccurrenceAlgorithmVersion).WillReturnRows(sqlmock.NewRows([]string{"id"}))
			}},
			{name: "resolver mismatch", patch: patch, setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(updateSafeheronOccurrenceAliasSQL)).WithArgs(patch.TransactionID, patch.OccurrenceKey, patch.OccurrenceAlgorithmVersion).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(patch.TransactionID))
				input := safeheronV2QualityInput(patch.expectedMovementKey, patch.OccurrenceKey, testSafeheronPrincipalAsset(), false)
				expectSafeheronIdentityLookup(mock, input, safeheronIdentityRows(999, request.Rows[0].MovementKey, MovementIdentityAlgorithmVersion, patch.OccurrenceKey, testSafeheronPrincipalAsset(), false))
			}},
		} {
			t.Run(testCase.name, func(t *testing.T) {
				db, mock := newCompanyFundMockDB(t)
				mock.ExpectBegin()
				if testCase.setup != nil {
					testCase.setup(mock)
				}
				mock.ExpectRollback()
				tx, _ := db.BeginTx(context.Background(), nil)
				if err := applySafeheronAliasPatchTx(context.Background(), tx, testCase.patch); err == nil {
					t.Fatal("unsafe alias patch accepted")
				}
				_ = tx.Rollback()
				assertCompanyFundMockExpectations(t, mock)
				db.Close()
			})
		}
	})
}

func TestDBSafeheronAliasScannerReportsAndHardStopsIncompleteIdentityWindows(t *testing.T) {
	request := validSafeheronAliasRepairRequest()
	tests := []struct {
		name                                      string
		rows                                      *sqlmock.Rows
		owner                                     *sqlmock.Rows
		wantMissing, wantDuplicate, wantAmbiguous int
	}{
		{name: "missing", rows: safeheronAliasMissingFactRows(), wantMissing: 1},
		{name: "ambiguous", rows: safeheronAliasFactRows(2), wantAmbiguous: 1},
		{name: "duplicate", rows: safeheronAliasFactRows(1), owner: sqlmock.NewRows([]string{"id"}).AddRow(999), wantDuplicate: 1},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			db, mock := newCompanyFundMockDB(t)
			defer db.Close()
			mock.ExpectBegin()
			expectSafeheronAliasWindow(mock, testCase.rows)
			if testCase.owner != nil {
				occurrence, _ := BuildSafeheronOccurrence(request.FactsByTransactionID[101][0].Occurrence)
				mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronOccurrenceOwnerSQL)).WithArgs(occurrence.Key).WillReturnRows(testCase.owner)
			}
			mock.ExpectRollback()
			result, err := newDBSafeheronAliasRepairScanner(t, db).ScanAndApplySafeheronAliasNull(context.Background(), request.Evidence, request.AfterID, request.Limit)
			if err == nil || result.Missing != testCase.wantMissing || result.Duplicate != testCase.wantDuplicate || result.Ambiguous != testCase.wantAmbiguous {
				t.Fatalf("blocked scan = %#v, %v", result, err)
			}
			assertCompanyFundMockExpectations(t, mock)
		})
	}
}

func TestDBSafeheronAliasScannerErrorContracts(t *testing.T) {
	request := validSafeheronAliasRepairRequest()
	if _, err := newDBSafeheronAliasRepairScanner(t, nil).ScanSafeheronAliasNull(context.Background(), request.Evidence, 100, 1); err == nil {
		t.Fatal("nil scan database accepted")
	}
	if _, err := newDBSafeheronAliasRepairScanner(t, nil).ScanAndApplySafeheronAliasNull(context.Background(), request.Evidence, 100, 1); err == nil {
		t.Fatal("nil apply database accepted")
	}
	for _, apply := range []bool{false, true} {
		db, mock := newCompanyFundMockDB(t)
		scanner := newDBSafeheronAliasRepairScanner(t, db)
		scanner.probe.ManifestPath = filepath.Join(t.TempDir(), "missing-manifest")
		var err error
		if apply {
			_, err = scanner.ScanAndApplySafeheronAliasNull(context.Background(), request.Evidence, 100, 1)
		} else {
			_, err = scanner.ScanSafeheronAliasNull(context.Background(), request.Evidence, 100, 1)
		}
		if err == nil {
			t.Fatal("unreadable live probe accepted")
		}
		assertCompanyFundMockExpectations(t, mock)
		db.Close()
	}
	for _, apply := range []bool{false, true} {
		db, mock := newCompanyFundMockDB(t)
		mock.ExpectBegin().WillReturnError(errors.New("begin failed"))
		writer := newDBSafeheronAliasRepairScanner(t, db)
		var err error
		if apply {
			_, err = writer.ScanAndApplySafeheronAliasNull(context.Background(), request.Evidence, 100, 1)
		} else {
			_, err = writer.ScanSafeheronAliasNull(context.Background(), request.Evidence, 100, 1)
		}
		if err == nil {
			t.Fatal("begin failure did not propagate")
		}
		assertCompanyFundMockExpectations(t, mock)
		db.Close()
	}

	t.Run("invalid evidence", func(t *testing.T) {
		db, mock := newCompanyFundMockDB(t)
		defer db.Close()
		evidence := request.Evidence
		evidence.V2ServerSHA = "short"
		if _, err := newDBSafeheronAliasRepairScanner(t, db).ScanSafeheronAliasNull(context.Background(), evidence, 100, 1); err == nil {
			t.Fatal("unsafe evidence accepted")
		}
		assertCompanyFundMockExpectations(t, mock)
	})

	t.Run("invalid window", func(t *testing.T) {
		db, mock := newCompanyFundMockDB(t)
		defer db.Close()
		mock.ExpectBegin()
		mock.ExpectRollback()
		if _, err := newDBSafeheronAliasRepairScanner(t, db).ScanSafeheronAliasNull(context.Background(), request.Evidence, -1, 1); err == nil {
			t.Fatal("invalid window accepted")
		}
		assertCompanyFundMockExpectations(t, mock)
	})

	t.Run("schema and query failures", func(t *testing.T) {
		for _, schemaRows := range []*sqlmock.Rows{
			sqlmock.NewRows([]string{"ready"}).AddRow(false),
			sqlmock.NewRows([]string{"ready"}).RowError(0, errors.New("schema failed")),
		} {
			db, mock := newCompanyFundMockDB(t)
			mock.ExpectBegin()
			mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronAliasSchemaAReadySQL)).WillReturnRows(schemaRows)
			mock.ExpectRollback()
			if _, err := newDBSafeheronAliasRepairScanner(t, db).ScanSafeheronAliasNull(context.Background(), request.Evidence, 100, 1); err == nil {
				t.Fatal("schema failure accepted")
			}
			assertCompanyFundMockExpectations(t, mock)
			db.Close()
		}
		for _, quiescenceRows := range []*sqlmock.Rows{
			sqlmock.NewRows([]string{"provider", "sync", "in_flight", "old_sessions"}).AddRow(1, 0, 0, 0),
			sqlmock.NewRows([]string{"provider", "sync", "in_flight", "old_sessions"}).RowError(0, errors.New("quiescence failed")),
		} {
			db, mock := newCompanyFundMockDB(t)
			mock.ExpectBegin()
			mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronAliasSchemaAReadySQL)).WillReturnRows(sqlmock.NewRows([]string{"ready"}).AddRow(true))
			mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronAliasQuiescenceSQL)).WithArgs(strings.Repeat("a", 40)).WillReturnRows(quiescenceRows)
			mock.ExpectRollback()
			if _, err := newDBSafeheronAliasRepairScanner(t, db).ScanSafeheronAliasNull(context.Background(), request.Evidence, 100, 1); err == nil {
				t.Fatal("active or unreadable quiescence accepted")
			}
			assertCompanyFundMockExpectations(t, mock)
			db.Close()
		}
		db, mock := newCompanyFundMockDB(t)
		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronAliasSchemaAReadySQL)).WillReturnRows(sqlmock.NewRows([]string{"ready"}).AddRow(true))
		expectSafeheronAliasQuiescence(mock)
		mock.ExpectQuery(regexp.QuoteMeta(selectCanonicalAccountPolicyRecordsSQL)).WillReturnError(errors.New("canonical query failed"))
		mock.ExpectRollback()
		if _, err := newDBSafeheronAliasRepairScanner(t, db).ScanSafeheronAliasNull(context.Background(), request.Evidence, 100, 1); err == nil {
			t.Fatal("canonical hash load failure accepted")
		}
		assertCompanyFundMockExpectations(t, mock)
		db.Close()

		db, mock = newCompanyFundMockDB(t)
		defer db.Close()
		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronAliasSchemaAReadySQL)).WillReturnRows(sqlmock.NewRows([]string{"ready"}).AddRow(true))
		expectSafeheronAliasQuiescence(mock)
		expectSafeheronAliasCanonicalRecords(mock)
		mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronAliasNullRepairFactsSQL)).WillReturnError(errors.New("query failed"))
		mock.ExpectRollback()
		if _, err := newDBSafeheronAliasRepairScanner(t, db).ScanSafeheronAliasNull(context.Background(), request.Evidence, 100, 1); err == nil {
			t.Fatal("window query failure accepted")
		}
		assertCompanyFundMockExpectations(t, mock)
	})

	t.Run("scan and owner failures", func(t *testing.T) {
		badRows := sqlmock.NewRows([]string{"only_one"}).AddRow(101)
		db, mock := newCompanyFundMockDB(t)
		mock.ExpectBegin()
		expectSafeheronAliasWindow(mock, badRows)
		mock.ExpectRollback()
		if _, err := newDBSafeheronAliasRepairScanner(t, db).ScanSafeheronAliasNull(context.Background(), request.Evidence, 100, 1); err == nil {
			t.Fatal("malformed scan row accepted")
		}
		assertCompanyFundMockExpectations(t, mock)
		db.Close()

		db, mock = newCompanyFundMockDB(t)
		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronAliasSchemaAReadySQL)).WillReturnRows(sqlmock.NewRows([]string{"ready"}).AddRow(true))
		expectSafeheronAliasQuiescence(mock)
		expectSafeheronAliasCanonicalRecords(mock)
		mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronAliasNullRepairFactsSQL)).WillReturnRows(safeheronAliasFactRows(1).RowError(0, errors.New("rows failed")))
		mock.ExpectRollback()
		if _, err := newDBSafeheronAliasRepairScanner(t, db).ScanSafeheronAliasNull(context.Background(), request.Evidence, 100, 1); err == nil {
			t.Fatal("window row error accepted")
		}
		assertCompanyFundMockExpectations(t, mock)
		db.Close()

		occurrence, _ := BuildSafeheronOccurrence(request.FactsByTransactionID[101][0].Occurrence)
		db, mock = newCompanyFundMockDB(t)
		mock.ExpectBegin()
		expectSafeheronAliasWindow(mock, safeheronAliasFactRows(1))
		mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronOccurrenceOwnerSQL)).WithArgs(occurrence.Key).WillReturnError(errors.New("owner query failed"))
		mock.ExpectRollback()
		if _, err := newDBSafeheronAliasRepairScanner(t, db).ScanSafeheronAliasNull(context.Background(), request.Evidence, 100, 1); err == nil {
			t.Fatal("owner query failure accepted")
		}
		assertCompanyFundMockExpectations(t, mock)
		db.Close()

		db, mock = newCompanyFundMockDB(t)
		mock.ExpectBegin()
		expectSafeheronAliasWindow(mock, safeheronAliasFactRows(1))
		mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronOccurrenceOwnerSQL)).WithArgs(occurrence.Key).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("not-an-int"))
		mock.ExpectRollback()
		if _, err := newDBSafeheronAliasRepairScanner(t, db).ScanSafeheronAliasNull(context.Background(), request.Evidence, 100, 1); err == nil {
			t.Fatal("owner scan failure accepted")
		}
		assertCompanyFundMockExpectations(t, mock)
		db.Close()

		db, mock = newCompanyFundMockDB(t)
		mock.ExpectBegin()
		expectSafeheronAliasWindow(mock, safeheronAliasFactRows(1))
		mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronOccurrenceOwnerSQL)).WithArgs(occurrence.Key).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(101).RowError(1, errors.New("owner rows failed")))
		if _, err := newDBSafeheronAliasRepairScanner(t, db).ScanSafeheronAliasNull(context.Background(), request.Evidence, 100, 1); err == nil {
			t.Fatal("owner row error accepted")
		}
		assertCompanyFundMockExpectations(t, mock)
		db.Close()
	})

	t.Run("malformed facts and duplicate window", func(t *testing.T) {
		for _, rows := range []*sqlmock.Rows{safeheronAliasRowsWithRawFact("bad", "ETHEREUM_USDT", "v1:legacy"), safeheronAliasRowsWithRawFact("1", "", "v1:legacy")} {
			db, mock := newCompanyFundMockDB(t)
			mock.ExpectBegin()
			expectSafeheronAliasWindow(mock, rows)
			mock.ExpectRollback()
			_, err := newDBSafeheronAliasRepairScanner(t, db).ScanAndApplySafeheronAliasNull(context.Background(), request.Evidence, 100, 1)
			if err == nil {
				t.Fatal("malformed scanner input accepted")
			}
			assertCompanyFundMockExpectations(t, mock)
			db.Close()
		}

		occurrence, _ := BuildSafeheronOccurrence(request.FactsByTransactionID[101][0].Occurrence)
		db, mock := newCompanyFundMockDB(t)
		mock.ExpectBegin()
		expectSafeheronAliasWindow(mock, safeheronAliasRowsWithRawFact("1", "ETHEREUM_USDT", " "))
		mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronOccurrenceOwnerSQL)).WithArgs(occurrence.Key).WillReturnRows(sqlmock.NewRows([]string{"id"}))
		mock.ExpectRollback()
		if _, err := newDBSafeheronAliasRepairScanner(t, db).ScanAndApplySafeheronAliasNull(context.Background(), request.Evidence, 100, 1); err == nil {
			t.Fatal("invalid legacy row reached repair")
		}
		assertCompanyFundMockExpectations(t, mock)
		db.Close()

		db, mock = newCompanyFundMockDB(t)
		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronAliasSchemaAReadySQL)).WillReturnRows(sqlmock.NewRows([]string{"ready"}).AddRow(true))
		expectSafeheronAliasQuiescence(mock)
		expectSafeheronAliasCanonicalRecords(mock)
		mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronAliasNullRepairFactsSQL)).WithArgs(int64(100), 2).WillReturnRows(safeheronAliasDuplicateWindowRows())
		mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronOccurrenceOwnerSQL)).WithArgs(occurrence.Key).WillReturnRows(sqlmock.NewRows([]string{"id"}))
		mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronOccurrenceOwnerSQL)).WithArgs(occurrence.Key).WillReturnRows(sqlmock.NewRows([]string{"id"}))
		mock.ExpectRollback()
		result, err := newDBSafeheronAliasRepairScanner(t, db).ScanAndApplySafeheronAliasNull(context.Background(), request.Evidence, 100, 2)
		if err == nil || result.Duplicate != 1 {
			t.Fatalf("duplicate planned occurrence = %#v, %v", result, err)
		}
		assertCompanyFundMockExpectations(t, mock)
		db.Close()
	})

	t.Run("commit failures", func(t *testing.T) {
		occurrence, _ := BuildSafeheronOccurrence(request.FactsByTransactionID[101][0].Occurrence)
		db, mock := newCompanyFundMockDB(t)
		mock.ExpectBegin()
		expectSafeheronAliasWindow(mock, safeheronAliasFactRows(1))
		mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronOccurrenceOwnerSQL)).WithArgs(occurrence.Key).WillReturnRows(sqlmock.NewRows([]string{"id"}))
		expectSafeheronAliasSafety(mock)
		mock.ExpectCommit().WillReturnError(errors.New("commit failed"))
		if _, err := newDBSafeheronAliasRepairScanner(t, db).ScanSafeheronAliasNull(context.Background(), request.Evidence, 100, 1); err == nil {
			t.Fatal("scan commit failure accepted")
		}
		assertCompanyFundMockExpectations(t, mock)
		db.Close()

		db, mock = newCompanyFundMockDB(t)
		mock.ExpectBegin()
		expectSafeheronAliasWindow(mock, safeheronAliasFactRows(1))
		mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronOccurrenceOwnerSQL)).WithArgs(occurrence.Key).WillReturnRows(sqlmock.NewRows([]string{"id"}))
		plan, _ := PlanSafeheronAliasRepair(request)
		patch := plan.Patches[0]
		mock.ExpectQuery(regexp.QuoteMeta(updateSafeheronOccurrenceAliasSQL)).WithArgs(patch.TransactionID, patch.OccurrenceKey, patch.OccurrenceAlgorithmVersion).WillReturnError(errors.New("update failed"))
		mock.ExpectRollback()
		if _, err := newDBSafeheronAliasRepairScanner(t, db).ScanAndApplySafeheronAliasNull(context.Background(), request.Evidence, 100, 1); err == nil {
			t.Fatal("atomic update failure accepted")
		}
		assertCompanyFundMockExpectations(t, mock)
		db.Close()

		db, mock = newCompanyFundMockDB(t)
		mock.ExpectBegin()
		expectSafeheronAliasWindow(mock, safeheronAliasFactRows(1))
		mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronOccurrenceOwnerSQL)).WithArgs(occurrence.Key).WillReturnRows(sqlmock.NewRows([]string{"id"}))
		plan, _ = PlanSafeheronAliasRepair(request)
		patch = plan.Patches[0]
		mock.ExpectQuery(regexp.QuoteMeta(updateSafeheronOccurrenceAliasSQL)).WithArgs(patch.TransactionID, patch.OccurrenceKey, patch.OccurrenceAlgorithmVersion).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(patch.TransactionID))
		input := safeheronV2QualityInput(patch.expectedMovementKey, patch.OccurrenceKey, testSafeheronPrincipalAsset(), false)
		expectSafeheronIdentityLookup(mock, input, safeheronIdentityRows(patch.TransactionID, request.Rows[0].MovementKey, MovementIdentityAlgorithmVersion, patch.OccurrenceKey, testSafeheronPrincipalAsset(), false))
		expectSafeheronAliasSafety(mock)
		mock.ExpectCommit().WillReturnError(errors.New("commit failed"))
		if _, err := newDBSafeheronAliasRepairScanner(t, db).ScanAndApplySafeheronAliasNull(context.Background(), request.Evidence, 100, 1); err == nil {
			t.Fatal("apply commit failure accepted")
		}
		assertCompanyFundMockExpectations(t, mock)
		db.Close()
	})
}

func expectSafeheronAliasWindow(mock sqlmock.Sqlmock, rows *sqlmock.Rows) {
	expectSafeheronAliasSafety(mock)
	mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronAliasNullRepairFactsSQL)).WithArgs(int64(100), 1).WillReturnRows(rows)
}

func expectSafeheronAliasSafety(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronAliasSchemaAReadySQL)).WillReturnRows(sqlmock.NewRows([]string{"ready"}).AddRow(true))
	expectSafeheronAliasQuiescence(mock)
	expectSafeheronAliasCanonicalRecords(mock)
}

func expectSafeheronAliasQuiescence(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronAliasQuiescenceSQL)).WithArgs(strings.Repeat("a", 40)).WillReturnRows(sqlmock.NewRows([]string{"provider", "sync", "in_flight", "old_sessions"}).AddRow(0, 0, 0, 0))
}

func expectSafeheronAliasCanonicalRecords(mock sqlmock.Sqlmock) {
	record := safeheronAliasCanonicalRecords()[0]
	mock.ExpectQuery(regexp.QuoteMeta(selectCanonicalAccountPolicyRecordsSQL)).WillReturnRows(sqlmock.NewRows([]string{"account_id", "channel", "provider_account_key", "address", "network_family", "account_enabled", "asset_key", "policy_enabled"}).AddRow(record.AccountID, record.Channel, record.ProviderAccountKey, record.Address, record.NetworkFamily, record.AccountEnabled, record.AssetKey, record.PolicyEnabled))
}

func safeheronAliasCanonicalRecords() []companyfundcontract.CanonicalAccountPolicyRecord {
	return []companyfundcontract.CanonicalAccountPolicyRecord{{AccountID: 1, Channel: "SAFEHERON", ProviderAccountKey: "account-a", Address: "0xabc", NetworkFamily: "EVM", AccountEnabled: true}}
}

func safeheronAliasCanonicalHash() string {
	exported, err := companyfundcontract.BuildCanonicalAccountPolicyExport(safeheronAliasCanonicalRecords())
	if err != nil {
		panic(err)
	}
	return exported.SHA256
}

func newDBSafeheronAliasRepairScanner(t *testing.T, db *sql.DB) *DBSafeheronAliasRepairScanner {
	t.Helper()
	dir := t.TempDir()
	sha := strings.Repeat("a", 40)
	manifest := filepath.Join(dir, "release-manifest.json")
	environment := filepath.Join(dir, ".env")
	if err := os.WriteFile(manifest, []byte(`{"server_sha":"`+sha+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(environment, []byte("COMPANY_FUND_START_BACKGROUND_WORKERS=false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	probe := SafeheronAliasLiveProbe{ManifestPath: manifest, EnvironmentPath: environment, ExpectedV2SHA: sha}
	baseline, err := CaptureSafeheronAliasLiveProbe(probe)
	if err != nil {
		t.Fatal(err)
	}
	scanner := NewDBSafeheronAliasRepairScanner(db, probe, baseline)
	scanner.now = func() time.Time { return time.Date(2026, 7, 15, 1, 0, 10, 0, time.UTC) }
	return scanner
}

func safeheronAliasFactRows(count int) *sqlmock.Rows {
	rows := sqlmock.NewRows([]string{"id", "movement_key", "identity_algorithm_version", "fact_id", "provider_transaction_id", "movement_kind", "raw_coin_key", "source", "destination", "amount", "transfer_mode", "movement_index"})
	for index := 0; index < count; index++ {
		rows.AddRow(101, "v1:legacy", MovementIdentityAlgorithmVersion, index+1, "safeheron-tx", string(MovementKindPrincipal), "ETHEREUM_USDT", "0xfrom", "0xto", "1", string(TransferModeSingle), 0)
	}
	return rows
}

func safeheronAliasMissingFactRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "movement_key", "identity_algorithm_version", "fact_id", "provider_transaction_id", "movement_kind", "raw_coin_key", "source", "destination", "amount", "transfer_mode", "movement_index"}).
		AddRow(101, "v1:legacy", MovementIdentityAlgorithmVersion, nil, nil, nil, nil, nil, nil, nil, nil, nil)
}

func safeheronAliasRowsWithRawFact(amount, rawCoinKey, movementKey string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "movement_key", "identity_algorithm_version", "fact_id", "provider_transaction_id", "movement_kind", "raw_coin_key", "source", "destination", "amount", "transfer_mode", "movement_index"}).
		AddRow(101, movementKey, MovementIdentityAlgorithmVersion, 1, "safeheron-tx", string(MovementKindPrincipal), rawCoinKey, "0xfrom", "0xto", amount, string(TransferModeSingle), 0)
}

func safeheronAliasDuplicateWindowRows() *sqlmock.Rows {
	rows := sqlmock.NewRows([]string{"id", "movement_key", "identity_algorithm_version", "fact_id", "provider_transaction_id", "movement_kind", "raw_coin_key", "source", "destination", "amount", "transfer_mode", "movement_index"})
	for index, transactionID := range []int64{101, 102} {
		rows.AddRow(transactionID, fmt.Sprintf("v1:legacy-%d", transactionID), MovementIdentityAlgorithmVersion, index+1, "safeheron-tx", string(MovementKindPrincipal), "ETHEREUM_USDT", "0xfrom", "0xto", "1", string(TransferModeSingle), 0)
	}
	return rows
}

func validSafeheronAliasRepairRequest() SafeheronAliasRepairRequest {
	sha := strings.Repeat("a", 40)
	hash := safeheronAliasCanonicalHash()
	start := time.Date(2026, 7, 15, 1, 0, 0, 0, time.UTC)
	return SafeheronAliasRepairRequest{
		Evidence: SafeheronAliasRepairEvidence{
			V2ServerSHA: sha, FrozenAccountHash: hash,
			AccountHashSamples:   []SafeheronAliasHashSample{{At: start, SHA256: hash}, {At: start.Add(5 * time.Second), SHA256: hash}, {At: start.Add(10 * time.Second), SHA256: hash}},
			DrainSamples:         []SafeheronAliasDrainSample{{At: start, AccountHash: hash}, {At: start.Add(5 * time.Second), AccountHash: hash}, {At: start.Add(10 * time.Second), AccountHash: hash}},
			AccountHashStableFor: 10 * time.Second,
		},
		AfterID: 100, Limit: 1,
		Rows: []SafeheronAliasNullRow{{TransactionID: 101, MovementKey: "v1:legacy", IdentityAlgorithmVersion: MovementIdentityAlgorithmVersion}},
		FactsByTransactionID: map[int64][]SafeheronAliasOccurrenceFact{101: {{
			TransactionID: 101,
			Occurrence: SafeheronOccurrenceInput{
				ProviderTransactionKey: "safeheron-tx", MovementKind: MovementKindPrincipal,
				RawCoinKey: "ETHEREUM_USDT", NormalizedSource: "0xfrom", NormalizedDestination: "0xto",
				Amount: decimal.NewFromInt(1), TransferMode: TransferModeSingle, MovementIndex: 0,
			},
		}}},
		ExistingOccurrenceOwners: map[string]int64{},
	}
}
