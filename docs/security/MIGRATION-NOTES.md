# MoneraDigital — Database Migration System (C-2 Rebuild Notes)

**Audit reference:** C-2 (two parallel migration tracking tables, dead runner)
**Date applied:** 2026-06-05
**Owner:** Platform / Backend

This document is the operational handbook for the rebuilt Go migration
runner. It explains what changed, why, and how to verify production state
going forward.

---

## 1. What was wrong

Before 2026-06-05, the migration system was in a fully-dead state:

| Component | Symptom | Root cause |
|---|---|---|
| `cmd/migrate/main.go` | `go build` reported "build constraints exclude all Go files" | File had `//go:build ignore` |
| `internal/migration/migrations/00046.sql` | Never executed | Runner was dead |
| `internal/migration/migrations/00047.sql` | Never executed, and would mass-reset ACTIVE users if it ever ran | Runner was dead + the file was destructive |
| 9 of 16 Go migration files | Never reached the `migrations` tracking table | The `registerMigrations` list in `cmd/migrate/main.go` only included 6 of 16 (007, 010, 011, 013, 015, 016) |
| `migrations` table | Tracked whatever was registered, so production was inconsistent with source | Same as above |
| `schema_migrations` table | Created by 00046.sql, never used by anything | Dead parallel tracking table |
| `cmd/server/main.go` | Did not call any migration function | Migrations were meant to be a separate step |

In short: **no Go code path actually applied any migration to production**.
Production schema was (per operator) applied by hand via `psql` against the
.SQL files. The Go migration files were aspirational documentation.

## 2. What changed

### 2.1 Runner re-enabled and registered

`cmd/migrate/main.go`:

- Removed the `//go:build ignore` and `// +build ignore` build tags. The
  binary now builds and runs.
- Registered **all 16** Go migrations (the previous list had 6).
- Added `-dry-run` and `-rollback` flags for safer ops.
- All registration is in one `registerMigrations` function so adding a
  new migration is one line, not a re-typing exercise.

### 2.2 New Go migration `046_add_pending_status_and_activation_fields.go`

Replaces the legacy `00046_add_pending_status_and_activation_fields.sql`
with a Go-typed equivalent. Same logical effect:

- Adds `PENDING` value to the `user_status` enum (with `pg_enum` precheck
  for idempotency and PG 12+ transaction safety).
- Adds `activation_code`, `activation_attempts`, `activation_expires_at`,
  `activated_at` columns to `users`.
- Creates the `rate_limits` table used by the in-process rate limiter.
- Creates the three supporting indexes.

All DDL uses `IF NOT EXISTS` (or a `pg_enum` precheck for the enum value),
so re-running against a database where 00046 was applied by hand via
`psql` is a safe no-op.

### 2.3 `.sql` files removed from `internal/migration/migrations/`

`00046_*.sql` and `00047_*.sql` are no longer in the migrations
directory. A CI guard (`scripts/check-secrets.sh`) now fails the build
if any `.sql` reappears in that directory.

### 2.4 `00047` moved to `scripts/archive/sql/00047_2026-04-20_reset_existing_users_to_pending.sql`

`00047` was a one-time destructive data fix (mass `UPDATE users SET
status='PENDING' WHERE status='ACTIVE'`) that should never be a regular
migration. It now lives in `scripts/archive/sql/` with a header block
explaining:

- It is NOT a migration.
- It must NEVER be registered in `cmd/migrate/main.go`.
- The historical context (it was originally going to be applied by
  hand on 2026-04-20).

The file is preserved only as a paper trail. If a future need arises to
re-apply a similar reset, the operator must:

1. Take a fresh `pg_dump` first.
2. Confirm intent with the product owner.
3. Run by hand via `psql` with rotated credentials.
4. Never add it to the `registerMigrations` list.

### 2.5 Advisory lock to prevent concurrent runs

`internal/migration/migrator.go::Migrate()` and `Rollback()` now
acquire a session-level `pg_advisory_lock(8675309)` before any DDL or
`migrations` row insert, and release it via `defer`. Two concurrent
invocations (e.g., a deploy step racing a local ops run) now serialise
cleanly instead of interleaving DDL with row inserts.

The lock is automatically released when the process exits. To look up
a stuck run: `SELECT * FROM pg_locks WHERE locktype = 'advisory';`

## 3. How to verify production state

The production schema was historically applied through more than one path, so
its `migrations` provenance may be sparse even when the corresponding tables
and columns already exist. Always inspect the live provenance before choosing
an execution mode:

