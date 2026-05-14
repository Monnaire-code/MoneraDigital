package pool

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

var testColumns = []string{
	"id", "network_family", "address", "safeheron_account_key", "customer_ref_id",
	"address_group_key", "derive_path", "account_tag", "hidden_on_ui", "auto_fuel",
	"status", "assigned_user_id", "assigned_at", "created_at", "updated_at",
}

func TestGetUserAddress_Found(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)

	now := time.Now()
	userID := 42
	mock.ExpectQuery("SELECT .+ FROM address_pool").
		WithArgs(userID, "EVM").
		WillReturnRows(sqlmock.NewRows(testColumns).
			AddRow(1, "EVM", "0xabc", "ak-1", "cref-1",
				"agk-1", "m/44/60", "DEPOSIT", true, false,
				"ASSIGNED", userID, now, now, now))

	addr, err := repo.GetUserAddress(context.Background(), userID, "EVM")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if addr.Address != "0xabc" {
		t.Errorf("expected 0xabc, got %s", addr.Address)
	}
	if addr.AssignedUserID == nil || *addr.AssignedUserID != userID {
		t.Errorf("expected assigned_user_id=%d", userID)
	}
}

func TestGetUserAddress_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)

	mock.ExpectQuery("SELECT .+ FROM address_pool").
		WithArgs(1, "EVM").
		WillReturnRows(sqlmock.NewRows(testColumns))

	_, err = repo.GetUserAddress(context.Background(), 1, "EVM")
	if err == nil {
		t.Fatal("expected error for not found")
	}
}

func TestAssignAvailable_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)

	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .+ FROM address_pool.+assigned_user_id").
		WithArgs(99, "EVM").
		WillReturnRows(sqlmock.NewRows(testColumns))
	mock.ExpectQuery("UPDATE address_pool").
		WillReturnRows(sqlmock.NewRows(testColumns).
			AddRow(5, "EVM", "0xdef", "ak-5", "cref-5",
				nil, nil, "DEPOSIT", true, false,
				"ASSIGNED", 99, now, now, now))
	mock.ExpectCommit()

	addr, err := repo.AssignAvailable(context.Background(), 99, "EVM")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if addr.ID != 5 || addr.Status != "ASSIGNED" {
		t.Errorf("unexpected addr: %+v", addr)
	}
}

func TestAssignAvailable_PoolEmpty(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .+ FROM address_pool.+assigned_user_id").
		WithArgs(1, "EVM").
		WillReturnRows(sqlmock.NewRows(testColumns))
	mock.ExpectQuery("UPDATE address_pool").
		WillReturnRows(sqlmock.NewRows(testColumns))
	mock.ExpectRollback()

	_, err = repo.AssignAvailable(context.Background(), 1, "EVM")
	if !errors.Is(err, ErrPoolEmpty) {
		t.Fatalf("expected ErrPoolEmpty, got %v", err)
	}
}

func TestCountByStatus(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)

	mock.ExpectQuery("SELECT COUNT").
		WithArgs("EVM", "AVAILABLE").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(42))

	count, err := repo.CountByStatus(context.Background(), "EVM", "AVAILABLE")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 42 {
		t.Errorf("expected 42, got %d", count)
	}
}

func TestBulkInsert_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)

	addrs := []*Address{
		{NetworkFamily: "EVM", Address: "0x1", SafeheronAccountKey: "ak1", CustomerRefID: "cr1", Status: StatusAvailable},
		{NetworkFamily: "EVM", Address: "0x2", SafeheronAccountKey: "ak2", CustomerRefID: "cr2", Status: StatusAvailable},
	}

	mock.ExpectExec("INSERT INTO address_pool").
		WillReturnResult(sqlmock.NewResult(0, 2))

	err = repo.BulkInsert(context.Background(), addrs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBulkInsert_Empty(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)

	err = repo.BulkInsert(context.Background(), nil)
	if err != nil {
		t.Fatalf("expected no error for empty insert, got %v", err)
	}
}

func TestBulkInsert_ExceedsMaxBatch(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)

	addrs := make([]*Address, maxBulkInsertBatch+1)
	for i := range addrs {
		addrs[i] = &Address{NetworkFamily: "EVM", Status: StatusAvailable}
	}

	err = repo.BulkInsert(context.Background(), addrs)
	if err == nil {
		t.Fatal("expected error for batch exceeding max")
	}
}

func TestAssignAvailable_ReturnsExistingForSameUser(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)

	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .+ FROM address_pool.+assigned_user_id").
		WithArgs(42, "EVM").
		WillReturnRows(sqlmock.NewRows(testColumns).
			AddRow(1, "EVM", "0xexisting", "ak-1", "cref-1",
				nil, nil, "DEPOSIT", true, false,
				"ASSIGNED", 42, now, now, now))
	mock.ExpectCommit()

	addr, err := repo.AssignAvailable(context.Background(), 42, "EVM")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if addr.Address != "0xexisting" {
		t.Errorf("expected existing address, got %s", addr.Address)
	}
}
