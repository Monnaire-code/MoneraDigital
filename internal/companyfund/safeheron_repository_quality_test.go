package companyfund

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"
)

func TestUpsertCompanyFundTransaction_SafeheronV2ZeroRowInsert(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	input := safeheronV2QualityInput("safeheron-v2:insert", "safeheron-occurrence-v1:insert", testSafeheronPrincipalAsset(), false)
	mock.ExpectBegin()
	expectSafeheronIdentityLookup(mock, input, sqlmock.NewRows(safeheronIdentityForUpdateColumns()))
	mock.ExpectQuery(regexp.QuoteMeta(insertSafeheronCompanyFundTransactionSQL)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(501))
	mock.ExpectQuery(regexp.QuoteMeta(updateCompanyFundTransactionProviderSupplementSQL)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(501))
	mock.ExpectCommit()

	result, err := NewDBRepository(db).UpsertCompanyFundTransaction(context.Background(), input)
	if err != nil || !result.Inserted || result.ID != 501 {
		t.Fatalf("Safeheron v2 insert = %#v, %v", result, err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestUpsertCompanyFundTransaction_SafeheronConflictRequeriesSamePair(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	input := safeheronV2QualityInput("safeheron-v2:converge", "safeheron-occurrence-v1:converge", testSafeheronPrincipalAsset(), false)
	mock.ExpectBegin()
	expectSafeheronIdentityLookup(mock, input, sqlmock.NewRows(safeheronIdentityForUpdateColumns()))
	mock.ExpectQuery(regexp.QuoteMeta(insertSafeheronCompanyFundTransactionSQL)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	expectSafeheronIdentityLookup(mock, input, safeheronIdentityRows(502, input.MovementKey, input.IdentityAlgorithmVersion, input.ProviderOccurrenceKey, input.Asset, false))
	expectSafeheronRecognitionUpdate(mock, 502)
	mock.ExpectCommit()

	result, err := NewDBRepository(db).UpsertCompanyFundTransaction(context.Background(), input)
	if err != nil || result.Inserted || result.ID != 502 {
		t.Fatalf("Safeheron conflict convergence = %#v, %v", result, err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestUpsertCompanyFundTransaction_SafeheronLegacyAliasUpdateKeepsV1Identity(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	input := safeheronV2QualityInput("safeheron-v2:legacy-replay", "safeheron-occurrence-v1:legacy", testSafeheronPrincipalAsset(), false)
	mock.ExpectBegin()
	expectSafeheronIdentityLookup(mock, input, safeheronIdentityRows(503, "v1:legacy-row", MovementIdentityAlgorithmVersion, input.ProviderOccurrenceKey, input.Asset, false))
	expectSafeheronRecognitionUpdate(mock, 503)
	mock.ExpectCommit()

	result, err := NewDBRepository(db).UpsertCompanyFundTransaction(context.Background(), input)
	if err != nil || result.ID != 503 || result.Inserted {
		t.Fatalf("legacy alias replay = %#v, %v", result, err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestUpsertCompanyFundTransaction_SafeheronRecognitionSnapshotReplays(t *testing.T) {
	recognized := testSafeheronPrincipalAsset()
	fallback := AssetIdentity{Currency: "RAW_USDT", ProviderAssetKey: recognized.ProviderAssetKey}
	newerRevision := int64(2)
	for _, testCase := range []struct {
		name                  string
		persisted             AssetIdentity
		persistedUnrecognized bool
		incoming              AssetIdentity
		incomingUnrecognized  bool
		providerRevision      *int64
	}{
		{name: "fallback to catalog hit", persisted: fallback, persistedUnrecognized: true, incoming: recognized},
		{name: "catalog hit to cold miss", persisted: recognized, incoming: fallback, incomingUnrecognized: true},
		{name: "newer provider revision retains catalog snapshot", persisted: recognized, incoming: AssetIdentity{Currency: "TETHER", ChainCode: "UPDATED", ProviderAssetKey: recognized.ProviderAssetKey, ContractAddress: "0xchanged"}, providerRevision: &newerRevision},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db, mock := newCompanyFundMockDB(t)
			defer db.Close()
			input := safeheronV2QualityInput("safeheron-v2:"+testCase.name, "safeheron-occurrence-v1:"+testCase.name, testCase.incoming, testCase.incomingUnrecognized)
			input.Provider.Metadata.Revision = testCase.providerRevision
			mock.ExpectBegin()
			expectSafeheronIdentityLookup(mock, input, safeheronIdentityRows(510, input.MovementKey, input.IdentityAlgorithmVersion, input.ProviderOccurrenceKey, testCase.persisted, testCase.persistedUnrecognized))
			expectSafeheronRecognitionUpdate(mock, 510)
			mock.ExpectCommit()

			result, err := NewDBRepository(db).UpsertCompanyFundTransaction(context.Background(), input)
			if err != nil || result.ID != 510 || result.Inserted || result.Quarantined {
				t.Fatalf("recognition replay = %#v, %v", result, err)
			}
			assertCompanyFundMockExpectations(t, mock)
		})
	}
}

func TestUpsertCompanyFundTransaction_SafeheronIdentityFailuresRollback(t *testing.T) {
	baseAsset := testSafeheronPrincipalAsset()
	for _, testCase := range []struct {
		name string
		rows *sqlmock.Rows
	}{
		{
			name: "raw CoinKey mismatch",
			rows: safeheronIdentityRows(520, "safeheron-v2:raw-mismatch", SafeheronMovementIdentityAlgorithmVersion, "safeheron-occurrence-v1:raw-mismatch", AssetIdentity{Currency: "USDC", ProviderAssetKey: "ETHEREUM_USDC"}, false),
		},
		{
			name: "split rows invariant",
			rows: safeheronIdentityRows(521, "safeheron-v2:split", SafeheronMovementIdentityAlgorithmVersion, "safeheron-occurrence-v1:other", baseAsset, false).
				AddRow(safeheronIdentityRowValues(522, "v1:legacy", MovementIdentityAlgorithmVersion, "safeheron-occurrence-v1:split", baseAsset, false)...),
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db, mock := newCompanyFundMockDB(t)
			defer db.Close()
			input := safeheronV2QualityInput("safeheron-v2:"+map[bool]string{true: "raw-mismatch", false: "split"}[testCase.name == "raw CoinKey mismatch"], "safeheron-occurrence-v1:"+map[bool]string{true: "raw-mismatch", false: "split"}[testCase.name == "raw CoinKey mismatch"], baseAsset, false)
			mock.ExpectBegin()
			expectSafeheronIdentityLookup(mock, input, testCase.rows)
			mock.ExpectRollback()
			result, err := NewDBRepository(db).UpsertCompanyFundTransaction(context.Background(), input)
			if err == nil || result.Inserted {
				t.Fatalf("identity failure = %#v, %v", result, err)
			}
			assertCompanyFundMockExpectations(t, mock)
		})
	}
}

func TestSafeheronRepositoryHelpers_ErrorBranches(t *testing.T) {
	input := safeheronV2QualityInput("safeheron-v2:errors", "safeheron-occurrence-v1:errors", testSafeheronPrincipalAsset(), false)
	unsupported := persistedCompanyFundTransaction{ID: 1, MovementKey: "v1:legacy", IdentityAlgorithmVersion: "v0", ProviderOccurrenceKey: input.ProviderOccurrenceKey, ProviderOccurrenceAlgorithmVersion: SafeheronOccurrenceAlgorithmVersion}
	if _, _, err := resolveSafeheronPersistedIdentityPair([]persistedCompanyFundTransaction{unsupported}, input); err == nil {
		t.Fatal("unsupported legacy algorithm must fail")
	}
	legacyCollision := unsupported
	legacyCollision.IdentityAlgorithmVersion = MovementIdentityAlgorithmVersion
	legacyCollision.MovementKey = input.MovementKey
	if _, _, err := resolveSafeheronPersistedIdentityPair([]persistedCompanyFundTransaction{legacyCollision}, input); err == nil {
		t.Fatal("v2 key stored as legacy must fail")
	}
	aliasNull := persistedCompanyFundTransaction{
		ID:                       2,
		MovementKey:              "v1:alias-null",
		IdentityAlgorithmVersion: MovementIdentityAlgorithmVersion,
	}
	if _, _, err := resolveSafeheronPersistedIdentityPair([]persistedCompanyFundTransaction{aliasNull}, input); err == nil {
		t.Fatal("runtime replay must not repair a missing occurrence alias")
	}
	if _, _, err := alignSafeheronIncomingRecognitionSnapshot(persistedCompanyFundTransaction{}, ProviderOwnedFields{}, normalizedTransactionProviderSupplement{}); err == nil {
		t.Fatal("missing recognition identities must fail")
	}
}

func safeheronV2QualityInput(movementKey, occurrenceKey string, asset AssetIdentity, unrecognized bool) TransactionUpsertInput {
	fromID := int64(101)
	riskFlag := unrecognized
	return TransactionUpsertInput{
		MovementKey:                        movementKey,
		Channel:                            ChannelSafeheron,
		IdentityAlgorithmVersion:           SafeheronMovementIdentityAlgorithmVersion,
		ProviderOccurrenceKey:              occurrenceKey,
		ProviderOccurrenceAlgorithmVersion: SafeheronOccurrenceAlgorithmVersion,
		ProviderTransactionID:              "safeheron-tx",
		MovementKind:                       MovementKindPrincipal,
		TransferMode:                       TransferModeSingle,
		Direction:                          DirectionOutflow,
		FromCompanyFundAccountID:           &fromID,
		Currency:                           asset.Currency,
		Asset:                              asset,
		Amount:                             decimal.NewFromInt(1),
		FirstSeenSource:                    TransactionSeenSourceWebhook,
		Provider: ProviderOwnedFields{
			Metadata: ProviderFactMetadata{Source: ProviderSourceWebhook},
		},
		AutomaticRisk: ProviderAutomaticRiskInput{IsUnrecognizedAsset: &riskFlag},
	}
}

func safeheronIdentityForUpdateColumns() []string {
	return []string{
		"id", "movement_key", "channel", "identity_algorithm_version", "provider_occurrence_key", "provider_occurrence_algorithm_version",
		"provider_account_key", "provider_transaction_id", "provider_event_id", "provider_movement_id", "provider_transaction_fact_id", "latest_provider_event_id", "raw_snapshot_digest",
		"amount", "currency", "chain_code", "provider_asset_key", "asset_contract", "is_unrecognized_asset", "provider_status", "provider_status_version", "provider_fact_source", "status_rank", "last_seen_source", "tx_hash", "occurred_at", "completed_at", "provider_updated_at",
	}
}

func safeheronIdentityRowValues(id int64, movementKey, algorithm, occurrenceKey string, asset AssetIdentity, unrecognized bool) []driver.Value {
	return []driver.Value{
		id, movementKey, "SAFEHERON", algorithm, occurrenceKey, SafeheronOccurrenceAlgorithmVersion,
		"", "safeheron-tx", "", "", nil, nil, "", "1", asset.Currency, asset.ChainCode, asset.ProviderAssetKey, asset.ContractAddress, unrecognized,
		"", nil, "WEBHOOK", 0, "WEBHOOK", "", nil, nil, nil,
	}
}

func safeheronIdentityRows(id int64, movementKey, algorithm, occurrenceKey string, asset AssetIdentity, unrecognized bool) *sqlmock.Rows {
	return sqlmock.NewRows(safeheronIdentityForUpdateColumns()).AddRow(safeheronIdentityRowValues(id, movementKey, algorithm, occurrenceKey, asset, unrecognized)...)
}

func expectSafeheronIdentityLookup(mock sqlmock.Sqlmock, input TransactionUpsertInput, rows *sqlmock.Rows) {
	mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronCompanyFundTransactionForUpdateSQL)).
		WithArgs(input.MovementKey, input.ProviderOccurrenceKey).
		WillReturnRows(rows)
}

func expectSafeheronRecognitionUpdate(mock sqlmock.Sqlmock, id int64) {
	mock.ExpectQuery(regexp.QuoteMeta(updateCompanyFundTransactionSQL)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(id))
	mock.ExpectQuery(regexp.QuoteMeta(updateCompanyFundTransactionProviderSupplementSQL)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(id))
}

func TestLoadSafeheronCompanyFundTransactionForUpdate_QueryAndScanFailures(t *testing.T) {
	input := safeheronV2QualityInput("safeheron-v2:io", "safeheron-occurrence-v1:io", testSafeheronPrincipalAsset(), false)
	for _, testCase := range []struct {
		name      string
		configure func(sqlmock.Sqlmock)
	}{
		{name: "query", configure: func(mock sqlmock.Sqlmock) {
			mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronCompanyFundTransactionForUpdateSQL)).WithArgs(input.MovementKey, input.ProviderOccurrenceKey).WillReturnError(errors.New("query failed"))
		}},
		{name: "scan", configure: func(mock sqlmock.Sqlmock) {
			values := safeheronIdentityRowValues(1, input.MovementKey, input.IdentityAlgorithmVersion, input.ProviderOccurrenceKey, input.Asset, false)
			values[0] = "invalid-id"
			mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronCompanyFundTransactionForUpdateSQL)).WithArgs(input.MovementKey, input.ProviderOccurrenceKey).
				WillReturnRows(sqlmock.NewRows(safeheronIdentityForUpdateColumns()).AddRow(values...))
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db, mock := newCompanyFundMockDB(t)
			defer db.Close()
			mock.ExpectBegin()
			tx, err := db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			testCase.configure(mock)
			if _, _, err := loadSafeheronCompanyFundTransactionForUpdate(context.Background(), tx, input); err == nil {
				t.Fatal("load error branch unexpectedly succeeded")
			}
			mock.ExpectRollback()
			_ = tx.Rollback()
			assertCompanyFundMockExpectations(t, mock)
		})
	}
}

func TestLoadSafeheronCompanyFundTransactionForUpdate_ScannerAndIterationContracts(t *testing.T) {
	input := safeheronV2QualityInput("safeheron-v2:scanner", "safeheron-occurrence-v1:scanner", testSafeheronPrincipalAsset(), false)
	now := time.Date(2026, 7, 15, 1, 2, 3, 0, time.UTC)
	fullValues := safeheronIdentityRowValues(601, input.MovementKey, input.IdentityAlgorithmVersion, input.ProviderOccurrenceKey, input.Asset, false)
	fullValues[10] = int64(71)
	fullValues[11] = int64(72)
	fullValues[12] = "digest"
	fullValues[19] = "CONFIRMED"
	fullValues[20] = int64(4)
	fullValues[21] = "RECONCILIATION"
	fullValues[24] = "0xtx"
	fullValues[25] = now
	fullValues[26] = now.Add(time.Minute)
	fullValues[27] = now.Add(2 * time.Minute)
	assertSafeheronLoaderResult(t, input, sqlmock.NewRows(safeheronIdentityForUpdateColumns()).AddRow(fullValues...), func(persisted persistedCompanyFundTransaction, found bool, err error) {
		if err != nil || !found || persisted.ID != 601 || persisted.Provider.Amount == nil || persisted.Provider.Status == nil || persisted.Provider.Metadata.Revision == nil || persisted.Provider.Metadata.UpdatedAt == nil || persisted.Provider.TxHash == nil || persisted.Provider.OccurredAt == nil || persisted.Provider.CompletedAt == nil || persisted.Provenance.ProviderTransactionFactID == nil || persisted.Provenance.LatestProviderEventID == nil {
			t.Fatalf("full scanner result = %#v, %v, %v", persisted, found, err)
		}
	})

	fallbackValues := safeheronIdentityRowValues(602, input.MovementKey, input.IdentityAlgorithmVersion, input.ProviderOccurrenceKey, input.Asset, false)
	fallbackValues[21] = ""
	assertSafeheronLoaderResult(t, input, sqlmock.NewRows(safeheronIdentityForUpdateColumns()).AddRow(fallbackValues...), func(persisted persistedCompanyFundTransaction, found bool, err error) {
		if err != nil || !found || persisted.Provider.Metadata.Source != ProviderSourceWebhook {
			t.Fatalf("fallback scanner result = %#v, %v, %v", persisted, found, err)
		}
	})

	invalidAmount := safeheronIdentityRowValues(603, input.MovementKey, input.IdentityAlgorithmVersion, input.ProviderOccurrenceKey, input.Asset, false)
	invalidAmount[13] = "not-a-decimal"
	assertSafeheronLoaderResult(t, input, sqlmock.NewRows(safeheronIdentityForUpdateColumns()).AddRow(invalidAmount...), func(_ persistedCompanyFundTransaction, _ bool, err error) {
		if err == nil {
			t.Fatal("invalid persisted amount must fail closed")
		}
	})

	threeRows := sqlmock.NewRows(safeheronIdentityForUpdateColumns())
	for id := int64(604); id <= 606; id++ {
		threeRows.AddRow(safeheronIdentityRowValues(id, input.MovementKey, input.IdentityAlgorithmVersion, input.ProviderOccurrenceKey, input.Asset, false)...)
	}
	assertSafeheronLoaderResult(t, input, threeRows, func(_ persistedCompanyFundTransaction, _ bool, err error) {
		if err == nil {
			t.Fatal("more than two locked identities must fail closed")
		}
	})

	iterationError := sqlmock.NewRows(safeheronIdentityForUpdateColumns()).
		AddRow(safeheronIdentityRowValues(607, input.MovementKey, input.IdentityAlgorithmVersion, input.ProviderOccurrenceKey, input.Asset, false)...).
		RowError(0, errors.New("row failed"))
	assertSafeheronLoaderResult(t, input, iterationError, func(_ persistedCompanyFundTransaction, _ bool, err error) {
		if err == nil {
			t.Fatal("row iteration error must fail closed")
		}
	})
}

func assertSafeheronLoaderResult(
	t *testing.T,
	input TransactionUpsertInput,
	rows *sqlmock.Rows,
	assert func(persistedCompanyFundTransaction, bool, error),
) {
	t.Helper()
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	mock.ExpectBegin()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	expectSafeheronIdentityLookup(mock, input, rows)
	persisted, found, loadErr := loadSafeheronCompanyFundTransactionForUpdate(context.Background(), tx, input)
	assert(persisted, found, loadErr)
	mock.ExpectRollback()
	_ = tx.Rollback()
	assertCompanyFundMockExpectations(t, mock)
}
