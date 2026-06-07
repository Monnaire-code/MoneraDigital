# MoneraDigital - Fund/AUM Incremental Production Deploy Guide

**Scope:** Incrementally deploy today's Fund/AUM homepage feature only.

**Feature commit:** `61e86c3 feat: add public fund AUM dashboard`

**Changed surface:**

- DB: migration `049_create_fund_reports.go`
- Go backend: public `GET /api/fund/stats`
- Vercel API router: `GET /api/fund/stats` proxy route
- Frontend: homepage AUM card, fund performance section, i18n text
- Ops docs/scripts: fund DB promote helpers and frontend smoke scripts

**Required deploy order:** backup -> DB -> backend -> frontend -> smoke.

Do not reverse the order. The frontend calls `/api/fund/stats`; the Go
backend and `fund_reports` tables must be ready before the Vercel build is
promoted to production.

---

## 0. TL;DR

Run this as an incremental deploy. Do not rerun unrelated historical audit
runbooks.

1. Confirm production is currently healthy.
2. Take backups:
   - Full DB dump, or at minimum a fund table snapshot if the tables exist.
   - Existing backend binary backup is handled by `scripts/deploy.sh`.
   - Record the current Vercel production deployment URL for rollback.
3. Apply only the Fund/AUM DB migration using `scripts/db-promote`.
4. Deploy the backend binary using `scripts/deploy.sh --skip-migrate`.
5. Deploy the frontend/Vercel API router from the repo root using `vercel --prod`.
6. Verify direct backend and public Vercel paths.
7. Monitor for 30 minutes.

Canonical commands:

```bash
# DB preflight
bash scripts/db-promote/01-preflight.sh

# Optional narrow fund-table snapshot
bash scripts/db-promote/05-snapshot.sh

# Apply Fund/AUM migration 049 only
bash scripts/db-promote/02-promote.sh

# Verify DB and backend API, once backend is up
API_URL=http://localhost:8081 bash scripts/db-promote/03-verify.sh

# Backend binary deploy after DB migration
bash scripts/deploy.sh --skip-migrate

# Frontend/Vercel deploy from repo root
vercel --prod
```

Important boundaries:

- `scripts/db-promote/*` is a Fund/AUM migration toolkit. It is not the
  full Go migration runner.
- Full Go migration runner is `cmd/migrate/main.go`, but this incremental
  deploy should not need it unless production is missing older required
  migrations.
- `scripts/deploy-remote.sh --frontend` is not recommended for this deploy:
  it packages a temporary frontend directory but does not copy `api/`, so it
  can miss the Vercel API router change.

---

## 1. Pre-Deploy Checks

### 1.1 Confirm Target Commit

On the deploy host:

```bash
cd /home/ec2-user/monera
git fetch origin
git checkout main
git pull origin main
git log --oneline -5
```

Expected: the log includes:

```text
61e86c3 feat: add public fund AUM dashboard
```

If the source checkout lives somewhere else, check `scripts/deploy.sh` first.
It currently hard-codes:

```text
APP_DIR=/home/ec2-user/monera
SERVICE_NAME=monera-digital
BINARY_NAME=server
MIGRATE_NAME=monera-migrate
```

The deploy script must be run from a checkout that has `go.mod`.

### 1.2 Confirm Current Production Health

Before changing anything:

```bash
curl -s -w " HTTP %{http_code}\n" -o /tmp/pre-home.html https://moneradigital.com/
curl -s -w " HTTP %{http_code}\n" http://localhost:8081/api/health
sudo systemctl status monera-digital --no-pager
sudo journalctl -u monera-digital -n 80 --no-pager
```

Expected:

- homepage returns `200`
- backend health returns `200`
- systemd service is active
- recent logs have no `panic`, `fatal`, or repeated 5xx errors

If production is already degraded, stop and record the baseline before
deploying.

### 1.3 Confirm Environment Variables

Backend host:

```bash
test -f /home/ec2-user/monera/.env
chmod 600 /home/ec2-user/monera/.env
grep '^PORT=' /home/ec2-user/monera/.env || true
grep '^DATABASE_URL=' /home/ec2-user/monera/.env >/dev/null
```

Vercel:

- `BACKEND_URL` must point to the production Go backend.
- Do not add database credentials to frontend code.
- Do not commit `.env`, `.env.prod`, `.env.test`, or `.env.vercel`.