```bash
# 1. From your local dev box, with the new (rotated) DATABASE_URL:
DATABASE_URL="..." go run ./cmd/migrate -dry-run

# 2. Output shows every registered migration with its applied status.
#    Versions that say "pending" still need to be applied. Versions
#    that say "applied" are already in `migrations` table.

# Do not execute the default all-pending command against a sparse legacy
# provenance history merely because dry-run reports old versions as pending.
```

For the company-fund production cutover, apply only the approved version and
require its immediate predecessor to be recorded before the process opens a
mutation path:

```bash
APP_ENV=production \
EXPECTED_MIGRATION_CEILING=050 \
DATABASE_URL="..." \
go run ./cmd/migrate -exact-version 050
```

Repeat with matching values for each approved successor through `059`. Exact
mode has these invariants:

- Only the requested migration is registered and eligible to run.
- `EXPECTED_MIGRATION_CEILING` must equal `-exact-version`.
- `050` requires `049`; every later version requires its immediate predecessor.
- Historical versions below `050`, unknown versions, and rollback combinations
  are rejected.
- Omitting `-exact-version` preserves the existing all-pending behavior for a
  fresh database or an environment with a fully reconciled Go migration history.

Do not make a sparse legacy history look continuous by inserting synthetic
provenance rows. A migration row claims that exact migration ran; hand-applied
or separately generated schema is not equivalent execution evidence.

## 4. Operational model

The Go runner is intended to be a **deploy-pipeline step**, not a
server-startup hook. The server (`cmd/server/main.go`) does NOT call
`migrator.Migrate()` because:

- Long DDL transactions would extend server cold-start latency.
- A failed migration would block every replica from booting.
- Production deploys already have a "run migration" stage.

If you want a startup-time "auto-migrate" hook, do it in
`cmd/server/main.go` behind a `MIGRATE_ON_BOOT=1` opt-in env var so
local dev can use it but prod stays explicit.

## 5. Adding a new migration (checklist)

1. Create `internal/migration/migrations/0NN_description.go`. Follow the
   existing patterns (transactional step functions if multi-statement).
2. Use idempotent DDL (`IF NOT EXISTS` / `pg_enum` precheck) so re-runs
   are safe.
3. Add `migrations.<StructName>{}` to the `registerMigrations` list in
   `cmd/migrate/main.go`.
4. Add the struct + version to the `migrations` array in
   `internal/migration/migrations/migrations_test.go::TestMigrationOrder`.
5. Run `go test ./internal/migration/migrations/` — the order test will
   fail if you used a duplicate or out-of-order version.
6. Run `bash scripts/check-secrets.sh` — the migration-runner-integrity
   block will fail if you forgot step 3 or accidentally put a `.sql`
   in the directory.
7. Commit. CI will run the same checks on every push.

## 6. Why we did NOT add `.sql` migration support to the runner

We considered extending `migrator.go` to also load and execute `.sql`
files in `internal/migration/migrations/`. We chose not to, because:

- AGENTS.md says "Go 强制所有数据库访问" (Go mandatory for all DB
  access). SQL-only migrations fight that principle.
- Two tracking systems (Go-managed `migrations` table + SQL-managed
  `schema_migrations` table) caused the original C-2 issue. Re-introducing
  `.sql` execution would risk re-introducing the parallel tracking.
- The CI guard keeps the Go-only invariant honest. The escape hatch
  for one-off destructive scripts is `scripts/archive/sql/`, not the
  migrations directory.

If a future need genuinely requires `.sql` migrations (e.g., bulk data
backfill that is easier to write as SQL), the right path is to write
the SQL as a string inside a Go migration's `Up()` method, not to
loosen the directory invariant.

## 7. Open follow-ups (not blocking C-2 close)

- **006 is missing.** The version sequence goes 005 → 007 with no 006
  in the repo. We do not know why; possibly a deleted migration. The
  Go registry has 16 migrations covering the real schema, and the
  numeric gap does not affect correctness. We chose not to backfill a
  fake 006 — git history would surface what was there.
- **`drizzle/monera_complete_schema.sql`** is still a documentation
  file that may drift from the Go migrations. Per audit M-5, the
  authoritative source is the Go migrations. The Drizzle file should
  either be auto-generated from Go or deleted in a future pass.
- **No SQL test coverage of the 046 DDL.** The existing test pattern
  is sqlmock-based (see `safeheron_migrations_test.go`). For 046, the
  DDL is verified by `go run ./cmd/migrate` against a real DB. A
  follow-up PR can add sqlmock tests if the team prefers that style.

