package companyfund

import (
	"context"
	"database/sql/driver"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"
)

func TestMergeProviderFields_OccurredAtFollowsProviderMetadata(t *testing.T) {
	oldRevision := int64(2)
	newerRevision := int64(3)
	lowerRevision := int64(1)
	existingOccurredAt := time.Date(2026, time.July, 10, 8, 0, 0, 0, time.UTC)
	newerOccurredAt := existingOccurredAt.Add(time.Hour)
	lowerOccurredAt := existingOccurredAt.Add(-time.Hour)
	existing := ProviderOwnedFields{
		Metadata:   ProviderFactMetadata{Revision: &oldRevision, Source: ProviderSourceWebhook},
		OccurredAt: &existingOccurredAt,
	}

	retained, decision := MergeProviderFields(existing, ProviderOwnedFields{
		Metadata:   ProviderFactMetadata{Revision: &lowerRevision, Source: ProviderSourceWebhook},
		OccurredAt: &lowerOccurredAt,
	})
	if decision.Outcome == MergeOutcomeQuarantine || retained.OccurredAt == nil || !retained.OccurredAt.Equal(existingOccurredAt) {
		t.Fatalf("lower-revision provider fact must not regress occurred_at: %#v %#v", retained, decision)
	}

	corrected, decision := MergeProviderFields(existing, ProviderOwnedFields{
		Metadata:   ProviderFactMetadata{Revision: &newerRevision, Source: ProviderSourceWebhook},
		OccurredAt: &newerOccurredAt,
	})
	if decision.Outcome != MergeOutcomeApplied || corrected.OccurredAt == nil || !corrected.OccurredAt.Equal(newerOccurredAt) {
		t.Fatalf("higher-revision provider fact must correct occurred_at: %#v %#v", corrected, decision)
	}
}

func TestMergeProviderFields_PersistsHigherSourcePriorityWithoutFieldMutation(t *testing.T) {
	revision := int64(2)
	existing := ProviderOwnedFields{Metadata: ProviderFactMetadata{Revision: &revision, Source: ProviderSourceProductDetail}}
	incoming := ProviderOwnedFields{Metadata: ProviderFactMetadata{Revision: &revision, Source: ProviderSourceReconciliation}}

	merged, decision := MergeProviderFields(existing, incoming)
	if decision.Outcome != MergeOutcomeApplied || merged.Metadata.Source != ProviderSourceReconciliation {
		t.Fatalf("same-revision reconciliation source must supersede product detail: %#v %#v", merged, decision)
	}
}

func TestMergeProviderFields_EqualRevisionMissingTimestampDefersToSourcePriority(t *testing.T) {
	revision := int64(2)
	providerTime := time.Date(2026, time.July, 10, 8, 0, 0, 0, time.UTC)

	merged, decision := MergeProviderFields(
		ProviderOwnedFields{Metadata: ProviderFactMetadata{Revision: &revision, UpdatedAt: &providerTime, Source: ProviderSourceProductDetail}},
		ProviderOwnedFields{Metadata: ProviderFactMetadata{Revision: &revision, Source: ProviderSourceReconciliation}},
	)
	if decision.Outcome != MergeOutcomeApplied || merged.Metadata.Source != ProviderSourceReconciliation {
		t.Fatalf("same revision reconciliation must outrank detail despite missing incoming timestamp: %#v %#v", merged, decision)
	}

	retained, decision := MergeProviderFields(
		ProviderOwnedFields{Metadata: ProviderFactMetadata{Revision: &revision, Source: ProviderSourceReconciliation}},
		ProviderOwnedFields{Metadata: ProviderFactMetadata{Revision: &revision, UpdatedAt: &providerTime, Source: ProviderSourceProductDetail}},
	)
	if decision.Outcome != MergeOutcomeUnchanged || retained.Metadata.Source != ProviderSourceReconciliation {
		t.Fatalf("same revision detail must not outrank reconciliation because it has a timestamp: %#v %#v", retained, decision)
	}
}

