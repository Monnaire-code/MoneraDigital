package companyfund

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"
)

func TestResolveSafeheronPersistedIdentityPair(t *testing.T) {
	input := TransactionUpsertInput{
		MovementKey:                        "safeheron-v2:new",
		IdentityAlgorithmVersion:           SafeheronMovementIdentityAlgorithmVersion,
		ProviderOccurrenceKey:              "safeheron-occurrence-v1:occ",
		ProviderOccurrenceAlgorithmVersion: SafeheronOccurrenceAlgorithmVersion,
	}

	tests := []struct {
		name       string
		candidates []persistedCompanyFundTransaction
		wantID     int64
		wantErr    bool
	}{
		{name: "none"},
		{name: "same pair", candidates: []persistedCompanyFundTransaction{{ID: 1, MovementKey: input.MovementKey, IdentityAlgorithmVersion: input.IdentityAlgorithmVersion, ProviderOccurrenceKey: input.ProviderOccurrenceKey, ProviderOccurrenceAlgorithmVersion: SafeheronOccurrenceAlgorithmVersion}}, wantID: 1},
		{name: "legacy occurrence alias", candidates: []persistedCompanyFundTransaction{{ID: 2, MovementKey: "v1:legacy", IdentityAlgorithmVersion: MovementIdentityAlgorithmVersion, ProviderOccurrenceKey: input.ProviderOccurrenceKey, ProviderOccurrenceAlgorithmVersion: SafeheronOccurrenceAlgorithmVersion}}, wantID: 2},
		{name: "v2 movement wrong occurrence", candidates: []persistedCompanyFundTransaction{{ID: 3, MovementKey: input.MovementKey, IdentityAlgorithmVersion: input.IdentityAlgorithmVersion, ProviderOccurrenceKey: "safeheron-occurrence-v1:other", ProviderOccurrenceAlgorithmVersion: SafeheronOccurrenceAlgorithmVersion}}, wantErr: true},
		{name: "occurrence points to other v2", candidates: []persistedCompanyFundTransaction{{ID: 4, MovementKey: "safeheron-v2:other", IdentityAlgorithmVersion: input.IdentityAlgorithmVersion, ProviderOccurrenceKey: input.ProviderOccurrenceKey, ProviderOccurrenceAlgorithmVersion: SafeheronOccurrenceAlgorithmVersion}}, wantErr: true},
		{name: "split rows", candidates: []persistedCompanyFundTransaction{{ID: 5, MovementKey: input.MovementKey, IdentityAlgorithmVersion: input.IdentityAlgorithmVersion, ProviderOccurrenceKey: "safeheron-occurrence-v1:other"}, {ID: 6, MovementKey: "v1:legacy", IdentityAlgorithmVersion: MovementIdentityAlgorithmVersion, ProviderOccurrenceKey: input.ProviderOccurrenceKey}}, wantErr: true},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			resolved, found, err := resolveSafeheronPersistedIdentityPair(testCase.candidates, input)
			if (err != nil) != testCase.wantErr {
				t.Fatalf("resolve error = %v, wantErr=%t", err, testCase.wantErr)
			}
			if testCase.wantID == 0 {
				if found {
					t.Fatalf("unexpected candidate %#v", resolved)
				}
				return
			}
			if !found || resolved.ID != testCase.wantID {
				t.Fatalf("resolved = %#v, found=%t", resolved, found)
			}
		})
	}
}

func TestSafeheronIdentityLookupSQL_IsAuthoritativeAndNeverUsesTxHash(t *testing.T) {
	compact := strings.Join(strings.Fields(selectSafeheronCompanyFundTransactionForUpdateSQL), " ")
	if !strings.Contains(compact, "WHERE movement_key = $1 OR provider_occurrence_key = $2 ORDER BY id FOR UPDATE") {
		t.Fatalf("identity lookup is not the required single ordered OR query: %s", compact)
	}
	where := compact[strings.Index(compact, "WHERE "):]
	if strings.Contains(strings.ToLower(where), "tx_hash") {
		t.Fatalf("Safeheron authoritative identity lookup must not use TxHash: %s", where)
	}
}