`api/[...route].ts` reads `BACKEND_URL` at module import time, so after
changing it in Vercel you must redeploy the Vercel app.

---

## 2. Backup Plan

Backups are mandatory before the first production mutation.

### 2.1 Backend Binary Backup

`scripts/deploy.sh` automatically backs up the current backend binary before
building the new one:

```text
/home/ec2-user/monera/server.bak
```

Verify that backup exists immediately after the deploy script reaches the
build step:

```bash
ls -la /home/ec2-user/monera/server.bak
```

Note: the current script backs up `server` only. It does not back up
`monera-migrate`.

### 2.2 Database Backup

Preferred full DB backup:

```bash
pg_dump "$PROD_DSN" -Fc > /tmp/prod-full-before-aum-$(date +%Y%m%d-%H%M%S).dump
ls -lh /tmp/prod-full-before-aum-*.dump
```

If `pg_dump` is not available on the host, take a Neon branch/snapshot from
the Neon console before continuing.

Narrow Fund/AUM snapshot:

```bash
bash scripts/db-promote/05-snapshot.sh
# Type: snapshot
```

This only backs up `fund_reports` and `fund_asset_allocations`. It is useful
for this feature but is not a substitute for a full DB backup if broader
schema risk is suspected.

### 2.3 Frontend Rollback Reference

Before Vercel deploy, record the current production deployment:

```bash
vercel ls monera-digital
```

Or use the Vercel dashboard:

```text
Project -> Deployments -> current Production deployment
```

Keep the previous good deployment URL. Rollback is done by promoting that
deployment back to production.

---

## 3. Local Verification Before Production

Run from the repo root before touching production:

```bash
npm test -- src/lib/fund-service.test.ts src/lib/locale-format.test.ts api/__route__.test.ts --exclude '.dmux/**'
GOCACHE=/private/tmp/monera-go-build-cache go test ./internal/services -run 'TestFundService|TestWealthService_GetAssets'
GOCACHE=/private/tmp/monera-go-build-cache go test ./internal/middleware ./internal/repository/postgres ./internal/migration/migrations
```

Expected: all three commands pass.

Do not use the full `go test ./internal/services` result as the deploy gate
unless existing unrelated auth/test-env failures have been cleaned up. This
runbook gates only the changed Fund/AUM surface.

Optional build checks:

```bash
npm run build
go build -o /tmp/monera-server ./cmd/server/main.go
go build -o /tmp/monera-migrate ./cmd/migrate/
```

---

## 4. Staging or Dev DB Gate

Use a Neon branch or a known non-prod DB before production.

```bash
ENV_FILE=/path/to/staging.env bash scripts/db-promote/01-preflight.sh
ENV_FILE=/path/to/staging.env bash scripts/db-promote/02-promote.sh
# Type: yes
ENV_FILE=/path/to/staging.env bash scripts/db-promote/03-verify.sh
```

If you have a staging backend:

```bash
API_URL=https://staging-backend.example.com bash scripts/db-promote/03-verify.sh --env-file /path/to/staging.env
```

Expected:

- migration `049` is applied or reported as already applied
- `fund_reports` has 5 rows
- `fund_asset_allocations` has 4 rows
- allocation `pct` sum is exactly `1.0`
- `GET /api/fund/stats` returns `200` with May 2026 data

If staging fails, do not touch production.

---

## 5. Production DB Deploy

This step mutates production DB state. Run it only after backups and staging
verification.

### 5.1 Preflight

```bash
cd /home/ec2-user/monera
bash scripts/db-promote/01-preflight.sh
```

Expected:

- `DATABASE_URL` is set
- DB ping succeeds
- `public.migrations` exists
- source migration `internal/migration/migrations/049_create_fund_reports.go`
  exists

If preflight fails, stop.

### 5.2 Apply Migration 049

```bash
bash scripts/db-promote/02-promote.sh
# Type: yes
```

Expected:

- migration runner completes
- `049` is inserted into the `migrations` table, or is already applied
- no `FATAL migrate` output

Important: `02-promote.sh` does not automatically run `01-preflight.sh`.
Run preflight yourself first.

### 5.3 Verify DB

```bash
bash scripts/db-promote/03-verify.sh
```

Expected:

- `VERIFY: PASS`
- `fund_reports` row count = 5
- `fund_asset_allocations` row count = 4
- allocation sum = 1.0000

