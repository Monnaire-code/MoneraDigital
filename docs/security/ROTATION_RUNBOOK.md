# MoneraDigital — Neon DB Credential Rotation Runbook

**Audit reference:** C-1 (production credential leakage in tracked source + docs)
**Date applied:** 2026-06-05
**Owner:** Platform / Security

This document is the operational procedure for rotating the Neon PostgreSQL
`neondb_owner` role password and propagating the new value to every
deployment surface. It exists because a previous literal credential was
committed to source and docs and must never be re-introduced.

---

## 1. Why this runbook exists

Prior to 2026-06-05, the production Neon owner connection string
(`postgresql://neondb_owner:npg_4zuq7JQNWFDB@ep-bold-cloud-…/neondb`)
was hard-coded in:

- `seed.ts`, `seed.mjs`
- `scripts/cleanup.go` (`DELETE FROM wealth_order` and balance mutations)
- `cmd/add_balance/main.go` (arbitrary balance writes — **CRITICAL**)
- `cmd/delete_orders/main.go`
- `cmd/db_check/main.go`
- `cmd/wealth_test/main.go`
- 6 docs under `docs/archive/`, `docs/静态理财/`, and `docs/静态理财/需求文档MD/`

The string also appears in git history (`git log -p` will surface it),
so the leaked password is treated as **public**. Rotation is the only
mitigation.

After this runbook, all such files read `DATABASE_URL` from the
environment, refuse to run when the env var is missing, and (for
destructive tools) refuse to run when `APP_ENV=production`.

---

## 2. Pre-rotation checklist

- [ ] Notify on-call: rotation causes a brief (~10 s) reconnection blip
      for the backend pool.
- [ ] Confirm the current Neon project: `ep-bold-cloud-adfpuk12-pooler.c-2.us-east-1.aws.neon.tech/neondb`
      (or whatever the current pooler endpoint is — verify in Neon
      dashboard before acting).
- [ ] Capture a `pg_dump --schema-only` of the current schema for
      rollback reference.

## 3. Rotate the password in Neon

1. Open the Neon console → project → **Settings → Reset password**.
2. Copy the new password into your password manager. **Do not paste it
   into chat, issues, or any tracked file.**
3. Verify by connecting with the new password from a non-production
   workstation: `psql "$DATABASE_URL" -c '\conninfo'`.
4. If you use a connection pooler (PgBouncer / Neon pooler), the new
   password propagates automatically once the Neon role is reset.

## 4. Propagate the new password

Update **only** the following surfaces. The new value must never be
written into a tracked source file.

| Surface | How to update | Notes |
|---|---|---|
| **Local dev `.env`** | Manually edit (`.env` is gitignored) | Replace `DATABASE_URL` line; keep other vars intact |
| **Vercel production** | Vercel dashboard → Project → Settings → Environment Variables | Update `DATABASE_URL`; redeploy to pick up |
| **Replit / other PaaS** | PaaS secret store → `DATABASE_URL` | Restart the deployment |
| **Test/CI ephemeral DBs** | Re-create the test DB or rotate per environment | Never share prod creds with CI |
| **Operator laptops** | `~/.env` or shell init script | Re-pull from password manager |

After each surface, restart the relevant service so the new DSN is
loaded (the Go server reads it at boot; the Vercel serverless functions
read it per request).

## 5. Post-rotation verification

- [ ] `curl https://api.moneradigital.com/api/fund/stats` returns
      200 (or expected 404/200 with a real report).
- [ ] `psql "$DATABASE_URL" -c 'SELECT 1'` succeeds from a
      production-bastion host.
- [ ] No service logs show auth errors.
- [ ] The `internal/middleware/rate_limit.go` request counter still
      increments (sanity check the pool is healthy).
- [ ] No automated secret-scan alerts (see §6) are firing in CI.

## 6. Detection: how future leaks are caught

Three layers of guard are wired into the repo as of this rotation:

1. **Pre-commit hook** (`.git/hooks/pre-commit`):
   `bash scripts/install-hooks.sh` once per clone.
2. **CI workflow** (`.github/workflows/secret-scan.yml`):
   runs `bash scripts/check-secrets.sh` on every push and PR to
   `main` / `master` / `develop`.
3. **Manual / scheduled:**
   `bash scripts/check-secrets.sh` from the repo root.

The guard greps for:

- The literal rotated password `npg_4zuq7JQNWFDB` (and any future
  rotation that follows the same pattern in the script's
  `ROTATED_LITERALS` array).
- The literal Neon pooler hostnames `ep-bold-cloud-adfpuk12-pooler` and
  `ep-weathered-mouse-adjd3txp-pooler`.
- Any `postgres://user:password@host` URL that does not contain a
  `[REDACTED-*]` placeholder.

If the guard fails, the offending file path is printed and the
process exits non-zero. Add new patterns to `ROTATED_LITERALS` /
`SKIP_PATHS` in the script as the secret-rotation landscape evolves.

## 7. If a leak is detected post-rotation

1. **Do not** revert the offending commit. The leaked value is already
   public; reverting does not un-leak it.
2. Go straight to §3 and rotate again (treat the guard finding as
   proof the new password is now also exposed).
3. Open an incident ticket; include the file path, the commit SHA,
   and the rotation timestamp.
4. Audit git history for any past commits that re-introduced the
   literal (the guard catches the working tree but not history).

## 8. What NOT to do

- ❌ Do not paste a new production DSN into a tracked `.md`, `.ts`,
  `.go`, `.sql`, or `.yml` file — even temporarily. The guard will
  block the commit, and the value will leak the moment the file is
  staged.
- ❌ Do not commit `.env` even if it is "obviously" gitignored —
  defense in depth: someone may fork and drop the gitignore.
- ❌ Do not store the new password in Slack/Discord/email subject
  lines. Use the password manager's share-vault or a 1Password /
  Bitwarden shared item.
- ❌ Do not modify `scripts/check-secrets.sh` to silence a finding
  without understanding why it fired.

## 9. Owner accountability

- The SRE on-call owns the next rotation and updates this runbook with
  any new tooling (e.g., switching from Neon to a self-managed cluster
  will require rewriting §3 and §6).
- Any change to `scripts/check-secrets.sh` requires a second reviewer
  and must be recorded in `CHANGELOG.md` with the rationale.
