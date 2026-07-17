# scripts/db-promote — production DB migration toolkit (016 fund_reports)

> Safe, idempotent, verify-after-apply workflow for promoting the
> fund_reports / fund_asset_allocations schema (migration 016) from
> any environment to another. No third-party dependencies (psql not
> required); only Go (which the project already requires).

## Files

| File | Purpose | Mutates DB? |
|---|---|---|
| `inspector/main.go` | All SQL ops (preflight, apply, verify, rollback, info, snapshot) | (only when `apply` / `rollback` / `snapshot` invoked) |
| `01-preflight.sh` | Read-only pre-deploy check | No |
| `02-promote.sh` | Apply migration (idempotent, with `yes` confirmation) | Yes (or no-op) |
| `03-verify.sh` | Post-apply schema + data + API smoke | No (API GET is read) |
| `04-rollback.sh` | Drop tables + unregister migration | Yes (destructive, gated) |
| `05-snapshot.sh` | `pg_dump` of the two fund tables to `/tmp/fund-snapshots/` | No (read-only) |

## Environment file

The Go inspector loads `.env` from the project root. The repo has two
`.env` files with **split responsabilités**:

| File | Audience | Contents |
|---|---|---|
| `.env` | **Go backend** (this inspector uses it) | `DATABASE_URL`, JWT secret, encryption key, etc. |
| `.env.prod` | **Vercel** (frontend deployment) | `ENCRYPTION_KEY`, `JWT_SECRET`, `UPSTASH_REDIS_*`, `VERCEL_OIDC_TOKEN` |

The inspector **only** uses `.env`. On a prod EC2 host the
`/home/ec2-user/monera/.env` file is the relevant one — pass it via
`--env-file` or `ENV_FILE` if it's not at the project root.

## Usage — the ONE recommended path

Promote DB schema first (so the binary can immediately use it on
startup), then deploy the binary. This avoids a window where the
binary is up but the table is missing.

```bash
# === STAGE 1: DB (3 steps) ===
cd /home/ec2-user/monera && git pull

# 1.1 Read-only pre-check (no harm)
bash scripts/db-promote/01-preflight.sh

# 1.2 Apply migration (interactive yes)
bash scripts/db-promote/02-promote.sh
#   Type 'yes' to confirm.

# 1.3 Verify (DB + API). If backend not yet up, skip API_URL.
API_URL=http://localhost:8081 bash scripts/db-promote/03-verify.sh
#   (or omit API_URL for DB-only verify)

# === STAGE 2: Binary ===
# Use --skip-migrate because 02-promote already ran the migrator.
bash scripts/deploy.sh --skip-migrate

# === STAGE 3: Full-stack sanity ===
API_URL=http://localhost:8081 bash scripts/db-promote/03-verify.sh
```

The `--skip-migrate` is critical — without it, `deploy.sh` will run
the migrator a second time. That's harmless (idempotent) but wasted
work and double the migrator log output.

### Override env file (when `.env` is at a different path)

```bash
bash scripts/db-promote/01-preflight.sh --env-file /opt/monera/.env
ENV_FILE=/opt/monera/.env bash scripts/db-promote/02-promote.sh
```

### Inspect current state (read-only)

```bash
go run scripts/db-promote/inspector/main.go info
```

### Take a backup snapshot (recommended before rollback)

```bash
# Optional: snapshot the two tables to /tmp/fund-snapshots/
bash scripts/db-promote/05-snapshot.sh
#   Type 'snapshot' to confirm.
```

### Rollback (destructive)

```bash
CONFIRM_ROLLBACK=yes bash scripts/db-promote/04-rollback.sh
#   Type 'rollback' to confirm.
```

This drops both tables and unregisters migration 016. Safe to re-apply
afterwards (the migration uses `CREATE TABLE IF NOT EXISTS` + `INSERT ...
ON CONFLICT DO NOTHING`).

## Exit codes