Manual checks if needed:

```bash
psql "$PROD_DSN" -c "SELECT version, name, executed_at FROM migrations WHERE version='049'"
psql "$PROD_DSN" -c "SELECT to_char(report_date,'YYYY-MM') AS month, total_aum FROM fund_reports ORDER BY report_date"
psql "$PROD_DSN" -c "SELECT category, amount, pct FROM fund_asset_allocations ORDER BY sort_order"
```

---

## 6. Backend Deploy

Deploy backend after the DB is ready.

```bash
cd /home/ec2-user/monera
git status --short
bash scripts/deploy.sh --skip-migrate
```

Why `--skip-migrate`:

- production DB migration `049` was already applied in section 5
- this keeps backend deploy logs focused on build, binary swap, systemd,
  and health check
- it avoids rerunning unrelated migrations during this incremental deploy

Expected from `deploy.sh`:

- builds `server`
- builds `monera-migrate`
- backs up old `/home/ec2-user/monera/server` to `server.bak`
- stops `monera-digital`
- copies the new binary
- starts `monera-digital`
- health check passes at `/api/health`

Verify:

```bash
curl -s -w " HTTP %{http_code}\n" http://localhost:8081/api/health
curl -s -w " HTTP %{http_code}\n" http://localhost:8081/api/fund/stats | head -c 800
sudo journalctl -u monera-digital -n 120 --no-pager
```

Expected:

- `/api/health` returns `200`
- `/api/fund/stats` returns `200`
- response includes `success: true`
- logs have no `panic`, `fatal`, or repeated errors

---

## 7. Frontend and Vercel API Router Deploy

Deploy frontend only after backend direct checks pass.

This feature includes frontend and Vercel API router changes. It is not
optional.

Recommended command from repo root:

```bash
vercel --prod
```

If using GitHub integration instead:

```bash
git push origin main
```

Then verify the Vercel deployment completed in the dashboard.

Do not use `bash scripts/deploy-remote.sh --frontend` for this specific
deploy unless that script has been updated to package the `api/` directory.
The current script copies `src`, `public`, and config files, but not `api/`.

Verify Vercel environment:

```text
BACKEND_URL=https://<production-backend-host>
```

---

## 8. Full Production Smoke Test

Run after backend and frontend are both deployed.

```bash
echo "[A] Homepage"
curl -s -w " HTTP %{http_code}\n" -o /tmp/aum-home.html --max-time 10 https://moneradigital.com/

echo "[B] Health through Vercel API router"
curl -s -w " HTTP %{http_code}\n" --max-time 10 https://moneradigital.com/api/health

echo "[C] Fund stats through Vercel API router"
curl -s --max-time 10 https://moneradigital.com/api/fund/stats | tee /tmp/aum-fund-stats.json
```

Expected:

- homepage returns `200`
- `/api/health` returns `200`
- `/api/fund/stats` returns `200`
- `/tmp/aum-fund-stats.json` has `success: true`
- current AUM is `14820125.94`
- report date is `2026-05`
- trend has 5 months
- allocations have 4 rows

Browser smoke:

- Open `https://moneradigital.com/`
- Homepage hero shows current AUM, not a permanent loading skeleton
- Fund performance section renders the trend chart and allocation chart
- Chinese locale renders month/date text correctly
- No console error loop

Optional local Playwright smoke after Vercel:

```bash
BASE_URL=https://moneradigital.com node scripts/verify-frontend.mjs
BASE_URL=https://moneradigital.com node scripts/verify-frontend-locale.mjs
```

---

## 9. Monitoring Window

Monitor for 30 minutes after Vercel promotion.

On backend host:

```bash
sudo journalctl -u monera-digital -f
```

In another terminal:

```bash
for i in 1 2 3 4 5 6 7 8 9 10; do
  health=$(curl -s -o /dev/null -w "%{http_code}" --max-time 5 https://moneradigital.com/api/health)
  fund=$(curl -s -o /dev/null -w "%{http_code}" --max-time 5 https://moneradigital.com/api/fund/stats)
  echo "$(date +%H:%M:%S) health=$health fund=$fund"
  sleep 180
done
```

Expected:

- health stays `200`
- fund stats stays `200`
- no increasing backend error rate
- no Vercel function errors for `/api/fund/stats`

---

## 10. Rollback Plan

Rollback order depends on where the failure occurs.

