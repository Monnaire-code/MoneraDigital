package companyfund

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestCreateCompanyFundSyncRun_CanonicalizesCheckpointAndReusesWindow(t *testing.T) {
	db, mock := newSyncRunMockDB(t)
	defer db.Close()
	repository := NewDBRepository(db)
	input := validCompanyFundSyncRunInput()
	input.Checkpoint = []byte(`{"z":2,"exact":9007199254740993}`)
	canonicalCheckpoint := `{"exact":9007199254740993,"z":2}`
	created := validCompanyFundSyncRunRecord()
	created.Checkpoint = []byte(canonicalCheckpoint)

	mock.ExpectQuery(regexp.QuoteMeta(insertCompanyFundSyncRunSQL)).
		WithArgs(input.Channel, input.SyncKind, input.WindowKey, input.WindowStart.UTC(), input.WindowEnd.UTC(), canonicalCheckpoint).
		WillReturnRows(companyFundSyncRunRows(created))
	inserted, err := repository.CreateCompanyFundSyncRun(context.Background(), input)
	if err != nil || !inserted.Inserted || inserted.Run.ID != created.ID || string(inserted.Run.Checkpoint) != canonicalCheckpoint {
		t.Fatalf("CreateCompanyFundSyncRun(insert) = %#v, %v", inserted, err)
	}

	// Checkpoint is mutable progress, not part of the immutable window identity.
	existing := created
	existing.Checkpoint = []byte(`{"cursor":"next-page"}`)
	mock.ExpectQuery(regexp.QuoteMeta(insertCompanyFundSyncRunSQL)).
		WithArgs(input.Channel, input.SyncKind, input.WindowKey, input.WindowStart.UTC(), input.WindowEnd.UTC(), canonicalCheckpoint).
		WillReturnRows(sqlmock.NewRows(companyFundSyncRunColumns()))
	mock.ExpectQuery(regexp.QuoteMeta(selectCompanyFundSyncRunByWindowSQL)).
		WithArgs(input.Channel, input.SyncKind, input.WindowKey).
		WillReturnRows(companyFundSyncRunRows(existing))
	duplicate, err := repository.CreateCompanyFundSyncRun(context.Background(), input)
	if err != nil || duplicate.Inserted || duplicate.Run.ID != created.ID || string(duplicate.Run.Checkpoint) != `{"cursor":"next-page"}` {
		t.Fatalf("CreateCompanyFundSyncRun(duplicate) = %#v, %v", duplicate, err)
	}
	assertSyncRunMockExpectations(t, mock)
}

func TestCreateCompanyFundSyncRun_RejectsDifferentWindowForSameIdempotencyKey(t *testing.T) {
	db, mock := newSyncRunMockDB(t)
	defer db.Close()
	repository := NewDBRepository(db)
	input := validCompanyFundSyncRunInput()
	existing := validCompanyFundSyncRunRecord()
	existing.WindowEnd = existing.WindowEnd.Add(time.Hour)

	mock.ExpectQuery(regexp.QuoteMeta(insertCompanyFundSyncRunSQL)).
		WillReturnRows(sqlmock.NewRows(companyFundSyncRunColumns()))
	mock.ExpectQuery(regexp.QuoteMeta(selectCompanyFundSyncRunByWindowSQL)).
		WillReturnRows(companyFundSyncRunRows(existing))
	if _, err := repository.CreateCompanyFundSyncRun(context.Background(), input); err == nil || !strings.Contains(err.Error(), "window_end") {
		t.Fatalf("CreateCompanyFundSyncRun() error = %v, want immutable window conflict", err)
	}
	assertSyncRunMockExpectations(t, mock)
}

func TestClaimNextCompanyFundSyncRun_UsesSkipLockedDatabaseLeaseAndCommits(t *testing.T) {
	db, mock := newSyncRunMockDB(t)
	defer db.Close()
	repository := NewDBRepository(db)
	run := validCompanyFundSyncRunRecord()
	databaseExpiry := time.Date(2026, time.July, 10, 4, 5, 0, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("FOR UPDATE SKIP LOCKED")).
		WithArgs(run.Channel, run.SyncKind).
		WillReturnRows(companyFundSyncRunRows(run))
	mock.ExpectQuery(regexp.QuoteMeta(updateClaimedCompanyFundSyncRunSQL)).
		WithArgs(run.ID, "sync-worker-a", time.Minute.Microseconds()).
		WillReturnRows(sqlmock.NewRows([]string{"attempt_count", "lease_expires_at"}).AddRow(2, databaseExpiry))
	mock.ExpectCommit()

	claimed, err := repository.ClaimNextCompanyFundSyncRun(context.Background(), CompanyFundSyncRunClaimScope{Channel: run.Channel, SyncKind: run.SyncKind}, "sync-worker-a", time.Minute)
	if err != nil || claimed == nil || claimed.Status != CompanyFundSyncRunStatusLeased || claimed.LeaseOwner != "sync-worker-a" || claimed.AttemptCount != 2 || claimed.LeaseExpiresAt == nil || !claimed.LeaseExpiresAt.Equal(databaseExpiry) {
		t.Fatalf("ClaimNextCompanyFundSyncRun() = %#v, %v", claimed, err)
	}
	assertSyncRunMockExpectations(t, mock)
}

