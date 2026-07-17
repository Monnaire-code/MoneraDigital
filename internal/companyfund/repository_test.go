package companyfund

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"
)

func TestInsertProviderEvent_ValidatesSourceShapeAndDigestBeforeDatabaseUse(t *testing.T) {
	repository := NewDBRepository(nil)
	validExisting := validSafeheronProviderEvent()
	invalid := []ProviderEventInput{
		func() ProviderEventInput { value := validExisting; value.Channel = ChannelAirwallex; return value }(),
		func() ProviderEventInput { value := validExisting; value.ProviderEventID = ""; return value }(),
		func() ProviderEventInput {
			value := validExisting
			value.SourcePayloadDigest = strings.Repeat("A", 64)
			return value
		}(),
		func() ProviderEventInput {
			value := validExisting
			value.OwnedPayloadCiphertext = []byte("must-not-coexist")
			return value
		}(),
		func() ProviderEventInput {
			value := validOwnedProviderEvent()
			value.OwnedPayloadDigest = strings.Repeat("b", 64)
			return value
		}(),
		func() ProviderEventInput {
			value := validOwnedProviderEvent()
			value.OwnedPayloadCiphertext = nil
			return value
		}(),
		func() ProviderEventInput {
			value := validOwnedProviderEvent()
			value.ProviderEventVersion = strings.Repeat("v", maxProviderEventVersionBytes+1)
			return value
		}(),
		func() ProviderEventInput {
			value := validExisting
			value.AuthorizingRoutingActionID = 9
			return value
		}(),
	}
	for _, input := range invalid {
		if _, err := repository.InsertProviderEvent(context.Background(), input); err == nil {
			t.Fatalf("invalid provider event unexpectedly accepted: %#v", input)
		}
	}
}