### 10.1 Failure Before Frontend Deploy

If DB or backend fails before Vercel deploy:

1. Do not deploy frontend.
2. Restore backend binary if needed.
3. Roll back Fund/AUM DB tables only if the DB change itself is the problem.

### 10.2 Backend Binary Rollback

Fast rollback to previous backend binary:

```bash
ssh ec2-user@<prod-host>
cd /home/ec2-user/monera
sudo systemctl stop monera-digital
sudo cp /home/ec2-user/monera/server.bak /home/ec2-user/monera/server
sudo chmod +x /home/ec2-user/monera/server
sudo systemctl start monera-digital
sleep 2
curl -s -w " HTTP %{http_code}\n" http://localhost:8081/api/health
sudo journalctl -u monera-digital -n 80 --no-pager
```

Expected: health returns `200`.

If the old backend does not know `/api/fund/stats`, keep the frontend rolled
back too. Otherwise users may see an unavailable AUM widget.

### 10.3 Frontend Rollback

Use Vercel dashboard:

```text
Project -> Deployments -> previous good deployment -> Promote to Production
```

Or Vercel CLI:

```bash
vercel rollback
```

Verify:

```bash
curl -s -w " HTTP %{http_code}\n" -o /tmp/rollback-home.html https://moneradigital.com/
```

### 10.4 DB Rollback for Migration 049

Use this only if the Fund/AUM DB tables or seed data are the source of the
problem.

```bash
CONFIRM_ROLLBACK=yes bash scripts/db-promote/04-rollback.sh
# Type: rollback
```

This drops only:

```text
fund_asset_allocations
fund_reports
```

and unregisters migration:

```text
049
```

It does not roll back any other migration.

If you need to restore data instead of dropping the tables, use the backup
from section 2:

```bash
pg_restore -d "$PROD_DSN" --clean --if-exists /tmp/prod-full-before-aum-*.dump
```

That is a heavier operation and should be treated as a maintenance-window
restore.

### 10.5 Recommended Rollback Matrix

| Symptom | Immediate action | DB rollback? | Frontend rollback? | Backend rollback? |
|---|---|---:|---:|---:|
| DB migration preflight fails | Stop deploy | No | No | No |
| DB migration apply fails | Stop deploy, inspect transaction logs | Usually no | No | No |
| Backend fails to start | Restore `server.bak` | No | No, if frontend not deployed | Yes |
| Direct backend `/api/fund/stats` returns 500 | Check journal, consider backend rollback | Maybe | Do not deploy frontend | Maybe |
| Vercel `/api/fund/stats` fails but direct backend works | Check `BACKEND_URL` and Vercel logs | No | Maybe | No |
| Homepage broken after Vercel deploy | Promote previous Vercel deployment | No | Yes | No |
| AUM data wrong | Roll back frontend first, inspect DB seed | Maybe | Yes | Maybe no |

---

## 11. Deploy Log Template

Fill this out during the deploy.

```text
[YYYY-MM-DD HH:MM TZ] Fund/AUM incremental deploy
Deploy owner:
Commit:

Pre-check:
- current production health:
- current backend service:
- current Vercel deployment:

Backups:
- DB full backup path / Neon snapshot:
- fund snapshot path:
- backend server.bak verified:

DB:
- 01-preflight:
- 02-promote:
- 03-verify:

Backend:
- deploy.sh --skip-migrate:
- direct /api/health:
- direct /api/fund/stats:
- journal check:

Frontend:
- Vercel deploy id/url:
- public homepage:
- public /api/health:
- public /api/fund/stats:
- browser smoke:

Monitoring:
- 30 min window:
- errors observed:

Rollback:
- needed: yes/no
- action taken:
```

---

## 12. Known Caveats

- `scripts/db-promote/README.md` still has some historical `016` wording.
  The current feature migration is `049`.
- `scripts/db-promote/02-promote.sh` is intentionally scoped to Fund/AUM
  deployment support. Do not treat it as a full migration runner.
- `scripts/deploy.sh` backs up the backend `server` binary, not the migrator.
- `scripts/deploy-remote.sh --frontend` does not currently package `api/`.
  Use `vercel --prod` from the repo root for this deploy.
- Leave local untracked files such as `.env.*`, `.dmux/`, `tmp/`, screenshots,
  and local binaries out of the deployment commit and out of Vercel packages.