func TestUpsertCompanyFundTransaction_OccurredAtUsesMergedProviderFact(t *testing.T) {
	oldRevision := int64(2)
	lowerRevision := int64(1)
	higherRevision := int64(3)
	existingOccurredAt := time.Date(2026, time.July, 10, 8, 0, 0, 0, time.UTC)

	for _, testCase := range []struct {
		name             string
		incomingRevision *int64
		incomingOccurred time.Time
		wantRevision     int64
		wantOccurred     time.Time
	}{
		{
			name:             "lower revision retains stored occurrence",
			incomingRevision: &lowerRevision,
			incomingOccurred: existingOccurredAt.Add(time.Hour),
			wantRevision:     oldRevision,
			wantOccurred:     existingOccurredAt,
		},
		{
			name:             "higher revision corrects occurrence",
			incomingRevision: &higherRevision,
			incomingOccurred: existingOccurredAt.Add(time.Hour),
			wantRevision:     higherRevision,
			wantOccurred:     existingOccurredAt.Add(time.Hour),
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db, mock := newCompanyFundMockDB(t)
			defer db.Close()
			repository := NewDBRepository(db)
			fromAccountID := int64(101)
			status := LifecycleStatusPending

			mock.ExpectBegin()
			mock.ExpectQuery(transactionForUpdateQueryPattern()).
				WithArgs("v1:occurred-at-" + testCase.name).
				WillReturnRows(lockedTransactionRows(
					201, "SAFEHERON", MovementIdentityAlgorithmVersion, "", "", "", "", nil, nil, "",
					"10", "USDT", "", "", "", "PENDING", oldRevision, "WEBHOOK", 0, "WEBHOOK", "", existingOccurredAt, nil, nil,
				))
			mock.ExpectQuery(regexp.QuoteMeta("UPDATE company_fund_transactions")).
				WithArgs(
					int64(201), nil, nil, nil, nil, nil,
					"10", "USDT", false, nil, nil, nil, nil,
					LifecycleStatusPending, testCase.wantRevision, nil, ProviderSourceWebhook, 0,
					testCase.wantOccurred, nil, nil, nil, TransactionSeenSourceWebhook,
				).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(201))
			mock.ExpectCommit()

			result, err := repository.UpsertCompanyFundTransaction(context.Background(), TransactionUpsertInput{
				MovementKey:              "v1:occurred-at-" + testCase.name,
				Channel:                  ChannelSafeheron,
				IdentityAlgorithmVersion: MovementIdentityAlgorithmVersion,
				MovementKind:             MovementKindPrincipal,
				TransferMode:             TransferModeSingle,
				Direction:                DirectionOutflow,
				FromCompanyFundAccountID: &fromAccountID,
				Currency:                 "USDT",
				Amount:                   decimal.RequireFromString("10"),
				OccurredAt:               &testCase.incomingOccurred,
				FirstSeenSource:          TransactionSeenSourceWebhook,
				Provider: ProviderOwnedFields{
					Metadata: ProviderFactMetadata{Revision: testCase.incomingRevision, Source: ProviderSourceWebhook},
					Status:   &status,
				},
			})
			if err != nil || result.ID != 201 || result.Quarantined {
				t.Fatalf("UpsertCompanyFundTransaction = %#v, %v", result, err)
			}
			assertCompanyFundMockExpectations(t, mock)
		})
	}
}

func TestUpsertCompanyFundTransaction_PersistsFullProviderSourcePriority(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	repository := NewDBRepository(db)
	fromAccountID := int64(101)
	revision := int64(2)
	status := LifecycleStatusPending

	mock.ExpectBegin()
	mock.ExpectQuery(transactionForUpdateQueryPattern()).
		WithArgs("v1:source-priority").
		WillReturnRows(lockedTransactionRows(
			202, "SAFEHERON", MovementIdentityAlgorithmVersion, "", "", "", "", nil, nil, "",
			"10", "USDT", "", "", "", "PENDING", revision, "PRODUCT_DETAIL", 0, "RECONCILIATION", "", nil, nil, nil,
		))
	mock.ExpectQuery(regexp.QuoteMeta("UPDATE company_fund_transactions")).
		WithArgs(
			int64(202), nil, nil, nil, nil, nil,
			"10", "USDT", false, nil, nil, nil, nil,
			LifecycleStatusPending, revision, nil, ProviderSourceReconciliation, 0,
			nil, nil, nil, nil, TransactionSeenSourceReconciliation,
		).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(202))
	mock.ExpectCommit()

	result, err := repository.UpsertCompanyFundTransaction(context.Background(), TransactionUpsertInput{
		MovementKey:              "v1:source-priority",
		Channel:                  ChannelSafeheron,
		IdentityAlgorithmVersion: MovementIdentityAlgorithmVersion,
		MovementKind:             MovementKindPrincipal,
		TransferMode:             TransferModeSingle,
		Direction:                DirectionOutflow,
		FromCompanyFundAccountID: &fromAccountID,
		Currency:                 "USDT",
		Amount:                   decimal.RequireFromString("10"),
		FirstSeenSource:          TransactionSeenSourceReconciliation,
		Provider: ProviderOwnedFields{
			Metadata: ProviderFactMetadata{Revision: &revision, Source: ProviderSourceReconciliation},
			Status:   &status,
		},
	})
	if err != nil || result.ID != 202 || result.Quarantined {
		t.Fatalf("UpsertCompanyFundTransaction = %#v, %v", result, err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestUpsertCompanyFundTransaction_QuarantinesStoredChannelOrAlgorithmMismatch(t *testing.T) {
	fromAccountID := int64(101)
	status := LifecycleStatusPending
	for _, testCase := range []struct {
		name              string
		storedChannel     string
		storedAlgorithm   string
		incomingChannel   Channel
		incomingAlgorithm string
	}{
		{"cross channel", "AIRWALLEX", MovementIdentityAlgorithmVersion, ChannelSafeheron, MovementIdentityAlgorithmVersion},
		{"algorithm mismatch", "SAFEHERON", "v0:legacy", ChannelSafeheron, MovementIdentityAlgorithmVersion},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db, mock := newCompanyFundMockDB(t)
			defer db.Close()
			repository := NewDBRepository(db)
			mock.ExpectBegin()
			mock.ExpectQuery(transactionForUpdateQueryPattern()).
				WithArgs("v1:mismatch-" + testCase.name).
				WillReturnRows(lockedTransactionRows(
					203, testCase.storedChannel, testCase.storedAlgorithm, "", "", "", "", nil, nil, "",
					"10", "USDT", "", "", "", "PENDING", nil, "WEBHOOK", 0, "WEBHOOK", "", nil, nil, nil,
				))
			mock.ExpectRollback()

			result, err := repository.UpsertCompanyFundTransaction(context.Background(), TransactionUpsertInput{
				MovementKey:              "v1:mismatch-" + testCase.name,
				Channel:                  testCase.incomingChannel,
				IdentityAlgorithmVersion: testCase.incomingAlgorithm,
				MovementKind:             MovementKindPrincipal,
				TransferMode:             TransferModeSingle,
				Direction:                DirectionOutflow,
				FromCompanyFundAccountID: &fromAccountID,
				Currency:                 "USDT",
				Amount:                   decimal.RequireFromString("10"),
				FirstSeenSource:          TransactionSeenSourceWebhook,
				Provider:                 ProviderOwnedFields{Status: &status},
			})
			if err == nil || !result.Quarantined || result.ID != 203 {
				t.Fatalf("mismatch result = %#v, %v", result, err)
			}
			if _, ok := err.(*TransactionQuarantineError); !ok {
				t.Fatalf("mismatch error type = %T, want TransactionQuarantineError", err)
			}
			assertCompanyFundMockExpectations(t, mock)
		})
	}
}