func TestRenewAndUpdateCompanyFundSyncRunProgressRequireLiveLeaseAndCanonicalizeCheckpoint(t *testing.T) {
	db, mock := newSyncRunMockDB(t)
	defer db.Close()
	repository := NewDBRepository(db)
	expiry := time.Date(2026, time.July, 10, 5, 0, 0, 0, time.UTC)

	mock.ExpectQuery(regexp.QuoteMeta(renewCompanyFundSyncRunLeaseSQL)).
		WithArgs(int64(81), "sync-worker-a", time.Minute.Microseconds()).
		WillReturnRows(sqlmock.NewRows([]string{"lease_expires_at"}).AddRow(expiry))
	if got, err := repository.RenewCompanyFundSyncRunLease(context.Background(), 81, "sync-worker-a", time.Minute); err != nil || !got.Equal(expiry) {
		t.Fatalf("RenewCompanyFundSyncRunLease() = %s, %v", got, err)
	}

	checkpoint := []byte(`{"z":2,"exact":9007199254740993}`)
	mock.ExpectQuery(regexp.QuoteMeta(updateCompanyFundSyncRunProgressSQL)).
		WithArgs(int64(81), "sync-worker-a", `{"exact":9007199254740993,"z":2}`, 3, 2, 1, 4).
		WillReturnRows(sqlmock.NewRows([]string{
			"checkpoint", "candidates_seen", "events_created", "transactions_upserted", "transactions_skipped",
		}).AddRow(`{"exact": 9007199254740993, "z": 2}`, 8, 3, 2, 4))
	progress, err := repository.UpdateCompanyFundSyncRunProgress(context.Background(), 81, "sync-worker-a", CompanyFundSyncRunProgressUpdate{
		Checkpoint:                checkpoint,
		CandidatesSeenDelta:       3,
		EventsCreatedDelta:        2,
		TransactionsUpsertedDelta: 1,
		TransactionsSkippedDelta:  4,
	})
	if err != nil || string(progress.Checkpoint) != `{"exact":9007199254740993,"z":2}` || progress.CandidatesSeen != 8 || progress.TransactionsSkipped != 4 {
		t.Fatalf("UpdateCompanyFundSyncRunProgress() = %#v, %v", progress, err)
	}

	mock.ExpectQuery(regexp.QuoteMeta(renewCompanyFundSyncRunLeaseSQL)).
		WithArgs(int64(81), "lost-owner", time.Minute.Microseconds()).
		WillReturnRows(sqlmock.NewRows([]string{"lease_expires_at"}))
	if _, err := repository.RenewCompanyFundSyncRunLease(context.Background(), 81, "lost-owner", time.Minute); !errors.Is(err, ErrCompanyFundSyncRunLeaseNotOwned) {
		t.Fatalf("RenewCompanyFundSyncRunLease(lost) error = %v", err)
	}
	assertSyncRunMockExpectations(t, mock)
}

func TestFinalizeCompanyFundSyncRun_RetryIsDurableAndLeaseGuarded(t *testing.T) {
	db, mock := newSyncRunMockDB(t)
	defer db.Close()
	repository := NewDBRepository(db)
	retryAt := time.Now().UTC().Add(time.Minute)

	mock.ExpectQuery(regexp.QuoteMeta(finalizeCompanyFundSyncRunSQL)).
		WithArgs(int64(91), "sync-worker-a", CompanyFundSyncRunStatusFailed, retryAt, "provider pagination failed").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(91))
	if err := repository.FinalizeCompanyFundSyncRun(context.Background(), 91, "sync-worker-a", CompanyFundSyncRunFinalizeRetry, &retryAt, "provider pagination failed"); err != nil {
		t.Fatalf("FinalizeCompanyFundSyncRun(retry) = %v", err)
	}

	mock.ExpectQuery(regexp.QuoteMeta(finalizeCompanyFundSyncRunSQL)).
		WithArgs(int64(91), "lost-owner", CompanyFundSyncRunStatusSucceeded, nil, nil).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	if err := repository.FinalizeCompanyFundSyncRun(context.Background(), 91, "lost-owner", CompanyFundSyncRunFinalizeSucceeded, nil, ""); !errors.Is(err, ErrCompanyFundSyncRunLeaseNotOwned) {
		t.Fatalf("FinalizeCompanyFundSyncRun(lost) error = %v", err)
	}
	assertSyncRunMockExpectations(t, mock)
}