## 8. Origin/main merge reconciliation (2026-06-06)

This handbook was originally written before the local main was pulled
against origin/main, which had 15 additional commits. Those commits
included a hotfix migration registered as version **016** (the
`AccountFrozenBalanceDefault` first-deposit fix from commit `240f7c6`).
That collides with the `CreateFundReports` migration that was sitting
untracked in the local working tree as `016_create_fund_reports.go`.

**Resolution: keep both migrations, renumber the create-fund_reports
migration from 016 to 049.**

### 8.1 What changed in the local file layout

- `internal/migration/migrations/016_create_fund_reports.go` (untracked)
  → renamed to `internal/migration/migrations/049_create_fund_reports.go`
  (now tracked). The `Version()` method returns `"049"` instead of
  `"016"`. The `Description()` string was updated to note the rename.
  The Up()/Down() bodies are byte-identical.
- `internal/migration/migrations/016_account_frozen_balance_default.go`
  (new from origin) is now the slot-016 owner. Registered in
  `cmd/migrate/main.go` immediately after `SafeheronPhase1` (015).
- `registerMigrations()` in `cmd/migrate/main.go` now registers 19
  migrations (was 18): the 16 from C-2 + AccountFrozenBalanceDefault
  (016, from origin) + the renamed CreateFundReports (049).
- `migrations_test.go::TestMigrationOrder` has 19 entries with the
  new ordering: 001-005, 007-015, 016, 046, 047, 048, 049.

### 8.2 What changed in the db-promote tooling

The `scripts/db-promote/` toolkit previously targeted migration 016
as "the fund_reports migration", which was the untracked local file.
After the rename, the toolkit targets 049:

- `inspector/main.go::migrationVersion` constant: `"016"` → `"049"`.
  All preflight/verify/rollback/info subcommands use this constant,
  so they now target the renamed migration.
- `inspector/main.go` now also registers
  `migrations.AccountFrozenBalanceDefault` in its apply list. Without
  this, the 016 hotfix would not be applied when an operator runs
  `02-promote.sh` on a fresh DB.
- All five shell scripts (`01-preflight.sh`, `02-promote.sh`,
  `03-verify.sh`, `04-rollback.sh`, `05-snapshot.sh`) had their
  header comments and `echo` strings updated from "016" to "049".
- The two print statements in preflight that hardcoded "migration
  016" / "016 not yet applied" now use `fmt.Printf("...%s...", migrationVersion)`.

### 8.3 Operator impact

A `02-promote.sh` run on a fresh DB now applies 19 migrations instead
of 18. The two new behaviours to be aware of:

1. **`016_account_frozen_balance_default` adds a DEFAULT to
   `accounts.frozen_balance`.** The migration uses `ADD COLUMN
   ... DEFAULT ...` semantics. If the column already exists without
   a DEFAULT (the prod state before this deploy), the migration
   drops the NOT NULL and re-adds the column with the DEFAULT — this
   is intentional from the origin dev branch and matches the
   `ce7a6a9 fix: FindOrCreateAccountForUpdate 缺少 frozen_balance
   导致入账失败` fix.
2. **`049_create_fund_reports` creates the `fund_reports` and
   `fund_asset_allocations` tables** (and seeds 5 monthly rows + the
   May 2026 allocation snapshot). This was previously "migration 016"
   in the local pre-merge working tree. If your DB has the
   `fund_reports` table already (because someone ran the untracked
   file manually with psql), the migration's `CREATE TABLE IF NOT
   EXISTS` is a no-op and the `INSERT ... ON CONFLICT DO NOTHING`
   won't double-seed.

### 8.4 What does NOT change

- The order in which the migrator applies migrations (still
  version-sorted).
- The C-1/C-2/H-1/H-2/404 fix code (those are independent of this
  renumbering).
- The reversibility table in PRODUCTION-DEPLOY-2026-06-05.md §9.1
  (still accurate: 049 is the only "soft-reversible" migration via
  `04-rollback.sh`; 047 is the only truly-irreversible one; everything
  else is idempotent re-run).
- The 404 fix's pre-condition: `/api/fund/stats` returns 404 on
  pre-migration DBs and 200 on post-049 DBs. The fix is at the
  repository layer (`isUndefinedTable` translating 42P01 to
  `ErrFundNotFound`); the migration's version number is irrelevant
  to that mapping.

## 9. Open follow-ups (not blocking C-2 close)
