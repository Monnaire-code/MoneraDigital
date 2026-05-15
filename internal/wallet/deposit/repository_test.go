package deposit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestInsertEventOrSkip_NewRow(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectExec("INSERT INTO safeheron_webhook_events").
		WithArgs("evt-1", "T", "tk", "cref", []byte(`{}`)).
		WillReturnResult(sqlmock.NewResult(1, 1))

	r := NewRepository(db)
	inserted, err := r.InsertEventOrSkip(context.Background(), &Event{
		EventID:        "evt-1",
		EventType:      "T",
		SafeheronTxKey: "tk",
		CustomerRefID:  "cref",
		RawPayload:     []byte(`{}`),
	})
	if err != nil || !inserted {
		t.Fatalf("expected inserted=true, got %v / %v", inserted, err)
	}
}

func TestInsertEventOrSkip_Conflict(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.ExpectExec("INSERT INTO safeheron_webhook_events").
		WillReturnResult(sqlmock.NewResult(0, 0))
	r := NewRepository(db)
	inserted, err := r.InsertEventOrSkip(context.Background(), &Event{EventID: "evt-1", RawPayload: []byte(`{}`)})
	if err != nil || inserted {
		t.Fatalf("expected inserted=false, got %v / %v", inserted, err)
	}
}