func TestUpsertCompanyFundTransaction_StableIdentityConflictQuarantinesWithoutWrite(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	repository := NewDBRepository(db)
	fromAccountID := int64(101)
	status := LifecycleStatusPending

	mock.ExpectBegin()
	mock.ExpectQuery(transactionForUpdateQueryPattern()).
		WithArgs("v1:stable-conflict").
		WillReturnRows(lockedTransactionRows(
			204, "SAFEHERON", MovementIdentityAlgorithmVersion, "account-a", "transaction-a", "", "movement-a", nil, nil, "",
			"10", "USDT", "", "", "", "PENDING", nil, "WEBHOOK", 0, "WEBHOOK", "", nil, nil, nil,
		))
	mock.ExpectRollback()

	result, err := repository.UpsertCompanyFundTransaction(context.Background(), TransactionUpsertInput{
		MovementKey:              "v1:stable-conflict",
		Channel:                  ChannelSafeheron,
		IdentityAlgorithmVersion: MovementIdentityAlgorithmVersion,
		ProviderAccountKey:       "account-b",
		ProviderTransactionID:    "transaction-a",
		ProviderMovementID:       "movement-a",
		MovementKind:             MovementKindPrincipal,
		TransferMode:             TransferModeSingle,
		Direction:                DirectionOutflow,
		FromCompanyFundAccountID: &fromAccountID,
		Currency:                 "USDT",
		Amount:                   decimal.RequireFromString("10"),
		FirstSeenSource:          TransactionSeenSourceWebhook,
		Provider:                 ProviderOwnedFields{Status: &status},
	})
	if err == nil || !result.Quarantined || result.ID != 204 {
		t.Fatalf("stable conflict result = %#v, %v", result, err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestResolveStableTransactionIdentity_FillsOnceAndRejectsEachConflict(t *testing.T) {
	incoming := transactionStableIdentity{
		ProviderAccountKey:    "account-a",
		ProviderTransactionID: "transaction-a",
		ProviderMovementID:    "movement-a",
	}
	filled, err := resolveStableTransactionIdentity(transactionStableIdentity{}, incoming)
	if err != nil || filled != incoming {
		t.Fatalf("first stable identity fill = %#v, %v", filled, err)
	}
	retained, err := resolveStableTransactionIdentity(incoming, incoming)
	if err != nil || retained != incoming {
		t.Fatalf("same stable identity = %#v, %v", retained, err)
	}
	for _, conflicting := range []transactionStableIdentity{
		{ProviderAccountKey: "account-b", ProviderTransactionID: "transaction-a", ProviderMovementID: "movement-a"},
		{ProviderAccountKey: "account-a", ProviderTransactionID: "transaction-b", ProviderMovementID: "movement-a"},
		{ProviderAccountKey: "account-a", ProviderTransactionID: "transaction-a", ProviderMovementID: "movement-b"},
	} {
		if _, err := resolveStableTransactionIdentity(incoming, conflicting); err == nil {
			t.Fatalf("conflicting stable identity %#v must be rejected", conflicting)
		}
	}
}

func TestUpsertCompanyFundTransaction_ProvenancePairUsesMetadataWinner(t *testing.T) {
	higherRevision := int64(2)
	lowerRevision := int64(1)
	newerRevision := int64(3)
	existingEventID := int64(301)
	incomingEventID := int64(302)
	existingFactID := int64(401)
	incomingFactID := int64(402)
	existingDigest := strings.Repeat("a", 64)
	incomingDigest := strings.Repeat("b", 64)
	fromAccountID := int64(101)
	status := LifecycleStatusPending

	for _, testCase := range []struct {
		name              string
		incomingRevision  *int64
		wantRevision      int64
		wantEventID       int64
		wantFactID        int64
		wantDigest        string
		wantProviderEvent string
	}{
		{"lower metadata retains complete pair", &lowerRevision, higherRevision, existingEventID, existingFactID, existingDigest, "delivery-existing"},
		{"higher metadata advances provenance", &newerRevision, newerRevision, incomingEventID, incomingFactID, incomingDigest, "delivery-incoming"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db, mock := newCompanyFundMockDB(t)
			defer db.Close()
			repository := NewDBRepository(db)
			mock.ExpectBegin()
			mock.ExpectQuery(regexp.QuoteMeta("SELECT channel, source_payload_digest FROM company_fund_provider_events WHERE id = $1")).
				WithArgs(incomingEventID).
				WillReturnRows(sqlmock.NewRows([]string{"channel", "source_payload_digest"}).AddRow("SAFEHERON", incomingDigest))
			mock.ExpectQuery(transactionForUpdateQueryPattern()).
				WithArgs("v1:provenance-" + testCase.name).
				WillReturnRows(lockedTransactionRows(
					205, "SAFEHERON", MovementIdentityAlgorithmVersion, "account-a", "transaction-a", "delivery-existing", "movement-a", existingFactID, existingEventID, existingDigest,
					"10", "USDT", "", "", "", "PENDING", higherRevision, "WEBHOOK", 0, "WEBHOOK", "", nil, nil, nil,
				))
			mock.ExpectQuery(regexp.QuoteMeta("SELECT channel, provider_account_key, provider_transaction_id, allocation_state, derivation_contract_version FROM company_fund_provider_transaction_facts WHERE id = $1")).
				WithArgs(testCase.wantFactID).
				WillReturnRows(sqlmock.NewRows([]string{"channel", "provider_account_key", "provider_transaction_id", "allocation_state", "derivation_contract_version"}).AddRow("SAFEHERON", "account-a", "transaction-a", "UNPROVEN", nil))
			mock.ExpectQuery(regexp.QuoteMeta("UPDATE company_fund_transactions")).
				WithArgs(
					int64(205), "account-a", "transaction-a", testCase.wantProviderEvent, "movement-a", testCase.wantFactID,
					"10", "USDT", false, nil, nil, nil, nil,
					LifecycleStatusPending, testCase.wantRevision, nil, ProviderSourceWebhook, 0,
					nil, nil, testCase.wantEventID, testCase.wantDigest, TransactionSeenSourceWebhook,
				).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(205))
			mock.ExpectCommit()

			result, err := repository.UpsertCompanyFundTransaction(context.Background(), TransactionUpsertInput{
				MovementKey:               "v1:provenance-" + testCase.name,
				Channel:                   ChannelSafeheron,
				IdentityAlgorithmVersion:  MovementIdentityAlgorithmVersion,
				ProviderAccountKey:        "account-a",
				ProviderTransactionID:     "transaction-a",
				ProviderEventID:           "delivery-incoming",
				ProviderMovementID:        "movement-a",
				ProviderTransactionFactID: &incomingFactID,
				MovementKind:              MovementKindPrincipal,
				TransferMode:              TransferModeSingle,
				Direction:                 DirectionOutflow,
				FromCompanyFundAccountID:  &fromAccountID,
				Currency:                  "USDT",
				Amount:                    decimal.RequireFromString("10"),
				LatestProviderEventID:     &incomingEventID,
				RawSnapshotDigest:         incomingDigest,
				FirstSeenSource:           TransactionSeenSourceWebhook,
				Provider: ProviderOwnedFields{
					Metadata: ProviderFactMetadata{Revision: testCase.incomingRevision, Source: ProviderSourceWebhook},
					Status:   &status,
				},
			})
			if err != nil || result.ID != 205 || result.Quarantined {
				t.Fatalf("provenance result = %#v, %v", result, err)
			}
			assertCompanyFundMockExpectations(t, mock)
		})
	}
}

func TestUpsertCompanyFundTransaction_ValidatesProviderFactOwnershipBeforeWrite(t *testing.T) {
	factID := int64(501)
	fromAccountID := int64(101)
	status := LifecycleStatusPending
	newInput := func(movementKey, accountKey, transactionID string) TransactionUpsertInput {
		return TransactionUpsertInput{
			MovementKey:               movementKey,
			Channel:                   ChannelSafeheron,
			IdentityAlgorithmVersion:  MovementIdentityAlgorithmVersion,
			ProviderAccountKey:        accountKey,
			ProviderTransactionID:     transactionID,
			ProviderTransactionFactID: &factID,
			MovementKind:              MovementKindPrincipal,
			TransferMode:              TransferModeSingle,
			Direction:                 DirectionOutflow,
			FromCompanyFundAccountID:  &fromAccountID,
			Currency:                  "USDT",
			Amount:                    decimal.RequireFromString("10"),
			FirstSeenSource:           TransactionSeenSourceWebhook,
			Provider:                  ProviderOwnedFields{Status: &status},
		}
	}

	t.Run("first insert rejects cross-channel fact", func(t *testing.T) {
		db, mock := newCompanyFundMockDB(t)
		defer db.Close()
		repository := NewDBRepository(db)
		mock.ExpectBegin()
		mock.ExpectQuery(transactionForUpdateQueryPattern()).
			WithArgs("v1:fact-cross-channel").
			WillReturnRows(sqlmock.NewRows(transactionForUpdateColumns()))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT channel, provider_account_key, provider_transaction_id, allocation_state, derivation_contract_version FROM company_fund_provider_transaction_facts WHERE id = $1")).
			WithArgs(factID).
			WillReturnRows(sqlmock.NewRows([]string{"channel", "provider_account_key", "provider_transaction_id", "allocation_state", "derivation_contract_version"}).AddRow("AIRWALLEX", "account-a", "transaction-a", "UNPROVEN", nil))
		mock.ExpectRollback()

		result, err := repository.UpsertCompanyFundTransaction(context.Background(), newInput("v1:fact-cross-channel", "account-a", "transaction-a"))
		if err == nil || !result.Quarantined {
			t.Fatalf("cross-channel fact result = %#v, %v", result, err)
		}
		assertCompanyFundMockExpectations(t, mock)
	})

	t.Run("existing transaction rejects cross-account fact", func(t *testing.T) {
		db, mock := newCompanyFundMockDB(t)
		defer db.Close()
		repository := NewDBRepository(db)
		mock.ExpectBegin()
		mock.ExpectQuery(transactionForUpdateQueryPattern()).
			WithArgs("v1:fact-cross-account").
			WillReturnRows(lockedTransactionRows(
				207, "SAFEHERON", MovementIdentityAlgorithmVersion, "account-a", "", "", "", nil, nil, "",
				"10", "USDT", "", "", "", "PENDING", nil, "WEBHOOK", 0, "WEBHOOK", "", nil, nil, nil,
			))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT channel, provider_account_key, provider_transaction_id, allocation_state, derivation_contract_version FROM company_fund_provider_transaction_facts WHERE id = $1")).
			WithArgs(factID).
			WillReturnRows(sqlmock.NewRows([]string{"channel", "provider_account_key", "provider_transaction_id", "allocation_state", "derivation_contract_version"}).AddRow("SAFEHERON", "account-b", "transaction-a", "UNPROVEN", nil))
		mock.ExpectRollback()

		result, err := repository.UpsertCompanyFundTransaction(context.Background(), newInput("v1:fact-cross-account", "account-a", "transaction-a"))
		if err == nil || !result.Quarantined || result.ID != 207 {
			t.Fatalf("cross-account fact result = %#v, %v", result, err)
		}
		assertCompanyFundMockExpectations(t, mock)
	})

	t.Run("first insert rejects proven cross-account fact", func(t *testing.T) {
		db, mock := newCompanyFundMockDB(t)
		defer db.Close()
		repository := NewDBRepository(db)
		mock.ExpectBegin()
		mock.ExpectQuery(transactionForUpdateQueryPattern()).
			WithArgs("v1:fact-proven-cross-account").
			WillReturnRows(sqlmock.NewRows(transactionForUpdateColumns()))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT channel, provider_account_key, provider_transaction_id, allocation_state, derivation_contract_version FROM company_fund_provider_transaction_facts WHERE id = $1")).
			WithArgs(factID).
			WillReturnRows(providerFactOwnershipRows("SAFEHERON", "account-b", "transaction-a", "PROVEN_DERIVABLE", "sandbox-v1"))
		mock.ExpectRollback()

		result, err := repository.UpsertCompanyFundTransaction(context.Background(), newInput("v1:fact-proven-cross-account", "account-a", "transaction-a"))
		if err == nil || !result.Quarantined {
			t.Fatalf("proven cross-account fact result = %#v, %v", result, err)
		}
		assertCompanyFundMockExpectations(t, mock)
	})

	t.Run("matching fact may be inserted", func(t *testing.T) {
		db, mock := newCompanyFundMockDB(t)
		defer db.Close()
		repository := NewDBRepository(db)
		mock.ExpectBegin()
		mock.ExpectQuery(transactionForUpdateQueryPattern()).
			WithArgs("v1:fact-matching").
			WillReturnRows(sqlmock.NewRows(transactionForUpdateColumns()))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT channel, provider_account_key, provider_transaction_id, allocation_state, derivation_contract_version FROM company_fund_provider_transaction_facts WHERE id = $1")).
			WithArgs(factID).
			WillReturnRows(sqlmock.NewRows([]string{"channel", "provider_account_key", "provider_transaction_id", "allocation_state", "derivation_contract_version"}).AddRow("SAFEHERON", "account-a", "transaction-a", "UNPROVEN", nil))
		mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO company_fund_transactions")).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(208))
		mock.ExpectCommit()

		result, err := repository.UpsertCompanyFundTransaction(context.Background(), newInput("v1:fact-matching", "account-a", "transaction-a"))
		if err != nil || !result.Inserted || result.ID != 208 {
			t.Fatalf("matching fact result = %#v, %v", result, err)
		}
		assertCompanyFundMockExpectations(t, mock)
	})
}

func TestUpsertCompanyFundTransaction_CrossTransactionFactRequiresProvenDerivableContract(t *testing.T) {
	factID := int64(502)
	fromAccountID := int64(101)
	status := LifecycleStatusPending
	baseInput := func(movementKey string) TransactionUpsertInput {
		return TransactionUpsertInput{
			MovementKey:               movementKey,
			Channel:                   ChannelSafeheron,
			IdentityAlgorithmVersion:  MovementIdentityAlgorithmVersion,
			ProviderAccountKey:        "account-a",
			ProviderTransactionID:     "child-transaction",
			ProviderTransactionFactID: &factID,
			MovementKind:              MovementKindPrincipal,
			TransferMode:              TransferModeSingle,
			Direction:                 DirectionOutflow,
			FromCompanyFundAccountID:  &fromAccountID,
			Currency:                  "USDT",
			Amount:                    decimal.RequireFromString("10"),
			FirstSeenSource:           TransactionSeenSourceWebhook,
			Provider:                  ProviderOwnedFields{Status: &status},
		}
	}

	for _, testCase := range []struct {
		name               string
		allocationState    string
		derivationContract any
	}{
		{"unproven parent fact is rejected", "UNPROVEN", nil},
		{"proven parent fact without contract is rejected", "PROVEN_DERIVABLE", "   "},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db, mock := newCompanyFundMockDB(t)
			defer db.Close()
			repository := NewDBRepository(db)
			mock.ExpectBegin()
			mock.ExpectQuery(transactionForUpdateQueryPattern()).
				WithArgs("v1:cross-transaction-" + testCase.name).
				WillReturnRows(sqlmock.NewRows(transactionForUpdateColumns()))
			mock.ExpectQuery(regexp.QuoteMeta("SELECT channel, provider_account_key, provider_transaction_id, allocation_state, derivation_contract_version FROM company_fund_provider_transaction_facts WHERE id = $1")).
				WithArgs(factID).
				WillReturnRows(providerFactOwnershipRows("SAFEHERON", "account-a", "parent-transaction", testCase.allocationState, testCase.derivationContract))
			mock.ExpectRollback()

			result, err := repository.UpsertCompanyFundTransaction(context.Background(), baseInput("v1:cross-transaction-"+testCase.name))
			if err == nil || !result.Quarantined {
				t.Fatalf("cross-transaction rejected result = %#v, %v", result, err)
			}
			assertCompanyFundMockExpectations(t, mock)
		})
	}

	t.Run("proven parent fact with contract may update derived transaction", func(t *testing.T) {
		db, mock := newCompanyFundMockDB(t)
		defer db.Close()
		repository := NewDBRepository(db)
		revision := int64(1)
		input := baseInput("v1:cross-transaction-proven-update")
		input.Provider.Metadata = ProviderFactMetadata{Revision: &revision, Source: ProviderSourceWebhook}

		mock.ExpectBegin()
		mock.ExpectQuery(transactionForUpdateQueryPattern()).
			WithArgs(input.MovementKey).
			WillReturnRows(lockedTransactionRows(
				209, "SAFEHERON", MovementIdentityAlgorithmVersion, "account-a", "child-transaction", "", "movement-a", nil, nil, "",
				"10", "USDT", "", "", "", "PENDING", nil, "WEBHOOK", 0, "WEBHOOK", "", nil, nil, nil,
			))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT channel, provider_account_key, provider_transaction_id, allocation_state, derivation_contract_version FROM company_fund_provider_transaction_facts WHERE id = $1")).
			WithArgs(factID).
			WillReturnRows(providerFactOwnershipRows("SAFEHERON", "account-a", "parent-transaction", "PROVEN_DERIVABLE", "sandbox-v1"))
		mock.ExpectQuery(regexp.QuoteMeta("UPDATE company_fund_transactions")).
			WithArgs(
				int64(209), "account-a", "child-transaction", nil, "movement-a", factID,
				"10", "USDT", false, nil, nil, nil, nil,
				LifecycleStatusPending, revision, nil, ProviderSourceWebhook, 0,
				nil, nil, nil, nil, TransactionSeenSourceWebhook,
			).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(209))
		mock.ExpectCommit()

		result, err := repository.UpsertCompanyFundTransaction(context.Background(), input)
		if err != nil || result.ID != 209 || result.Quarantined {
			t.Fatalf("proven cross-transaction update result = %#v, %v", result, err)
		}
		assertCompanyFundMockExpectations(t, mock)
	})
}