| Script | Code | Meaning |
|---|---|---|
| `01-preflight` | 0 | Ready to apply (or already applied — no-op) |
| | 1 | One or more preconditions failed; DO NOT apply |
| `02-promote` | 0 | Migration ran (or was a no-op) |
| | 1 | Migrator failed |
| `03-verify` | 0 | All DB + API checks pass |
| | 1 | Any check failed |
| `04-rollback` | 0 | Rollback succeeded |
| | 1 | Rollback failed |
| | 2 | `CONFIRM_ROLLBACK=yes` missing |
| `05-snapshot` | 0 | Snapshot written to disk |
| | 1 | pg_dump failed or disk error |
| | 2 | User aborted at confirmation prompt |

## Coupling with backend deploy

The DB change and the backend binary change are **independent** but
**logically coupled**. The ONE recommended path above (DB first via
`db-promote` toolkit, then binary via `deploy.sh --skip-migrate`)
avoids a 500 window on `/api/fund/stats` during the deploy.

The legacy path (use only `scripts/deploy.sh` without `--skip-migrate`)
also works — the migrate binary built into the deploy will run 016
on its own. But it conflates two concerns and runs the migrator twice
if combined with `02-promote.sh`. Don't mix them.

## Safety properties (idempotency proof)

- `01-preflight.sh` is **purely read-only** — cannot mutate state
- `02-promote.sh` calls the Go Migrator wrapped in a transaction:
  - Skips already-applied versions (lookup in `migrations` table)
  - On re-apply of 016, the migration itself uses
    `CREATE TABLE IF NOT EXISTS` + `INSERT ... ON CONFLICT DO NOTHING`
  - The seed (5 monthly reports + 4 May allocations) is therefore
    safe to re-run against a partially-applied DB
  - If Up() returns an error, the entire transaction is rolled back
- `03-verify.sh` is read-only against the DB; the API smoke is a
  single GET, not a mutation
- `04-rollback.sh` requires BOTH `CONFIRM_ROLLBACK=yes` env var AND
  the interactive `rollback` typing — two independent gates
- `05-snapshot.sh` is a read-only `pg_dump`

## Concurrency warning

**Do not run `02-promote.sh` (or `04-rollback.sh`) concurrently from
two terminals.** The `migrations.version` column is `UNIQUE`, so two
concurrent runs will cause the second one to fail with
`duplicate key value violates unique constraint "migrations_version_key"`.

The DDL itself is idempotent (so the first run is sufficient), but the
second will report failure even though the end state is correct. If
this happens, the verify check is the source of truth — re-run
`03-verify.sh`. If it passes, you're done.

If concurrent runs are likely (CI, multiple ops), add a PostgreSQL
advisory lock to the inspector's `apply` subcommand. Not done by
default because the project doesn't already use advisory locks
elsewhere — keep it simple unless you need it.

## Verified behavior (dev DB, 2026-06-04, post-audit-fixes)

The full cycle was exercised on the dev Neon DB after the audit
remediations:

| Step | Result |
|---|---|
| `01-preflight` (clean state) | PASS (info: 016 not yet applied) |
| `02-promote` (apply) | OK (5 reports + 4 allocations inserted, sum pct = 1.0000) |
| `03-verify` | PASS (schema + data + API smoke HTTP 200, sum_pct=1.0000) |
| `05-snapshot` | OK (dump written to /tmp/fund-snapshots/fund-{ts}.dump) |
| `04-rollback` (with `CONFIRM_ROLLBACK=yes`) | OK (tables dropped, 016 unregistered) |
| `01-preflight` (post-rollback) | PASS (info: 016 not yet applied) |
| `02-promote` (re-apply) | OK (5 reports + 4 allocations re-inserted, sum pct = 1.0000) |
| `03-verify` (final) | PASS |

Zero side effects beyond the migration itself; the 5 rows of trend +
4 rows of allocations are byte-identical after each round-trip
(excluding auto-incrementing `id` PKs, which is expected).

## When NOT to use this

- **Full backend deploy** (build + copy + migrate + systemd): use
  `scripts/deploy.sh` instead. It already calls the migrator.
- **Vercel frontend deploy**: `bash scripts/deploy-remote.sh --frontend`.
- **Test env deploy**: use `.github/workflows/deploy-backend-stage.yml`; direct backend deployment now requires an exact full SHA and an explicit release mode as documented in `docs/company-fund-stage-release-control.md`.
- **Other migrations** (007-015): the inspector is hard-coded to 016.
  For other migrations, fall back to `go run cmd/migrate/main.go`
  directly.