func TestLockNextPendingEvent_Found(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	cols := []string{"id", "event_id", "event_type", "safeheron_tx_key",
		"customer_ref_id", "raw_payload", "process_status",
		"process_attempts", "error_message"}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .+ FROM safeheron_webhook_events").
		WillReturnRows(sqlmock.NewRows(cols).
			AddRow(7, "evt-7", "T", "tk", "cr", []byte(`{}`), "PENDING", 0, ""))
	mock.ExpectCommit()

	r := NewRepository(db)
	tx, err := r.BeginTx(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	evt, err := r.LockNextPendingEvent(context.Background(), tx)
	if err != nil {
		t.Fatal(err)
	}
	if evt.ID != 7 || evt.EventID != "evt-7" {
		t.Errorf("unexpected event: %+v", evt)
	}
	_ = tx.Commit()
}

func TestLockNextPendingEvent_NoRows(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	cols := []string{"id", "event_id", "event_type", "safeheron_tx_key",
		"customer_ref_id", "raw_payload", "process_status",
		"process_attempts", "error_message"}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .+ FROM safeheron_webhook_events").WillReturnRows(sqlmock.NewRows(cols))
	mock.ExpectRollback()

	r := NewRepository(db)
	tx, _ := r.BeginTx(context.Background())
	_, err := r.LockNextPendingEvent(context.Background(), tx)
	if !errors.Is(err, ErrNoPending) {
		t.Fatalf("expected ErrNoPending, got %v", err)
	}
	_ = tx.Rollback()
}

func TestLookupAddressOwner_Found(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.ExpectQuery("SELECT .+ FROM address_pool").
		WithArgs("0xabc", "EVM").
		WillReturnRows(sqlmock.NewRows([]string{"u"}).AddRow(42))
	r := NewRepository(db)
	uid, found, err := r.LookupAddressOwner(context.Background(), "0xabc", "EVM")
	if err != nil || !found || uid != 42 {
		t.Fatalf("expected uid=42 found=true, got %v %v %v", uid, found, err)
	}
}

func TestLookupAddressOwner_Unassigned(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.ExpectQuery("SELECT .+ FROM address_pool").
		WithArgs("0xstranger", "EVM").
		WillReturnRows(sqlmock.NewRows([]string{"u"}).AddRow(0))
	r := NewRepository(db)
	uid, found, _ := r.LookupAddressOwner(context.Background(), "0xstranger", "EVM")
	if found || uid != 0 {
		t.Errorf("expected unassigned, got uid=%d found=%v", uid, found)
	}
}

func TestLookupAddressOwner_NotFound(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.ExpectQuery("SELECT .+ FROM address_pool").
		WithArgs("0xunknown", "EVM").
		WillReturnRows(sqlmock.NewRows([]string{"u"}))
	r := NewRepository(db)
	uid, found, err := r.LookupAddressOwner(context.Background(), "0xunknown", "EVM")
	if err != nil || found || uid != 0 {
		t.Errorf("expected (0, false, nil), got (%d, %v, %v)", uid, found, err)
	}
}

func TestMarkEventDone(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE safeheron_webhook_events").
		WithArgs(int64(5)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	r := NewRepository(db)
	tx, _ := r.BeginTx(context.Background())
	if err := r.MarkEventDone(context.Background(), tx, 5); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit()
}

func TestMarkEventError(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE safeheron_webhook_events").
		WithArgs(int64(5), "boom").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	r := NewRepository(db)
	tx, _ := r.BeginTx(context.Background())
	if err := r.MarkEventError(context.Background(), tx, 5, "boom"); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit()
}

func TestMarkDepositCreditedFailedManualReview(t *testing.T) {
	cases := []struct {
		name string
		fn   func(r *DBRepository, tx Tx) error
	}{
		{"credited", func(r *DBRepository, tx Tx) error {
			return r.MarkDepositCredited(context.Background(), tx, 1)
		}},
		{"failed", func(r *DBRepository, tx Tx) error {
			return r.MarkDepositFailed(context.Background(), tx, 1, "x")
		}},
		{"manual", func(r *DBRepository, tx Tx) error {
			return r.MarkDepositManualReview(context.Background(), tx, 1, "x")
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db, mock, _ := sqlmock.New()
			defer db.Close()
			mock.ExpectBegin()
			mock.ExpectExec("UPDATE deposits").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()

			r := NewRepository(db)
			tx, _ := r.BeginTx(context.Background())
			if err := c.fn(r, tx); err != nil {
				t.Fatal(err)
			}
			_ = tx.Commit()
		})
	}
}

func TestFindOrCreateAccountForUpdate(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.ExpectBegin()
	// Require frozen_balance in the INSERT column list — regression guard for the NOT NULL bug.
	mock.ExpectQuery(`INSERT INTO account \(user_id, type, currency, balance, frozen_balance\)`).
		WithArgs(42, "ETH").
		WillReturnRows(sqlmock.NewRows([]string{"id", "balance"}).AddRow(101, "0"))
	mock.ExpectCommit()

	r := NewRepository(db)
	tx, _ := r.BeginTx(context.Background())
	id, bal, err := r.FindOrCreateAccountForUpdate(context.Background(), tx, 42, "ETH")
	if err != nil || id != 101 || bal != "0" {
		t.Fatalf("unexpected: %d %s %v", id, bal, err)
	}
	_ = tx.Commit()
}

func TestFindOrCreateAccountForUpdate_DBError(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO account \(user_id, type, currency, balance, frozen_balance\)`).
		WithArgs(42, "ETH").
		WillReturnError(fmt.Errorf("connection reset"))
	mock.ExpectRollback()

	r := NewRepository(db)
	tx, _ := r.BeginTx(context.Background())
	_, _, err := r.FindOrCreateAccountForUpdate(context.Background(), tx, 42, "ETH")
	if err == nil || !strings.Contains(err.Error(), "find or create account") {
		t.Fatalf("expected wrapped error, got %v", err)
	}
}

func TestFindOrCreateAccountForUpdate_ExistingAccount(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO account \(user_id, type, currency, balance, frozen_balance\)`).
		WithArgs(42, "ETH").
		WillReturnRows(sqlmock.NewRows([]string{"id", "balance"}).AddRow(101, "5.5"))
	mock.ExpectCommit()

	r := NewRepository(db)
	tx, _ := r.BeginTx(context.Background())
	id, bal, err := r.FindOrCreateAccountForUpdate(context.Background(), tx, 42, "ETH")
	if err != nil || id != 101 || bal != "5.5" {
		t.Fatalf("unexpected: %d %s %v", id, bal, err)
	}
	_ = tx.Commit()
}

func TestCreditAccount(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectQuery("UPDATE account").
		WithArgs(int64(101), "1.5").
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow("3.0"))
	mock.ExpectCommit()

	r := NewRepository(db)
	tx, _ := r.BeginTx(context.Background())
	bal, err := r.CreditAccount(context.Background(), tx, 101, "1.5")
	if err != nil || bal != "3.0" {
		t.Fatalf("unexpected: %s %v", bal, err)
	}
	_ = tx.Commit()
}