func TestUpsertCompanyFundTransaction_IncompleteFactIdentityRequiresProvenDerivableContract(t *testing.T) {
	factID := int64(503)
	fromAccountID := int64(101)
	status := LifecycleStatusPending
	for _, testCase := range []struct {
		name              string
		targetAccountKey  string
		targetTransaction string
		factAccountKey    any
		factTransactionID any
	}{
		{"fact transaction ID missing", "account-a", "target-transaction", "account-a", nil},
		{"target transaction ID missing", "account-a", "", "account-a", "fact-transaction"},
		{"both transaction IDs missing", "account-a", "", "account-a", nil},
		{"fact account key missing", "account-a", "target-transaction", nil, "target-transaction"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db, mock := newCompanyFundMockDB(t)
			defer db.Close()
			repository := NewDBRepository(db)
			mock.ExpectBegin()
			mock.ExpectQuery(transactionForUpdateQueryPattern()).
				WithArgs("v1:incomplete-fact-" + testCase.name).
				WillReturnRows(sqlmock.NewRows(transactionForUpdateColumns()))
			mock.ExpectQuery(regexp.QuoteMeta("SELECT channel, provider_account_key, provider_transaction_id, allocation_state, derivation_contract_version FROM company_fund_provider_transaction_facts WHERE id = $1")).
				WithArgs(factID).
				WillReturnRows(providerFactOwnershipRows("SAFEHERON", testCase.factAccountKey, testCase.factTransactionID, "UNPROVEN", nil))
			mock.ExpectRollback()

			result, err := repository.UpsertCompanyFundTransaction(context.Background(), TransactionUpsertInput{
				MovementKey:               "v1:incomplete-fact-" + testCase.name,
				Channel:                   ChannelSafeheron,
				IdentityAlgorithmVersion:  MovementIdentityAlgorithmVersion,
				ProviderAccountKey:        testCase.targetAccountKey,
				ProviderTransactionID:     testCase.targetTransaction,
				ProviderTransactionFactID: &factID,
				MovementKind:              MovementKindPrincipal,
				TransferMode:              TransferModeSingle,
				Direction:                 DirectionOutflow,
				FromCompanyFundAccountID:  &fromAccountID,
				Currency:                  "USDT",
				Amount:                    decimal.RequireFromString("10"),
				FirstSeenSource:           TransactionSeenSourceWebhook,
				Provider:                  ProviderOwnedFields{Status: &status},
			})
			if err == nil || !result.Quarantined {
				t.Fatalf("incomplete fact identity result = %#v, %v", result, err)
			}
			assertCompanyFundMockExpectations(t, mock)
		})
	}

	t.Run("proven contract does not permit missing fact account identity", func(t *testing.T) {
		db, mock := newCompanyFundMockDB(t)
		defer db.Close()
		repository := NewDBRepository(db)
		mock.ExpectBegin()
		mock.ExpectQuery(transactionForUpdateQueryPattern()).
			WithArgs("v1:incomplete-fact-proven-missing-account").
			WillReturnRows(sqlmock.NewRows(transactionForUpdateColumns()))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT channel, provider_account_key, provider_transaction_id, allocation_state, derivation_contract_version FROM company_fund_provider_transaction_facts WHERE id = $1")).
			WithArgs(factID).
			WillReturnRows(providerFactOwnershipRows("SAFEHERON", nil, "target-transaction", "PROVEN_DERIVABLE", "sandbox-v1"))
		mock.ExpectRollback()

		result, err := repository.UpsertCompanyFundTransaction(context.Background(), TransactionUpsertInput{
			MovementKey:               "v1:incomplete-fact-proven-missing-account",
			Channel:                   ChannelSafeheron,
			IdentityAlgorithmVersion:  MovementIdentityAlgorithmVersion,
			ProviderAccountKey:        "account-a",
			ProviderTransactionID:     "target-transaction",
			ProviderTransactionFactID: &factID,
			MovementKind:              MovementKindPrincipal,
			TransferMode:              TransferModeSingle,
			Direction:                 DirectionOutflow,
			FromCompanyFundAccountID:  &fromAccountID,
			Currency:                  "USDT",
			Amount:                    decimal.RequireFromString("10"),
			FirstSeenSource:           TransactionSeenSourceWebhook,
			Provider:                  ProviderOwnedFields{Status: &status},
		})
		if err == nil || !result.Quarantined {
			t.Fatalf("proven missing fact account result = %#v, %v", result, err)
		}
		assertCompanyFundMockExpectations(t, mock)
	})

	t.Run("proven derivable contract permits incomplete transaction identity in same account", func(t *testing.T) {
		db, mock := newCompanyFundMockDB(t)
		defer db.Close()
		repository := NewDBRepository(db)
		mock.ExpectBegin()
		mock.ExpectQuery(transactionForUpdateQueryPattern()).
			WithArgs("v1:incomplete-fact-proven").
			WillReturnRows(sqlmock.NewRows(transactionForUpdateColumns()))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT channel, provider_account_key, provider_transaction_id, allocation_state, derivation_contract_version FROM company_fund_provider_transaction_facts WHERE id = $1")).
			WithArgs(factID).
			WillReturnRows(providerFactOwnershipRows("SAFEHERON", "account-a", nil, "PROVEN_DERIVABLE", "sandbox-v1"))
		mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO company_fund_transactions")).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(210))
		mock.ExpectCommit()

		result, err := repository.UpsertCompanyFundTransaction(context.Background(), TransactionUpsertInput{
			MovementKey:               "v1:incomplete-fact-proven",
			Channel:                   ChannelSafeheron,
			IdentityAlgorithmVersion:  MovementIdentityAlgorithmVersion,
			ProviderAccountKey:        "account-a",
			ProviderTransactionID:     "target-transaction",
			ProviderTransactionFactID: &factID,
			MovementKind:              MovementKindPrincipal,
			TransferMode:              TransferModeSingle,
			Direction:                 DirectionOutflow,
			FromCompanyFundAccountID:  &fromAccountID,
			Currency:                  "USDT",
			Amount:                    decimal.RequireFromString("10"),
			FirstSeenSource:           TransactionSeenSourceWebhook,
			Provider:                  ProviderOwnedFields{Status: &status},
		})
		if err != nil || !result.Inserted || result.ID != 210 {
			t.Fatalf("proven incomplete fact identity result = %#v, %v", result, err)
		}
		assertCompanyFundMockExpectations(t, mock)
	})
}

