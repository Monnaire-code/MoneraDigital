# MoneraDigital — H-1 + H-2 Migration Notes (Schema Tightening)

**Audit reference:** H-1 (amount columns as VARCHAR), H-2 (missing foreign keys)
**Date applied:** 2026-06-05
**Migrations:** 047 (`NormalizeAmountTypes`), 048 (`AddMissingForeignKeys`)
**Owner:** Backend / Platform

This document explains what changed, why, and the operational model
for two related schema-tightening migrations.

---

## 1. What changed

### 1.1 Migration 047 — `NormalizeAmountTypes`

Two VARCHAR amount columns become `NUMERIC(32, 8)`:

| Table | Column | Before | After |
|---|---|---|---|
| `deposits` | `amount` | `VARCHAR(65)` | `NUMERIC(32, 8)` |
| `coin_chains` | `min_deposit_amount` | `VARCHAR(64)` | `NUMERIC(32, 8)` |

### 1.2 Migration 048 — `AddMissingForeignKeys`

Three previously-orphaned foreign keys:

| Child column | Parent | ON DELETE |
|---|---|---|
| `withdrawal_verification.withdrawal_order_id` | `withdrawal_order(id)` | `NO ACTION` (default) |
| `withdrawal_freeze_log.order_id` | `withdrawal_order(id)` | `SET NULL` |
| `address_pool.assigned_user_id` | `users(id)` | `SET NULL` |

Each `ADD CONSTRAINT` is guarded by a `pg_constraint` precheck so
re-running the migration is a safe no-op.

### 1.3 Go application code adjustments

The Go application reads `deposits.amount` and `coin_chains.min_deposit_amount`
into `string` fields (see `internal/wallet/deposit/repository.go::DepositRow.Amount`
and `internal/wallet/config/models.go::CoinChain.MinDepositAmount`). PostgreSQL's
`NUMERIC → text` output format is a clean string, so **no scan-side changes
are needed**.

The two write paths that touch `deposits.amount` were updated to add
an explicit `::numeric` cast on the parameter:

- `internal/wallet/deposit/repository.go::UpsertDeposit` — production
  insert path.
- `internal/repository/postgres/deposit.go::Create` — legacy
  depositAddressService insert path.

The implicit `text → numeric` cast in PG's assignment context would
work, but the explicit cast makes the type contract obvious at the call
site and is robust against future PG versions tightening implicit-cast
rules.

`coin_chains.min_deposit_amount` is only set by migration 015's seed
(one-time); no runtime Go code writes to it. The 015 seed runs BEFORE
047, so the existing seed SQL is unaffected.

## 2. Why we did NOT change the precision to `NUMERIC(65, 30)`

Other places in the schema already use `NUMERIC(65, 30)` (notably
`account.balance`, `account_journal.amount`, `wealth_order.amount`).
We chose `NUMERIC(32, 8)` for the two columns we touched because:

- 8 decimal places is enough for any real-world crypto amount
  (satoshi = 1e-8 BTC, wei = 1e-18 ETH — 18 decimals would need
  NUMERIC(38, 18) to be safe, but ETH is tracked via wei elsewhere
  in the schema, not in `deposits.amount`).
- 32 total digits is enough for the largest plausible single deposit
  (~9.2e23, which is over a billion times the global GDP in USDT).
- 32,8 keeps the column size compact (about 16 bytes plus the
  numeric header, vs ~32 bytes for 65,30).
- A future migration to `NUMERIC(38, 18)` for wei-level precision
  remains possible; the precision bump is non-breaking.

If the team prefers to align with `account.balance` (65, 30), the
subsequent migration is one ALTER COLUMN and is non-destructive.

## 3. Why NO ACTION vs CASCADE for the FKs

| Table | ON DELETE | Why |
|---|---|---|
| `withdrawal_verification` | `NO ACTION` | Verifications are tightly coupled to their order. Deleting an order with verifications should fail loudly, not silently lose the audit trail. The application should not be hard-deleting `withdrawal_order` rows. |
| `withdrawal_freeze_log` | `SET NULL` | Freeze logs are an audit record. If an order is purged, the log entry should survive with the FK nulled, preserving "this freeze happened" as a historical fact. |
| `address_pool.assigned_user_id` | `SET NULL` | The address pool is meant to be re-assignable. If a user is hard-deleted, the address returns to the available pool; the `status` column handling is app-side. |