func TestWriteJournal(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO account_journal").
		WithArgs("S1", int64(42), int64(101), "1.5", "3.0", "10", int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	r := NewRepository(db)
	tx, _ := r.BeginTx(context.Background())
	err := r.WriteJournal(context.Background(), tx, &JournalEntry{
		SerialNo: "S1", UserID: 42, AccountID: 101,
		Amount: "1.5", BalanceSnapshot: "3.0",
		BizType: JournalBizTypeDeposit, RefID: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit()
}

func TestUpsertDeposit_NewRow(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	// R2-I-1: safeheron_coin_key column now scanned alongside safeheron_tx_key.
	cols := []string{"id", "user_id", "safeheron_tx_key", "safeheron_coin_key",
		"amount", "asset",
		"chain_code", "coin_chain_id", "safeheron_status", "safeheron_sub_status",
		"status_rank", "block_height", "block_hash", "status"}

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO deposits").
		WillReturnRows(sqlmock.NewRows(cols).
			AddRow(1, 42, "tk-1", "USDT_ERC20", "1.5", "ETH", "ETHEREUM", 11,
				"COMPLETED", "CONFIRMED", 100, 0, "", "PENDING"))
	mock.ExpectCommit()

	r := NewRepository(db)
	tx, _ := r.BeginTx(context.Background())
	out, err := r.UpsertDeposit(context.Background(), tx, &DepositRow{
		UserID: 42, SafeheronTxKey: "tk-1", SafeheronCoinKey: "USDT_ERC20",
		Amount: "1.5", Asset: "ETH",
		ChainCode: "ETHEREUM", CoinChainID: 11,
		SafeheronStatus: "COMPLETED", SafeheronSubStatus: "CONFIRMED",
		StatusRank: 100, Status: "PENDING",
	})
	if err != nil || out.ID != 1 {
		t.Fatalf("unexpected: %+v %v", out, err)
	}
	if out.SafeheronCoinKey != "USDT_ERC20" {
		t.Errorf("expected SafeheronCoinKey to round-trip through Scan, got %q", out.SafeheronCoinKey)
	}
	_ = tx.Commit()
}

func TestNullableTxHash(t *testing.T) {
	if got := nullableTxHash("0xabc", "tk-1"); got != "0xabc" {
		t.Errorf("on-chain hash should win, got %s", got)
	}
	if got := nullableTxHash("", "tk-1"); got != "sh:tk-1" {
		t.Errorf("expected sh:tk-1, got %s", got)
	}
}

func TestAsSQLTx_PanicsOnWrongType(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic")
		}
	}()
	asSQLTx(&fakeTx{})
}

func TestLockOneAmlPending_Found(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cols := []string{"id", "user_id", "safeheron_tx_key", "safeheron_coin_key",
		"amount", "asset", "chain_code", "coin_chain_id",
		"safeheron_status", "safeheron_sub_status", "status_rank",
		"block_height", "block_hash", "status",
		"from_address", "to_address", "tx_hash"}

	mock.ExpectBegin()
	mock.ExpectQuery(`aml_risk_level`).
		WillReturnRows(sqlmock.NewRows(cols).
			AddRow(50, 1, "tx-aml-pending", "ETH_KEY", "0.011", "USDT",
				"BSC", 1, "COMPLETED", "CONFIRMED", 5, 99999, "0xhash", "KYT_PENDING",
				"0xfrom", "0xto", "0xtxhash"))
	mock.ExpectCommit()

	r := NewRepository(db)
	tx, _ := r.BeginTx(context.Background())
	dep, err := r.LockOneAmlPending(context.Background(), tx, 0)
	if err != nil {
		t.Fatalf("expected deposit, got error: %v", err)
	}
	if dep.ID != 50 {
		t.Errorf("expected id=50, got %d", dep.ID)
	}
	if dep.ToAddress != "0xto" || dep.TxHash != "0xtxhash" {
		t.Errorf("expected ToAddress=0xto TxHash=0xtxhash, got %q %q", dep.ToAddress, dep.TxHash)
	}
	_ = tx.Commit()
}

func TestLockOneAmlPending_NoRows(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	cols := []string{"id", "user_id", "safeheron_tx_key", "safeheron_coin_key",
		"amount", "asset", "chain_code", "coin_chain_id",
		"safeheron_status", "safeheron_sub_status", "status_rank",
		"block_height", "block_hash", "status",
		"from_address", "to_address", "tx_hash"}

	mock.ExpectBegin()
	mock.ExpectQuery(`aml_risk_level`).WillReturnRows(sqlmock.NewRows(cols))
	mock.ExpectRollback()

	r := NewRepository(db)
	tx, _ := r.BeginTx(context.Background())
	_, err := r.LockOneAmlPending(context.Background(), tx, 0)
	if !errors.Is(err, ErrNoPending) {
		t.Fatalf("expected ErrNoPending, got %v", err)
	}
	_ = tx.Rollback()
}
