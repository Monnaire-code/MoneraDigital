package companyfund

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestInsertProviderEvent_UsesDatabaseClockForOwnedRetention(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	repository := NewDBRepository(db)
	retention := 2*time.Hour + 3*time.Minute + 4*time.Microsecond
	input := ProviderEventInput{
		Channel:                       ChannelAirwallex,
		ProviderEventID:               "airwallex-event-retention",
		EventType:                     "PAYMENT",
		SourceKind:                    ProviderEventSourceOwnedEncryptedPayload,
		SourcePayloadDigest:           strings.Repeat("a", 64),
		OwnedPayloadCiphertext:        []byte("ciphertext"),
		OwnedPayloadDigest:            strings.Repeat("a", 64),
		OwnedPayloadKeyVersion:        "v1",
		OwnedPayloadRetentionDuration: retention,
	}

	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO company_fund_provider_events")).
		WithArgs(
			input.Channel, input.ProviderEventID, input.EventType, nil, nil, nil,
			input.SourceKind, nil, input.SourcePayloadDigest,
			nil,
			input.OwnedPayloadCiphertext, input.OwnedPayloadDigest, input.OwnedPayloadKeyVersion,
			retention.Microseconds(), false, nil,
		).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(81))

	result, err := repository.InsertProviderEvent(context.Background(), input)
	if err != nil || result != (ProviderEventInsertResult{ID: 81, Inserted: true}) {
		t.Fatalf("InsertProviderEvent() = %#v, %v", result, err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestInsertProviderEvent_RejectsInvalidOwnedRetentionDurationBeforeDatabaseUse(t *testing.T) {
	validSafeheron := validSafeheronProviderEvent()
	validSafeheron.OwnedPayloadRetentionDuration = time.Hour
	if _, err := NewDBRepository(nil).InsertProviderEvent(context.Background(), validSafeheron); err == nil {
		t.Fatal("Safeheron reference must require zero owned payload retention duration")
	}

	validOwned := ProviderEventInput{
		Channel:                ChannelAirwallex,
		ProviderEventID:        "airwallex-event-zero-retention",
		EventType:              "PAYMENT",
		SourceKind:             ProviderEventSourceOwnedEncryptedPayload,
		SourcePayloadDigest:    strings.Repeat("a", 64),
		OwnedPayloadCiphertext: []byte("ciphertext"),
		OwnedPayloadDigest:     strings.Repeat("a", 64),
		OwnedPayloadKeyVersion: "v1",
	}
	for _, retention := range []time.Duration{0, -time.Hour, time.Nanosecond} {
		validOwned.OwnedPayloadRetentionDuration = retention
		if _, err := NewDBRepository(nil).InsertProviderEvent(context.Background(), validOwned); err == nil {
			t.Fatalf("owned retention duration %s must be rejected before database use", retention)
		}
	}
}

func TestProviderEventRetentionSQLContracts(t *testing.T) {
	for _, contract := range []string{
		"NOW() + ($14::bigint * INTERVAL '1 microsecond')",
		"CASE WHEN $14::bigint = 0 THEN NULL",
		"owned_payload_purged_at IS NULL",
		"source_kind <> 'OWNED_ENCRYPTED_PAYLOAD'",
		"owned_payload_legal_hold = true",
		"owned_payload_retention_until > NOW()",
	} {
		if !strings.Contains(insertProviderEventSQL+claimNextProviderEventSQL+updateClaimedProviderEventSQL+renewProviderEventLeaseSQL, contract) {
			t.Errorf("provider-event retention SQL is missing %q", contract)
		}
	}

	for _, contract := range []string{
		"provider_event.event_state IN ('PENDING', 'FAILED', 'LEASED')",
		"event_state = CASE",
		"processed_at = CASE",
		"next_attempt_at = CASE",
		"lease_owner = CASE",
		"lease_expires_at = CASE",
		"last_error = CASE",
		"OWNED_PAYLOAD_RETENTION_EXPIRED",
	} {
		if !strings.Contains(purgeExpiredOwnedProviderPayloadsSQL, contract) {
			t.Errorf("purge lifecycle SQL is missing %q", contract)
		}
	}

	for _, forbidden := range []string{"safeheron_webhook_events", "process_status"} {
		if strings.Contains(purgeExpiredOwnedProviderPayloadsSQL, forbidden) {
			t.Errorf("purge lifecycle SQL must not touch %q", forbidden)
		}
	}
}