func TestAlignSafeheronIncomingRecognitionSnapshot_FirstPersistedWins(t *testing.T) {
	recognized := AssetIdentity{Currency: "USDT", ChainCode: "ETHEREUM", ProviderAssetKey: "ETHEREUM_USDT", ContractAddress: "0xstored"}
	incomingAsset := AssetIdentity{Currency: "TETHER", ChainCode: "EVM", ProviderAssetKey: "ETHEREUM_USDT", ContractAddress: "0xnew"}
	incomingCurrency := incomingAsset.Currency
	unrecognized := true
	aligned, supplement, err := alignSafeheronIncomingRecognitionSnapshot(
		persistedCompanyFundTransaction{Provider: ProviderOwnedFields{Asset: &recognized}, IsUnrecognizedAsset: true},
		ProviderOwnedFields{Currency: &incomingCurrency, Asset: &incomingAsset},
		normalizedTransactionProviderSupplement{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if aligned.Currency == nil || *aligned.Currency != recognized.Currency || aligned.Asset == nil || *aligned.Asset != recognized {
		t.Fatalf("aligned recognition snapshot = %#v", aligned)
	}
	if supplement.Risk.IsUnrecognizedAsset == nil || *supplement.Risk.IsUnrecognizedAsset != unrecognized {
		t.Fatalf("aligned recognition flag = %#v", supplement.Risk.IsUnrecognizedAsset)
	}

	conflict := incomingAsset
	conflict.ProviderAssetKey = "BSC_USDT"
	if _, _, err := alignSafeheronIncomingRecognitionSnapshot(
		persistedCompanyFundTransaction{Provider: ProviderOwnedFields{Asset: &recognized}},
		ProviderOwnedFields{Asset: &conflict},
		normalizedTransactionProviderSupplement{},
	); err == nil {
		t.Fatal("different exact raw CoinKey must fail before generic merge")
	}
}

func TestSafeheronIdentityRepository_LoadsLegacyAliasAndUsesV2Insert(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	input := TransactionUpsertInput{
		MovementKey:                        "safeheron-v2:new",
		Channel:                            ChannelSafeheron,
		IdentityAlgorithmVersion:           SafeheronMovementIdentityAlgorithmVersion,
		ProviderOccurrenceKey:              "safeheron-occurrence-v1:occ",
		ProviderOccurrenceAlgorithmVersion: SafeheronOccurrenceAlgorithmVersion,
		MovementKind:                       MovementKindPrincipal,
		TransferMode:                       TransferModeSingle,
		Direction:                          DirectionOutflow,
		Currency:                           "RAW_COIN",
		Asset:                              AssetIdentity{Currency: "RAW_COIN", ProviderAssetKey: "RAW_COIN"},
		Amount:                             decimal.RequireFromString("1"),
		FirstSeenSource:                    TransactionSeenSourceWebhook,
	}
	columns := []string{
		"id", "movement_key", "channel", "identity_algorithm_version", "provider_occurrence_key", "provider_occurrence_algorithm_version",
		"provider_account_key", "provider_transaction_id", "provider_event_id", "provider_movement_id",
		"provider_transaction_fact_id", "latest_provider_event_id", "raw_snapshot_digest", "amount", "currency",
		"chain_code", "provider_asset_key", "asset_contract", "is_unrecognized_asset", "provider_status", "provider_status_version",
		"provider_fact_source", "status_rank", "last_seen_source", "tx_hash", "occurred_at", "completed_at", "provider_updated_at",
	}
	mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronCompanyFundTransactionForUpdateSQL)).
		WithArgs(input.MovementKey, input.ProviderOccurrenceKey).
		WillReturnRows(sqlmock.NewRows(columns).AddRow(
			int64(7), "v1:legacy", "SAFEHERON", MovementIdentityAlgorithmVersion, input.ProviderOccurrenceKey, SafeheronOccurrenceAlgorithmVersion,
			"", "tx", "", "", nil, nil, "", "1", "RAW_COIN", "", "RAW_COIN", "", true,
			"PENDING", nil, "WEBHOOK", 0, "WEBHOOK", "", nil, nil, nil,
		))
	loaded, found, err := loadSafeheronCompanyFundTransactionForUpdate(context.Background(), tx, input)
	if err != nil || !found || loaded.ID != 7 || loaded.MovementKey != "v1:legacy" || !loaded.IsUnrecognizedAsset {
		t.Fatalf("loaded legacy alias = %#v, found=%t, err=%v", loaded, found, err)
	}

	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO company_fund_transactions")).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(8))
	provider := ProviderOwnedFields{Currency: stringValuePointer("RAW_COIN"), Asset: &input.Asset, Amount: &input.Amount, Metadata: ProviderFactMetadata{Source: ProviderSourceWebhook}}
	id, inserted, err := insertCompanyFundTransaction(context.Background(), tx, input, provider, 0, transactionStableIdentity{}, transactionProviderProvenance{})
	if err != nil || !inserted || id != 8 {
		t.Fatalf("v2 insert = id=%d inserted=%t err=%v", id, inserted, err)
	}
	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