func TestUpsertCompanyFundTransaction_QuarantinesInvalidProviderEventPairBeforeWrite(t *testing.T) {
	fromAccountID := int64(101)
	status := LifecycleStatusPending
	validDigest := strings.Repeat("a", 64)
	providerEventID := int64(303)
	for _, testCase := range []struct {
		name         string
		eventChannel string
		eventDigest  string
		inputEventID *int64
		inputDigest  string
	}{
		{"channel mismatch", "AIRWALLEX", validDigest, &providerEventID, validDigest},
		{"digest mismatch", "SAFEHERON", strings.Repeat("b", 64), &providerEventID, validDigest},
		{"half pair", "", "", nil, validDigest},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db, mock := newCompanyFundMockDB(t)
			defer db.Close()
			repository := NewDBRepository(db)
			mock.ExpectBegin()
			if testCase.inputEventID != nil {
				mock.ExpectQuery(regexp.QuoteMeta("SELECT channel, source_payload_digest FROM company_fund_provider_events WHERE id = $1")).
					WithArgs(providerEventID).
					WillReturnRows(sqlmock.NewRows([]string{"channel", "source_payload_digest"}).AddRow(testCase.eventChannel, testCase.eventDigest))
			}
			mock.ExpectRollback()

			result, err := repository.UpsertCompanyFundTransaction(context.Background(), TransactionUpsertInput{
				MovementKey:              "v1:invalid-pair-" + testCase.name,
				Channel:                  ChannelSafeheron,
				IdentityAlgorithmVersion: MovementIdentityAlgorithmVersion,
				MovementKind:             MovementKindPrincipal,
				TransferMode:             TransferModeSingle,
				Direction:                DirectionOutflow,
				FromCompanyFundAccountID: &fromAccountID,
				Currency:                 "USDT",
				Amount:                   decimal.RequireFromString("10"),
				LatestProviderEventID:    testCase.inputEventID,
				RawSnapshotDigest:        testCase.inputDigest,
				FirstSeenSource:          TransactionSeenSourceWebhook,
				Provider:                 ProviderOwnedFields{Status: &status},
			})
			if err == nil || !result.Quarantined {
				t.Fatalf("invalid pair result = %#v, %v", result, err)
			}
			if _, ok := err.(*TransactionQuarantineError); !ok {
				t.Fatalf("invalid pair error type = %T, want TransactionQuarantineError", err)
			}
			assertCompanyFundMockExpectations(t, mock)
		})
	}
}