The first FK (`withdrawal_verification`) is the only one that introduces
a hard delete-protection. It is a **bug fix**: today's schema allows
deleting a withdrawal_order that has verifications, silently orphaning
the audit trail. The new FK makes that impossible.

## 4. Pre-checks and how to recover from a stuck migration

Both 047 and 048 abort the transaction with a clear error message if
the pre-check finds an issue:

### 047 (amount normalization)
```
H-1: cannot migrate deposits.amount — N rows are not numeric literals
H-1: cannot migrate coin_chains.min_deposit_amount — N rows are not numeric literals
```

To recover:
1. Identify the offending rows:
   ```sql
   SELECT id, amount FROM deposits WHERE amount !~ '^-?[0-9]+(\.[0-9]+)?$' LIMIT 50;
   ```
2. Decide what to do with them: most commonly they are placeholder
   strings (e.g. `"TBD"`, `"--"`). The safest path is to UPDATE them
   to `NULL` or `0` after manual review, then re-run the migration.
3. Re-run: `DATABASE_URL=... go run ./cmd/migrate`

### 048 (FK addition)
```
H-2: cannot add FK — N withdrawal_verification rows have no matching withdrawal_order
H-2: cannot add FK — N withdrawal_freeze_log rows have no matching withdrawal_order
H-2: cannot add FK — N address_pool rows have no matching users
```

To recover:
1. Identify orphans:
   ```sql
   SELECT wv.id, wv.withdrawal_order_id
     FROM withdrawal_verification wv
    WHERE wv.withdrawal_order_id IS NOT NULL
      AND NOT EXISTS (SELECT 1 FROM withdrawal_order wo WHERE wo.id = wv.withdrawal_order_id);
   ```
2. Decide: most orphans in withdrawal_verification and
   withdrawal_freeze_log are the result of past order-table cleanups
   that didn't cascade. The safest path is `DELETE FROM
   withdrawal_verification WHERE id IN (...)` after manual review, then
   re-run.
3. For `address_pool.assigned_user_id` orphans, UPDATE them to NULL
   (the address becomes available again).

## 5. Operational model

Both migrations are inserted into the registry and applied by the
rebuilt Go runner (see `docs/security/MIGRATION-NOTES.md`). They are
idempotent:

- **047**: The pre-check queries return 0 rows on a re-run if the
  data was already converted, and `ALTER COLUMN ... TYPE NUMERIC` is
  a no-op on an already-NUMERIC column.
- **048**: Each `ADD CONSTRAINT` is wrapped in a `pg_constraint` precheck
  that makes the `ADD CONSTRAINT` a no-op if the constraint already
  exists.

Apply on production:
```bash
DATABASE_URL=... go run ./cmd/migrate -dry-run   # verify pending
DATABASE_URL=... go run ./cmd/migrate            # apply
```

If the migrator exits with the pre-check error, follow §4 above.

## 6. Tests

`internal/migration/migrations/migrations_test.go` now exercises the
interface, version, and description of both new migrations, and the
unified `TestMigrationOrder` table-test runs 18 sub-tests covering
every registered migration (including 047 and 048).

The application-side tests in `internal/wallet/deposit/` and
`internal/repository/postgres/` continue to pass without modification:
sqlmock matches the modified INSERT by regex and the new `::numeric`
cast is just a substring that satisfies the existing `INSERT INTO
deposits.*VALUES.*\$3` pattern.

## 7. Open follow-ups (not blocking close)

- **`deposits.amount` precision**: if wei-level precision is needed in
  the future, a follow-up migration to `NUMERIC(38, 18)` is non-breaking.
- **`deposits.asset` / `deposits.chain` / `withdrawal_order.coin_type`**:
  these are still free-text columns. They could be promoted to enums
  or FKs to `coins` / `chains` in a future pass (audit H-3 ish).
- **`withdrawal_verification.withdrawal_order_id` + `withdrawal_order.id`**
  type mismatch: 003 defined the FK column as INTEGER but
  `withdrawal_order.id` is SERIAL (also INTEGER — no mismatch, but
  worth noting that future migration to BIGINT ids would need a
  coordinated cascade).
- **`account.coin_type` not null but free-text**: the column was
  nominally NOT NULL but unconstrained, so `''` slipped in. A future
  pass should add a check constraint or FK to `coins.symbol`.