func TestInsertProviderEvent_RoutingProjectionUsesAtomicCommandFence(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	input := validSafeheronProviderEvent()
	input.AuthorizedSafeheronOccurrenceKey = SafeheronOccurrenceAlgorithmVersion + ":" + strings.Repeat("a", 64)
	input.AuthorizingRoutingActionID = 9
	input.AuthorizingRoutingLeaseOwner = "routing-worker"
	mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronWebhookPayloadDigestSQL)).
		WithArgs(17).WillReturnRows(sqlmock.NewRows([]string{"payload_digest"}).AddRow(input.SourcePayloadDigest))
	mock.ExpectQuery("WITH authorized AS (?s).*FOR UPDATE OF action,command,routing(?s).*INSERT INTO company_fund_provider_events").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(41))
	result, err := NewDBRepository(db).InsertProviderEvent(context.Background(), input)
	if err != nil || !result.Inserted || result.ID != 41 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestInsertProviderEvent_IdempotentlyInsertsAndLooksUpDuplicate(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	repository := NewDBRepository(db)
	input := validSafeheronProviderEvent()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT payload_digest FROM safeheron_webhook_events WHERE id = $1")).
		WithArgs(17).
		WillReturnRows(sqlmock.NewRows([]string{"payload_digest"}).AddRow(input.SourcePayloadDigest))
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO company_fund_provider_events")).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(41))
	inserted, err := repository.InsertProviderEvent(context.Background(), input)
	if err != nil {
		t.Fatalf("InsertProviderEvent(insert): %v", err)
	}
	if !inserted.Inserted || inserted.ID != 41 {
		t.Fatalf("insert result = %#v", inserted)
	}

	mock.ExpectQuery(regexp.QuoteMeta("SELECT payload_digest FROM safeheron_webhook_events WHERE id = $1")).
		WithArgs(17).
		WillReturnRows(sqlmock.NewRows([]string{"payload_digest"}).AddRow(input.SourcePayloadDigest))
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO company_fund_provider_events")).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(regexp.QuoteMeta(selectProviderEventIdentitySQL)).
		WithArgs(input.Channel, input.ProviderEventID).
		WillReturnRows(providerEventIdentityRows(input, 41))
	duplicate, err := repository.InsertProviderEvent(context.Background(), input)
	if err != nil {
		t.Fatalf("InsertProviderEvent(duplicate): %v", err)
	}
	if duplicate.Inserted || duplicate.ID != 41 {
		t.Fatalf("duplicate result = %#v", duplicate)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestInsertProviderEvent_DuplicateRejectsConflictingImmutableIdentity(t *testing.T) {
	baseline := validSafeheronProviderEvent()
	baseline.ProviderEventVersion = "v1"

	tests := []struct {
		name          string
		input         func() ProviderEventInput
		conflictField string
	}{
		{
			name: "event type",
			input: func() ProviderEventInput {
				value := baseline
				value.EventType = "BALANCE"
				return value
			},
			conflictField: "event type",
		},
		{
			name: "source kind",
			input: func() ProviderEventInput {
				value := validOwnedProviderEvent()
				value.Channel = baseline.Channel
				value.ProviderEventID = baseline.ProviderEventID
				value.EventType = baseline.EventType
				value.ProviderEventVersion = baseline.ProviderEventVersion
				value.SourcePayloadDigest = baseline.SourcePayloadDigest
				value.OwnedPayloadDigest = baseline.SourcePayloadDigest
				return value
			},
			conflictField: "source kind",
		},
		{
			name: "source payload digest",
			input: func() ProviderEventInput {
				value := baseline
				value.SourcePayloadDigest = strings.Repeat("b", 64)
				return value
			},
			conflictField: "source payload digest",
		},
		{
			name: "Safeheron raw event reference",
			input: func() ProviderEventInput {
				value := baseline
				rawID := 18
				value.SafeheronWebhookEventID = &rawID
				return value
			},
			conflictField: "Safeheron raw event reference",
		},
		{
			name: "provider event version",
			input: func() ProviderEventInput {
				value := baseline
				value.ProviderEventVersion = "v2"
				return value
			},
			conflictField: "provider event version",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, mock := newCompanyFundMockDB(t)
			defer db.Close()
			repository := NewDBRepository(db)
			input := test.input()

			if input.SourceKind == ProviderEventSourceExistingSafeheronWebhookRef {
				mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronWebhookPayloadDigestSQL)).
					WithArgs(*input.SafeheronWebhookEventID).
					WillReturnRows(sqlmock.NewRows([]string{"payload_digest"}).AddRow(input.SourcePayloadDigest))
			}
			mock.ExpectQuery(regexp.QuoteMeta(insertProviderEventSQL)).
				WillReturnRows(sqlmock.NewRows([]string{"id"}))
			mock.ExpectQuery(regexp.QuoteMeta(selectProviderEventIdentitySQL)).
				WithArgs(input.Channel, input.ProviderEventID).
				WillReturnRows(providerEventIdentityRows(baseline, 41))

			if _, err := repository.InsertProviderEvent(context.Background(), input); !errors.Is(err, ErrProviderEventIdentityConflict) || !strings.Contains(err.Error(), test.conflictField) {
				t.Fatalf("InsertProviderEvent() error = %v, want %v for %s", err, ErrProviderEventIdentityConflict, test.conflictField)
			}
			assertCompanyFundMockExpectations(t, mock)
		})
	}
}