func TestTransactionProvenanceSQLNeverTouchesProviderProcessStatus(t *testing.T) {
	if strings.Contains(selectCompanyFundProviderEventProvenanceSQL, "process_status") {
		t.Fatal("transaction provenance verification must not read provider process_status")
	}
	if !strings.Contains(selectCompanyFundProviderEventProvenanceSQL, "source_payload_digest") {
		t.Fatal("transaction provenance verification must read the provider event payload digest")
	}
	if strings.Contains(selectProviderTransactionFactOwnershipSQL, "process_status") || strings.Contains(selectProviderTransactionFactOwnershipSQL, "raw_") {
		t.Fatal("provider fact ownership verification must not read provider raw/process state")
	}
	for _, column := range []string{"provider_transaction_id", "allocation_state", "derivation_contract_version"} {
		if !strings.Contains(selectProviderTransactionFactOwnershipSQL, column) {
			t.Errorf("provider fact ownership verification must read %s", column)
		}
	}
}

func TestUpsertCompanyFundTransaction_AssetReplacementCanClearOnlyWhenMergedAssetWins(t *testing.T) {
	existingRevision := int64(2)
	lowerRevision := int64(1)
	higherRevision := int64(3)
	fromAccountID := int64(101)
	status := LifecycleStatusPending

	for _, testCase := range []struct {
		name             string
		incomingRevision *int64
		incomingAsset    *AssetIdentity
		wantReplace      bool
		wantChain        any
		wantProviderKey  any
		wantContract     any
		wantRevision     int64
	}{
		{
			name:             "higher metadata clears token identity for native asset",
			incomingRevision: &higherRevision,
			incomingAsset:    &AssetIdentity{},
			wantReplace:      true,
			wantChain:        nil,
			wantProviderKey:  nil,
			wantContract:     nil,
			wantRevision:     higherRevision,
		},
		{
			name:             "lower metadata cannot clear stored token identity",
			incomingRevision: &lowerRevision,
			incomingAsset:    &AssetIdentity{},
			wantReplace:      false,
			wantChain:        "ETH",
			wantProviderKey:  "USDT-ERC20",
			wantContract:     "0xtoken",
			wantRevision:     existingRevision,
		},
		{
			name:             "missing incoming asset keeps stored identity",
			incomingRevision: &higherRevision,
			incomingAsset:    nil,
			wantReplace:      false,
			wantChain:        "ETH",
			wantProviderKey:  "USDT-ERC20",
			wantContract:     "0xtoken",
			wantRevision:     higherRevision,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db, mock := newCompanyFundMockDB(t)
			defer db.Close()
			repository := NewDBRepository(db)
			mock.ExpectBegin()
			mock.ExpectQuery(transactionForUpdateQueryPattern()).
				WithArgs("v1:asset-replacement-" + testCase.name).
				WillReturnRows(lockedTransactionRows(
					206, "SAFEHERON", MovementIdentityAlgorithmVersion, "", "", "", "", nil, nil, "",
					"10", "USDT", "ETH", "USDT-ERC20", "0xtoken", "PENDING", existingRevision, "WEBHOOK", 0, "WEBHOOK", "", nil, nil, nil,
				))
			mock.ExpectQuery(regexp.QuoteMeta("UPDATE company_fund_transactions")).
				WithArgs(
					int64(206), nil, nil, nil, nil, nil,
					"10", "USDT", testCase.wantReplace, testCase.wantChain, testCase.wantProviderKey, testCase.wantContract, nil,
					LifecycleStatusPending, testCase.wantRevision, nil, ProviderSourceWebhook, 0,
					nil, nil, nil, nil, TransactionSeenSourceWebhook,
				).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(206))
			mock.ExpectCommit()

			result, err := repository.UpsertCompanyFundTransaction(context.Background(), TransactionUpsertInput{
				MovementKey:              "v1:asset-replacement-" + testCase.name,
				Channel:                  ChannelSafeheron,
				IdentityAlgorithmVersion: MovementIdentityAlgorithmVersion,
				MovementKind:             MovementKindPrincipal,
				TransferMode:             TransferModeSingle,
				Direction:                DirectionOutflow,
				FromCompanyFundAccountID: &fromAccountID,
				Currency:                 "USDT",
				Amount:                   decimal.RequireFromString("10"),
				FirstSeenSource:          TransactionSeenSourceWebhook,
				Provider: ProviderOwnedFields{
					Metadata: ProviderFactMetadata{Revision: testCase.incomingRevision, Source: ProviderSourceWebhook},
					Status:   &status,
					Asset:    testCase.incomingAsset,
				},
			})
			if err != nil || result.ID != 206 || result.Quarantined {
				t.Fatalf("asset replacement result = %#v, %v", result, err)
			}
			assertCompanyFundMockExpectations(t, mock)
		})
	}

	for _, contract := range []string{
		"chain_code = CASE WHEN $9 THEN $10 ELSE chain_code END",
		"provider_asset_key = CASE WHEN $9 THEN $11 ELSE provider_asset_key END",
		"asset_contract = CASE WHEN $9 THEN $12 ELSE asset_contract END",
	} {
		if !strings.Contains(updateCompanyFundTransactionSQL, contract) {
			t.Errorf("asset replacement SQL is missing %q", contract)
		}
	}
}

func lockedTransactionRows(values ...any) *sqlmock.Rows {
	row := make([]driver.Value, len(values))
	for index, value := range values {
		row[index] = value
	}
	return sqlmock.NewRows(transactionForUpdateColumns()).AddRow(row...)
}

func providerFactOwnershipRows(channel string, accountKey, transactionID any, allocationState string, derivationContract any) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"channel", "provider_account_key", "provider_transaction_id", "allocation_state", "derivation_contract_version"}).
		AddRow(driver.Value(channel), asDriverValue(accountKey), asDriverValue(transactionID), driver.Value(allocationState), asDriverValue(derivationContract))
}

func asDriverValue(value any) driver.Value {
	return value
}
