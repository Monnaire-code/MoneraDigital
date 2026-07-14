package companyfund

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestPurgeExpiredOwnedProviderPayloads_UsesBoundedDatabaseClockCleanup(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	repository := NewDBRepository(db)

	mock.ExpectExec(regexp.QuoteMeta("WITH purge_candidates AS")).
		WithArgs(25).
		WillReturnResult(sqlmock.NewResult(0, 2))
	purged, err := repository.PurgeExpiredOwnedProviderPayloads(context.Background(), 25)
	if err != nil || purged != 2 {
		t.Fatalf("PurgeExpiredOwnedProviderPayloads() = %d, %v", purged, err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestPurgeExpiredOwnedProviderPayloads_RejectsInvalidLimitBeforeDatabaseUse(t *testing.T) {
	for _, limit := range []int{0, -1} {
		if _, err := NewDBRepository(nil).PurgeExpiredOwnedProviderPayloads(context.Background(), limit); err == nil {
			t.Fatalf("PurgeExpiredOwnedProviderPayloads(%d) unexpectedly succeeded", limit)
		}
	}
}

func TestPurgeExpiredOwnedProviderPayloads_SQLOnlyTerminalizesExpiringOwnedEvents(t *testing.T) {
	for _, contract := range []string{
		"WITH purge_candidates AS",
		"FOR UPDATE SKIP LOCKED",
		"owned_payload_retention_until <= NOW()",
		"owned_payload_legal_hold = false",
		"owned_payload_ciphertext IS NOT NULL",
		"owned_payload_purged_at IS NULL",
		"owned_payload_ciphertext = NULL",
		"owned_payload_purged_at = NOW()",
		"event_state = CASE",
		"processed_at = CASE",
		"next_attempt_at = CASE",
		"lease_owner = CASE",
		"lease_expires_at = CASE",
		"last_error = CASE",
		"OWNED_PAYLOAD_RETENTION_EXPIRED",
		"ELSE provider_event.event_state",
		"ELSE provider_event.processed_at",
		"ELSE provider_event.next_attempt_at",
		"ELSE provider_event.lease_owner",
		"ELSE provider_event.lease_expires_at",
		"ELSE provider_event.last_error",
		"LIMIT $1",
	} {
		if !strings.Contains(purgeExpiredOwnedProviderPayloadsSQL, contract) {
			t.Errorf("purge SQL is missing %q", contract)
		}
	}
	for _, forbidden := range []string{
		"process_status",
		"safeheron_webhook_events",
		"source_payload_digest =",
		"owned_payload_digest =",
		"owned_payload_key_version =",
		"owned_payload_retention_until =",
	} {
		if strings.Contains(purgeExpiredOwnedProviderPayloadsSQL, forbidden) {
			t.Errorf("purge SQL must not mutate/read %q", forbidden)
		}
	}
}