func TestInsertProviderEvent_DuplicateDoesNotCompareRotatedOwnedPayloadStorage(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	repository := NewDBRepository(db)
	input := validOwnedProviderEvent()
	input.ProviderEventVersion = "v1"
	input.OwnedPayloadCiphertext = []byte("rotated-ciphertext")
	input.OwnedPayloadKeyVersion = "v2"
	input.OwnedPayloadRetentionDuration = 48 * time.Hour

	mock.ExpectQuery(regexp.QuoteMeta(insertProviderEventSQL)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(regexp.QuoteMeta(selectProviderEventIdentitySQL)).
		WithArgs(input.Channel, input.ProviderEventID).
		WillReturnRows(providerEventIdentityRows(input, 81))

	result, err := repository.InsertProviderEvent(context.Background(), input)
	if err != nil || result != (ProviderEventInsertResult{ID: 81}) {
		t.Fatalf("InsertProviderEvent() = %#v, %v", result, err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestInsertProviderEvent_RejectsSafeheronReferenceWithoutMatchingStoredDigest(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	repository := NewDBRepository(db)
	input := validSafeheronProviderEvent()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT payload_digest FROM safeheron_webhook_events WHERE id = $1")).
		WithArgs(17).
		WillReturnRows(sqlmock.NewRows([]string{"payload_digest"}).AddRow(strings.Repeat("b", 64)))

	if _, err := repository.InsertProviderEvent(context.Background(), input); err == nil {
		t.Fatal("Safeheron reference with a mismatched stored digest must be rejected before INSERT")
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestClaimNextProviderEvent_UsesSkipLockedAndCommitsLease(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	repository := NewDBRepository(db)
	databaseLeaseExpiry := time.Date(2026, time.July, 10, 3, 1, 0, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, channel, provider_event_id, event_type")).
		WithArgs(SafeheronProviderClaimAll).
		WillReturnRows(sqlmock.NewRows(providerEventClaimColumns()).AddRow(
			7, "SAFEHERON", "event-7", "TRANSACTION", "v1", "org-a", "account-a", "EXISTING_SAFEHERON_WEBHOOK_REF",
			17, strings.Repeat("a", 64), "", int64(0), nil, nil, nil, nil, nil, false, 2,
		))
	mock.ExpectQuery(regexp.QuoteMeta("UPDATE company_fund_provider_events")).
		WithArgs(int64(7), "worker-a", time.Minute.Microseconds()).
		WillReturnRows(sqlmock.NewRows([]string{"attempt_count", "lease_expires_at"}).AddRow(3, databaseLeaseExpiry))
	mock.ExpectCommit()

	lease, err := repository.ClaimNextProviderEvent(context.Background(), "worker-a", time.Minute)
	if err != nil {
		t.Fatalf("ClaimNextProviderEvent: %v", err)
	}
	if lease == nil || lease.ID != 7 || lease.ProviderOrgKey != "org-a" || lease.ProviderAccountKey != "account-a" || lease.AttemptCount != 3 || !lease.LeaseExpiresAt.Equal(databaseLeaseExpiry) {
		t.Fatalf("lease = %#v", lease)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestClaimNextProviderEventCaptureOnlyExcludesRoutingScopedProjection(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	repository := NewDBRepository(db)
	repository.SetSafeheronProviderClaimMode(SafeheronProviderClaimDisabled)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, channel, provider_event_id, event_type")).
		WithArgs(SafeheronProviderClaimDisabled).
		WillReturnRows(sqlmock.NewRows(providerEventClaimColumns()))
	mock.ExpectRollback()
	lease, err := repository.ClaimNextProviderEvent(context.Background(), "capture-only", time.Minute)
	if err != nil || lease != nil {
		t.Fatalf("ClaimNextProviderEvent() lease=%#v err=%v", lease, err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestRenewProviderEventLease_UsesDatabaseClockAndRejectsLostLease(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	repository := NewDBRepository(db)
	databaseLeaseExpiry := time.Date(2026, time.July, 10, 4, 0, 0, 0, time.UTC)

	mock.ExpectQuery(regexp.QuoteMeta("UPDATE company_fund_provider_events")).
		WithArgs(int64(7), "worker-a", time.Minute.Microseconds()).
		WillReturnRows(sqlmock.NewRows([]string{"lease_expires_at"}).AddRow(databaseLeaseExpiry))
	expiresAt, err := repository.RenewProviderEventLease(context.Background(), 7, "worker-a", time.Minute)
	if err != nil || !expiresAt.Equal(databaseLeaseExpiry) {
		t.Fatalf("RenewProviderEventLease(success) = %v, %v", expiresAt, err)
	}

	for _, owner := range []string{"wrong-owner", "expired-owner"} {
		mock.ExpectQuery(regexp.QuoteMeta("UPDATE company_fund_provider_events")).
			WithArgs(int64(7), owner, time.Minute.Microseconds()).
			WillReturnRows(sqlmock.NewRows([]string{"lease_expires_at"}))
		if _, err := repository.RenewProviderEventLease(context.Background(), 7, owner, time.Minute); !errors.Is(err, ErrProviderEventLeaseNotOwned) {
			t.Fatalf("RenewProviderEventLease(%q) error = %v, want ErrProviderEventLeaseNotOwned", owner, err)
		}
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestClaimNextProviderEvent_LeavesLiveLeaseUntouched(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	repository := NewDBRepository(db)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, channel, provider_event_id, event_type")).
		WillReturnRows(sqlmock.NewRows(providerEventClaimColumns()))
	mock.ExpectRollback()

	lease, err := repository.ClaimNextProviderEvent(context.Background(), "worker-a", time.Minute)
	if err != nil {
		t.Fatalf("ClaimNextProviderEvent(empty): %v", err)
	}
	if lease != nil {
		t.Fatalf("live lease must not be claimed: %#v", lease)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestFinalizeProviderEvent_DeadLettersPermanentFailureAndPreservesErrorTruncation(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	repository := NewDBRepository(db)
	failureDetail := strings.Repeat("错", maxProviderEventErrorBytes)
	truncated := truncateProviderEventError(failureDetail)

	mock.ExpectExec(regexp.QuoteMeta("UPDATE company_fund_provider_events")).
		WithArgs(int64(7), "worker-a", "DEAD_LETTER", nil, truncated).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repository.FinalizeProviderEvent(context.Background(), 7, "worker-a", ProviderEventFinalizeFailed, nil, failureDetail); err != nil {
		t.Fatalf("FinalizeProviderEvent(dead letter): %v", err)
	}
	if len(truncated) > maxProviderEventErrorBytes {
		t.Fatalf("truncated error exceeds byte limit: %d", len(truncated))
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestFinalizeProviderEvent_RetryRequiresFutureBackoffAndLeaseOwner(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	repository := NewDBRepository(db)
	retryAt := time.Now().UTC().Add(time.Minute)

	mock.ExpectExec(regexp.QuoteMeta("UPDATE company_fund_provider_events")).
		WithArgs(int64(7), "worker-a", "FAILED", retryAt, "temporary provider failure").
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repository.FinalizeProviderEvent(context.Background(), 7, "worker-a", ProviderEventFinalizeRetry, &retryAt, "temporary provider failure"); err != nil {
		t.Fatalf("FinalizeProviderEvent(retry): %v", err)
	}

	pastRetryAt := time.Now().UTC().Add(-time.Minute)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE company_fund_provider_events")).
		WithArgs(int64(7), "worker-a", "FAILED", pastRetryAt, "past retry").
		WillReturnResult(sqlmock.NewResult(0, 0))
	if err := repository.FinalizeProviderEvent(context.Background(), 7, "worker-a", ProviderEventFinalizeRetry, &pastRetryAt, "past retry"); err == nil || !strings.Contains(err.Error(), "database clock") {
		t.Fatalf("past retry error = %v, want database-clock future backoff validation", err)
	}
	mock.ExpectExec(regexp.QuoteMeta("UPDATE company_fund_provider_events")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	if err := repository.FinalizeProviderEvent(context.Background(), 7, "wrong-owner", ProviderEventFinalizeProcessed, nil, ""); !errors.Is(err, ErrProviderEventLeaseNotOwned) {
		t.Fatal("finalize without the active lease owner must fail")
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestCompanyFundRepositorySQLContracts(t *testing.T) {
	for _, contract := range []string{
		"FOR UPDATE SKIP LOCKED",
		"lease_owner = $2",
		"lease_expires_at > NOW()",
		"event_state = 'PENDING'",
		"event_state = 'FAILED' AND next_attempt_at IS NOT NULL AND next_attempt_at <= NOW()",
		"event_state = 'LEASED' AND lease_expires_at <= NOW()",
		"NOW() + ($3::bigint * INTERVAL '1 microsecond')",
		"next_attempt_at = $4::TIMESTAMPTZ",
		"$4::TIMESTAMPTZ IS NOT NULL",
		"DEAD_LETTER",
	} {
		if !strings.Contains(claimNextProviderEventSQL+updateClaimedProviderEventSQL+finalizeProviderEventSQL+renewProviderEventLeaseSQL, contract) {
			t.Errorf("provider-event SQL is missing %q", contract)
		}
	}
	if strings.Contains(claimNextProviderEventSQL+updateClaimedProviderEventSQL, "DEAD_LETTER") {
		t.Fatal("dead-letter provider events must never be claimable")
	}
	for _, query := range []string{insertProviderEventSQL, selectSafeheronWebhookPayloadDigestSQL, claimNextProviderEventSQL, finalizeProviderEventSQL, renewProviderEventLeaseSQL} {
		if strings.Contains(query, "safeheron_webhook_events.process_status") || strings.Contains(query, "process_status") {
			t.Fatalf("company-fund repository must not read/write Safeheron deposit status: %s", query)
		}
	}
}

func TestUpsertCompanyFundTransaction_InsertsThroughChannelAwareMerge(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	repository := NewDBRepository(db)
	status := LifecycleStatusPending
	revision := int64(1)
	fromAccountID := int64(101)

	mock.ExpectBegin()
	mock.ExpectQuery(transactionForUpdateQueryPattern()).
		WillReturnRows(sqlmock.NewRows(transactionForUpdateColumns()))
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO company_fund_transactions")).
		WithArgs(
			ChannelSafeheron, nil, nil, nil, nil,
			0, "v1:movement-1", MovementIdentityAlgorithmVersion,
			MovementKindPrincipal, TransferModeSingle, DirectionOutflow,
			int64(101), nil,
			"USDT", nil, nil, nil, "1.234567890123456789",
			"0xtx", LifecycleStatusPending, int64(1), nil, ProviderSourceWebhook, 1,
			nil, nil, TransactionSeenSourceWebhook, TransactionSeenSourceWebhook,
			nil, nil, nil,
		).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(99))
	mock.ExpectCommit()

	result, err := repository.UpsertCompanyFundTransaction(context.Background(), TransactionUpsertInput{
		MovementKey:              "v1:movement-1",
		Channel:                  ChannelSafeheron,
		IdentityAlgorithmVersion: MovementIdentityAlgorithmVersion,
		MovementKind:             MovementKindPrincipal,
		TransferMode:             TransferModeSingle,
		Direction:                DirectionOutflow,
		FromCompanyFundAccountID: &fromAccountID,
		Currency:                 "USDT",
		Amount:                   decimal.RequireFromString("1.234567890123456789"),
		FirstSeenSource:          TransactionSeenSourceWebhook,
		Provider: ProviderOwnedFields{
			Metadata: ProviderFactMetadata{Revision: &revision, Source: ProviderSourceWebhook},
			Status:   &status,
			TxHash:   stringPointer("0xtx"),
		},
		ProviderStatusRank: 1,
	})
	if err != nil {
		t.Fatalf("UpsertCompanyFundTransaction: %v", err)
	}
	if !result.Inserted || result.ID != 99 {
		t.Fatalf("transaction result = %#v", result)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestUpsertCompanyFundTransaction_UsesProviderTimestampInsteadOfManualRowUpdatedAt(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	repository := NewDBRepository(db)
	fromAccountID := int64(101)
	existingRevision := int64(1)
	incomingRevision := int64(2)
	status := LifecycleStatusConfirming
	providerUpdatedAt := time.Date(2026, time.July, 10, 8, 0, 0, 0, time.UTC)
	incomingProviderUpdatedAt := providerUpdatedAt.Add(time.Hour)
	manualRowUpdatedAt := incomingProviderUpdatedAt.Add(time.Hour)
	if !manualRowUpdatedAt.After(incomingProviderUpdatedAt) {
		t.Fatal("test setup requires the manual row timestamp to be newer than the provider fact")
	}

	// A finance-only edit may have moved the generic row updated_at to
	// manualRowUpdatedAt. The locked provider projection must instead use the
	// older provider_updated_at, so this newer provider fact can update amount.
	mock.ExpectBegin()
	mock.ExpectQuery(transactionForUpdateQueryPattern()).
		WithArgs("v1:provider-timestamp").
		WillReturnRows(sqlmock.NewRows(transactionForUpdateColumns()).AddRow(
			77, "SAFEHERON", MovementIdentityAlgorithmVersion, "", "", "", "", nil, nil, "",
			"10", "USDT", "", "", "", "PENDING", existingRevision, "WEBHOOK", 0, "WEBHOOK", "", nil, nil, providerUpdatedAt,
		))
	mock.ExpectQuery(regexp.QuoteMeta("UPDATE company_fund_transactions")).
		WithArgs(
			int64(77), nil, nil, nil, nil, nil,
			"11", "USDT", false, nil, nil, nil, nil,
			LifecycleStatusConfirming, incomingRevision, incomingProviderUpdatedAt, ProviderSourceWebhook, 4,
			nil, nil, nil, nil, TransactionSeenSourceWebhook,
		).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(77))
	mock.ExpectCommit()

	result, err := repository.UpsertCompanyFundTransaction(context.Background(), TransactionUpsertInput{
		MovementKey:              "v1:provider-timestamp",
		Channel:                  ChannelSafeheron,
		IdentityAlgorithmVersion: MovementIdentityAlgorithmVersion,
		MovementKind:             MovementKindPrincipal,
		TransferMode:             TransferModeSingle,
		Direction:                DirectionOutflow,
		FromCompanyFundAccountID: &fromAccountID,
		Currency:                 "USDT",
		Amount:                   decimal.RequireFromString("11"),
		FirstSeenSource:          TransactionSeenSourceWebhook,
		Provider: ProviderOwnedFields{
			Metadata: ProviderFactMetadata{Revision: &incomingRevision, UpdatedAt: &incomingProviderUpdatedAt, Source: ProviderSourceWebhook},
			Status:   &status,
		},
	})
	if err != nil {
		t.Fatalf("UpsertCompanyFundTransaction: %v", err)
	}
	if result.Inserted || result.ID != 77 {
		t.Fatalf("transaction result = %#v", result)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestUpsertCompanyFundTransaction_LowerRevisionAssetKeepsMergedStoredIdentity(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	repository := NewDBRepository(db)
	fromAccountID := int64(101)
	higherRevision := int64(2)
	lowerRevision := int64(1)
	status := LifecycleStatusPending

	mock.ExpectBegin()
	mock.ExpectQuery(transactionForUpdateQueryPattern()).
		WithArgs("v1:asset-retained").
		WillReturnRows(sqlmock.NewRows(transactionForUpdateColumns()).AddRow(
			78, "SAFEHERON", MovementIdentityAlgorithmVersion, "", "", "", "", nil, nil, "",
			"10", "USDT", "ETH", "USDT-ERC20", "0xexisting", "PENDING", higherRevision, "WEBHOOK", 0, "WEBHOOK", "", nil, nil, nil,
		))
	mock.ExpectQuery(regexp.QuoteMeta("UPDATE company_fund_transactions")).
		WithArgs(
			int64(78), nil, nil, nil, nil, nil,
			"10", "USDT", false, "ETH", "USDT-ERC20", "0xexisting", nil,
			LifecycleStatusPending, higherRevision, nil, ProviderSourceWebhook, 0,
			nil, nil, nil, nil, TransactionSeenSourceWebhook,
		).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(78))
	mock.ExpectCommit()

	result, err := repository.UpsertCompanyFundTransaction(context.Background(), TransactionUpsertInput{
		MovementKey:              "v1:asset-retained",
		Channel:                  ChannelSafeheron,
		IdentityAlgorithmVersion: MovementIdentityAlgorithmVersion,
		MovementKind:             MovementKindPrincipal,
		TransferMode:             TransferModeSingle,
		Direction:                DirectionOutflow,
		FromCompanyFundAccountID: &fromAccountID,
		Currency:                 "USDT",
		Asset: AssetIdentity{
			ChainCode:        "BSC",
			ProviderAssetKey: "USDT-BEP20",
			ContractAddress:  "0xincoming",
		},
		Amount:          decimal.RequireFromString("10"),
		FirstSeenSource: TransactionSeenSourceWebhook,
		Provider: ProviderOwnedFields{
			Metadata: ProviderFactMetadata{Revision: &lowerRevision, Source: ProviderSourceWebhook},
			Status:   &status,
		},
	})
	if err != nil {
		t.Fatalf("UpsertCompanyFundTransaction: %v", err)
	}
	if result.Inserted || result.ID != 78 {
		t.Fatalf("transaction result = %#v", result)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestUpsertCompanyFundTransaction_EqualPriorityConflictingAssetQuarantinesWithoutWrite(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	repository := NewDBRepository(db)
	fromAccountID := int64(101)
	revision := int64(2)
	status := LifecycleStatusPending

	mock.ExpectBegin()
	mock.ExpectQuery(transactionForUpdateQueryPattern()).
		WithArgs("v1:asset-conflict").
		WillReturnRows(sqlmock.NewRows(transactionForUpdateColumns()).AddRow(
			79, "SAFEHERON", MovementIdentityAlgorithmVersion, "", "", "", "", nil, nil, "",
			"10", "USDT", "ETH", "USDT-ERC20", "0xexisting", "PENDING", revision, "WEBHOOK", 0, "WEBHOOK", "", nil, nil, nil,
		))
	mock.ExpectRollback()

	result, err := repository.UpsertCompanyFundTransaction(context.Background(), TransactionUpsertInput{
		MovementKey:              "v1:asset-conflict",
		Channel:                  ChannelSafeheron,
		IdentityAlgorithmVersion: MovementIdentityAlgorithmVersion,
		MovementKind:             MovementKindPrincipal,
		TransferMode:             TransferModeSingle,
		Direction:                DirectionOutflow,
		FromCompanyFundAccountID: &fromAccountID,
		Currency:                 "USDT",
		Asset: AssetIdentity{
			ChainCode:        "BSC",
			ProviderAssetKey: "USDT-BEP20",
			ContractAddress:  "0xincoming",
		},
		Amount:          decimal.RequireFromString("10"),
		FirstSeenSource: TransactionSeenSourceWebhook,
		Provider: ProviderOwnedFields{
			Metadata: ProviderFactMetadata{Revision: &revision, Source: ProviderSourceWebhook},
			Status:   &status,
		},
	})
	if err == nil {
		t.Fatal("equal-priority conflicting asset must quarantine")
	}
	if _, ok := err.(*TransactionQuarantineError); !ok {
		t.Fatalf("expected TransactionQuarantineError, got %T: %v", err, err)
	}
	if !result.Quarantined || result.ID != 79 {
		t.Fatalf("transaction result = %#v", result)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestProtectedTransactionUpsertSQLContracts(t *testing.T) {
	for _, contract := range []string{
		"ON CONFLICT (movement_key) DO NOTHING",
		"COALESCE($",
		"provider_status",
		"status_rank",
	} {
		if !strings.Contains(insertCompanyFundTransactionSQL+updateCompanyFundTransactionSQL, contract) {
			t.Errorf("protected transaction SQL is missing %q", contract)
		}
	}
	for _, contract := range []string{
		"$17::jsonb IS NOT NULL",
		"$20::boolean IS NOT NULL",
		"$27::varchar IS NOT NULL",
		"$29::jsonb IS NOT NULL",
		"$30::boolean IS NOT NULL",
	} {
		if !strings.Contains(updateCompanyFundTransactionProviderSupplementSQL, contract) {
			t.Fatalf("provider supplement CASE guard is missing %q", contract)
		}
	}
	for _, manualColumn := range []string{
		"finance_category_level1_id",
		"finance_category_level2_id",
		"is_operating_income_expense",
		"applicant",
		"business_description",
		"classification_",
		"risk_override_",
	} {
		if strings.Contains(updateCompanyFundTransactionSQL, manualColumn) {
			t.Errorf("protected transaction UPDATE must not touch manual column %q", manualColumn)
		}
	}
	source, err := os.ReadFile("repository.go")
	if err != nil {
		t.Fatalf("read repository source: %v", err)
	}
	if !strings.Contains(string(source), "MergeMovementProviderFieldsForChannel") {
		t.Fatal("transaction upsert must use the channel-aware lifecycle merge entry")
	}
	if strings.Contains(string(source), "nullableString(input.Asset.") {
		t.Fatal("transaction persistence must not write raw input.Asset instead of merged provider.Asset")
	}
	if !strings.Contains(selectCompanyFundTransactionForUpdateSQL, "provider_updated_at") {
		t.Fatal("provider metadata loading must select provider_updated_at")
	}
	if strings.Contains(selectCompanyFundTransactionForUpdateSQL, "completed_at, updated_at") {
		t.Fatal("provider metadata loading must not select generic row updated_at")
	}
	if !strings.Contains(insertCompanyFundTransactionSQL, "provider_updated_at") ||
		!strings.Contains(updateCompanyFundTransactionSQL, "provider_updated_at = COALESCE") {
		t.Fatal("provider metadata timestamp must be persisted through transaction insert and update")
	}
}

func validSafeheronProviderEvent() ProviderEventInput {
	rawID := 17
	return ProviderEventInput{
		Channel:                 ChannelSafeheron,
		ProviderEventID:         "safeheron-event-1",
		EventType:               "TRANSACTION",
		ProviderEventVersion:    "v1",
		SourceKind:              ProviderEventSourceExistingSafeheronWebhookRef,
		SafeheronWebhookEventID: &rawID,
		SourcePayloadDigest:     strings.Repeat("a", 64),
	}
}

func validOwnedProviderEvent() ProviderEventInput {
	return ProviderEventInput{
		Channel:                       ChannelAirwallex,
		ProviderEventID:               "airwallex-event-1",
		EventType:                     "PAYMENT",
		ProviderEventVersion:          "v1",
		SourceKind:                    ProviderEventSourceOwnedEncryptedPayload,
		SourcePayloadDigest:           strings.Repeat("b", 64),
		OwnedPayloadCiphertext:        []byte("ciphertext"),
		OwnedPayloadDigest:            strings.Repeat("b", 64),
		OwnedPayloadKeyVersion:        "v1",
		OwnedPayloadRetentionDuration: 24 * time.Hour,
	}
}

func providerEventClaimColumns() []string {
	return []string{
		"id", "channel", "provider_event_id", "event_type", "provider_event_version", "provider_org_key", "provider_account_key", "source_kind",
		"safeheron_webhook_event_id", "source_payload_digest", "authorized_safeheron_occurrence_key", "authorizing_routing_action_id", "owned_payload_ciphertext",
		"owned_payload_digest", "owned_payload_key_version", "owned_payload_retention_until",
		"owned_payload_purged_at", "owned_payload_legal_hold", "attempt_count",
	}
}

func providerEventIdentityRows(input ProviderEventInput, id int64) *sqlmock.Rows {
	var rawEventID any
	if input.SafeheronWebhookEventID != nil {
		rawEventID = *input.SafeheronWebhookEventID
	}
	return sqlmock.NewRows([]string{
		"id", "event_type", "source_kind", "source_payload_digest",
		"safeheron_webhook_event_id", "provider_event_version", "authorized_safeheron_occurrence_key", "authorizing_routing_action_id",
	}).AddRow(id, input.EventType, input.SourceKind, input.SourcePayloadDigest, rawEventID, input.ProviderEventVersion, input.AuthorizedSafeheronOccurrenceKey, nullablePositiveInt64(input.AuthorizingRoutingActionID))
}

func transactionForUpdateColumns() []string {
	return []string{
		"id", "channel", "identity_algorithm_version",
		"provider_account_key", "provider_transaction_id", "provider_event_id", "provider_movement_id",
		"provider_transaction_fact_id", "latest_provider_event_id", "raw_snapshot_digest",
		"amount", "currency", "chain_code", "provider_asset_key", "asset_contract",
		"provider_status", "provider_status_version", "provider_fact_source", "status_rank", "last_seen_source",
		"tx_hash", "occurred_at", "completed_at", "provider_updated_at",
	}
}

func transactionForUpdateQueryPattern() string {
	return `(?s)SELECT id, channel, identity_algorithm_version,.*occurred_at, completed_at, provider_updated_at.*FOR UPDATE`
}

func newCompanyFundMockDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	return db, mock
}

func assertCompanyFundMockExpectations(t *testing.T, mock sqlmock.Sqlmock) {
	t.Helper()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func timePointer(value time.Time) *time.Time { return &value }
