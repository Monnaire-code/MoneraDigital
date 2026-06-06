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

The migrated audit reported prod schema is applied by hand via `psql`.
The cleanest way to reconcile production with the rebuilt Go registry:

```bash
# 1. From your local dev box, with the new (rotated) DATABASE_URL:
DATABASE_URL="..." go run ./cmd/migrate -dry-run

# 2. Output shows every registered migration with its applied status.
#    Versions that say "pending" still need to be applied. Versions
#    that say "applied" are already in `migrations` table.

# 3. To apply only the pending ones (idempotent on already-applied):
DATABASE_URL="..." go run ./cmd/migrate
```

Expected behavior on a production database that was applied by hand
without ever writing to `migrations`:

- **001-005, 007-016** should all show as "pending". Running the
  migrator applies them. The DDL uses `IF NOT EXISTS` / `IF NOT
  EXISTS` on every relevant clause, so the apply is safe on a
  production DB that already has these tables/columns.

  In particular:
  - **016** does data-seeding for `fund_reports` and
    `fund_asset_allocations`. Re-running the migrator will not
    re-seed (it uses `ON CONFLICT (report_date) DO NOTHING` /
    `ON CONFLICT (report_id, category) DO NOTHING`).
  - **015** also seeds `chains` / `coins` / `coin_chains`. Same
    `ON CONFLICT DO NOTHING` semantics — safe to re-run.
  - **046** uses `IF NOT EXISTS` everywhere — strict no-op if 00046
    was previously applied by hand.

- **046** will also show as "pending" if 00046 was applied by hand
  (since hand-applied SQL did not write to the `migrations` table).
  Re-running applies the same DDL idempotently and then records 046.

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