func TestCompanyFundSyncRunValidationAndSQLContracts(t *testing.T) {
	input := validCompanyFundSyncRunInput()
	for _, checkpoint := range [][]byte{
		[]byte(`[]`),
		[]byte(`{"a":1} trailing`),
		[]byte(`{"a":`),
	} {
		input.Checkpoint = checkpoint
		if _, err := input.canonical(); err == nil {
			t.Fatalf("invalid checkpoint %q unexpectedly accepted", checkpoint)
		}
	}
	if _, err := (CompanyFundSyncRunProgressUpdate{CandidatesSeenDelta: -1}).canonicalCheckpoint(); err != nil {
		t.Fatalf("nil checkpoint must preserve existing progress checkpoint: %v", err)
	}
	if err := (CompanyFundSyncRunProgressUpdate{CandidatesSeenDelta: -1}).validate(); err == nil {
		t.Fatal("negative progress delta must be rejected")
	}
	if _, err := NewDBRepository(nil).ClaimNextCompanyFundSyncRun(context.Background(), CompanyFundSyncRunClaimScope{Channel: Channel("OTHER"), SyncKind: "DAILY_RECONCILIATION"}, "sync-worker-a", time.Minute); err == nil {
		t.Fatal("invalid sync-run claim scope must be rejected before database use")
	}
	for _, required := range []string{
		"FOR UPDATE SKIP LOCKED",
		"next_attempt_at IS NOT NULL AND next_attempt_at <= NOW()",
		"status = 'LEASED' AND lease_expires_at <= NOW()",
		"NOW() + ($3::bigint * INTERVAL '1 microsecond')",
		"lease_expires_at > NOW()",
		"checkpoint = COALESCE($3::jsonb, checkpoint)",
	} {
		if !strings.Contains(claimNextCompanyFundSyncRunSQL+updateClaimedCompanyFundSyncRunSQL+renewCompanyFundSyncRunLeaseSQL+updateCompanyFundSyncRunProgressSQL+finalizeCompanyFundSyncRunSQL, required) {
			t.Fatalf("sync-run SQL missing %q", required)
		}
	}
}

func validCompanyFundSyncRunInput() CompanyFundSyncRunInput {
	return CompanyFundSyncRunInput{
		Channel:     ChannelSafeheron,
		SyncKind:    "DAILY_RECONCILIATION",
		WindowKey:   "2026-07-09",
		WindowStart: time.Date(2026, time.July, 8, 16, 0, 0, 0, time.UTC),
		WindowEnd:   time.Date(2026, time.July, 9, 16, 0, 0, 0, time.UTC),
		Checkpoint:  []byte(`{}`),
	}
}

func validCompanyFundSyncRunRecord() CompanyFundSyncRun {
	input := validCompanyFundSyncRunInput()
	createdAt := time.Date(2026, time.July, 10, 3, 0, 0, 0, time.UTC)
	return CompanyFundSyncRun{
		ID:          71,
		Channel:     input.Channel,
		SyncKind:    input.SyncKind,
		WindowKey:   input.WindowKey,
		WindowStart: input.WindowStart,
		WindowEnd:   input.WindowEnd,
		Status:      CompanyFundSyncRunStatusPending,
		Checkpoint:  []byte(`{}`),
		CreatedAt:   createdAt,
		UpdatedAt:   createdAt,
	}
}

func companyFundSyncRunColumns() []string {
	return []string{
		"id", "channel", "sync_kind", "window_key", "window_start", "window_end", "status", "checkpoint",
		"candidates_seen", "events_created", "transactions_upserted", "transactions_skipped", "attempt_count",
		"lease_owner", "lease_expires_at", "started_at", "completed_at", "next_attempt_at", "last_error", "created_at", "updated_at",
	}
}

func companyFundSyncRunRows(run CompanyFundSyncRun) *sqlmock.Rows {
	checkpoint := string(run.Checkpoint)
	if checkpoint == "" {
		checkpoint = `{}`
	}
	return sqlmock.NewRows(companyFundSyncRunColumns()).AddRow(
		run.ID,
		run.Channel,
		run.SyncKind,
		run.WindowKey,
		run.WindowStart,
		run.WindowEnd,
		run.Status,
		checkpoint,
		run.CandidatesSeen,
		run.EventsCreated,
		run.TransactionsUpserted,
		run.TransactionsSkipped,
		run.AttemptCount,
		companyFundSyncRunOptionalString(run.LeaseOwner),
		companyFundSyncRunOptionalTime(run.LeaseExpiresAt),
		companyFundSyncRunOptionalTime(run.StartedAt),
		companyFundSyncRunOptionalTime(run.CompletedAt),
		companyFundSyncRunOptionalTime(run.NextAttemptAt),
		companyFundSyncRunOptionalString(run.LastError),
		run.CreatedAt,
		run.UpdatedAt,
	)
}

func companyFundSyncRunOptionalString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func companyFundSyncRunOptionalTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return *value
}

func newSyncRunMockDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	return db, mock
}

func assertSyncRunMockExpectations(t *testing.T, mock sqlmock.Sqlmock) {
	t.Helper()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL mock expectation: %v", err)
	}
}
