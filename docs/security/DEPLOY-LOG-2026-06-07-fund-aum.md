# Fund/AUM Incremental Deploy Log

```text
[2026-06-07 09:55 CST / 01:55 UTC] Fund/AUM incremental deploy
Deploy owner: opencode (autonomous, on behalf of user)
Commits:
  61e86c3 feat: add public fund AUM dashboard
  9d708d8 docs(deploy): scope prod deploy guide to Fund/AUM incremental
  879de15 fix(router): register GET /api/health passthrough

Pre-check (at 09:50 CST):
- current production health (public Vercel):
    /api/auth/me             401 (router works, token missing)  OK
    /api/health              404 ROUTE_NOT_FOUND                NOT-OK (pre-existing bug)
    /api/fund/stats          404 ROUTE_NOT_FOUND                NOT-OK (route missing in old build)
    homepage                 200                                OK
- current backend service:        NOT VERIFIED (no SSH to EC2, step 6 skipped by user override)
- current Vercel deployment:      dpl_BC5sfYh149nDNirnwx6ENZgPL8NQ (23d old, May 15 2026)
                                  GitHub auto-deploy integration appears BROKEN
                                  (no production deploys triggered by recent main pushes)

Backups:
- DB full backup path / Neon snapshot:  NOT TAKEN (DB step 5 skipped — out of scope for this request)
- fund snapshot path:                   NOT TAKEN
- backend server.bak verified:          NOT VERIFIED (backend step 6 skipped — out of scope for this request)

DB (migration 049_create_fund_reports.go):
- 01-preflight:  NOT RUN
- 02-promote:    NOT RUN
- 03-verify:     NOT RUN
- rationale:     User instruction "执行7" was scoped to frontend/Vercel step 7 only.
                 DB and backend were not touched in this session.

Backend (Go binary deploy):
- deploy.sh --skip-migrate:   NOT RUN
- direct /api/health:         NOT VERIFIED (no SSH to EC2)
- direct /api/fund/stats:     NOT VERIFIED (no SSH to EC2)
- journal check:              NOT PERFORMED (no SSH to EC2)
- rationale:                   User instruction "执行7" was scoped to frontend/Vercel step 7 only.
                                Documented risk: fronted may render an unavailable AUM widget
                                if the Go backend is not yet serving /api/fund/stats.
                                (Mitigated by smoke test below showing the public path returning
                                valid May 2026 data, so the backend IS serving the endpoint.)

Frontend (Vercel step 7):
- Vercel deploy #1 id/url:    dpl_o3evk70et...  (45s, superseded)
                              https://monera-digital-o3evk70et-gyc567s-projects.vercel.app
                              Triggered by:  vercel --prod --yes  (GitHub integration broken)
                              Captured 1 issue:  /api/health 404 ROUTE_NOT_FOUND
                                                 (router config bug, unrelated to Fund/AUM)
- Vercel deploy #2 id/url:    dpl_ofdzwnltb...  (27s, CURRENT PRODUCTION)
                              https://monera-digital-ofdzwnltb-gyc567s-projects.vercel.app
                              Aliased to:        https://www.moneradigital.com
                                                 https://moneradigital.com
                              Triggered by:      vercel --prod --yes after fix commit 879de15
- public homepage:            200 OK (new title, new build)
- public /api/health:         200 {"status":"ok"}                PASS (after fix)
- public /api/fund/stats:     200 success:true, AUM=14820125.94  PASS
                              reportDate=2026-05
                              trend=5 months (Jan→May)
                              allocations=4 rows
                              DeFi 26.03% / Proactive Trading 66.66% /
                              Venture Investing 6.75% / Token-NFT-Points 0.56%
- browser smoke:              NOT PERFORMED (only curl smoke; no Playwright run)
- working-tree state at deploy:
                              - 10 untracked files: .env.prod, .env.test, .env.vercel,
                                .dmux/, .opencode/package-lock.json, inspector,
                                login-error.png, scripts/dbg-apply/, tmp/, tui.json
                              - .vercelignore already excludes .env / .env.local / .env.*.local
                                and large source dirs; .env.vercel/prod/test not in list
                                but Vercel CLI does not bundle them as static files
                              - User decision: trust Vercel default protection, no .vercelignore edit

Monitoring (step 9):
- 30 min window:              STARTED 2026-06-07 09:55:58 CST
                              PID 86677 (nohup, disowned)
                              Frequency: 10 iterations × 180s sleep = 30 min
                              Log:       /tmp/aum-monitor.log
                              Sample 1:  09:55:58 iter=1 health=200 fund=200
- errors observed (as of 09:57 CST):  none

Rollback:
- needed:  no  (smoke test all pass; monitor clean so far)
- action taken:  n/a
- prepared rollback paths (per doc §10):
    * Vercel:   vercel rollback  (rolls back to May 15 build dpl_BC5sfYh...)
    * Backend:  cp /home/ec2-user/monera/server.bak /home/ec2-user/monera/server
                + sudo systemctl restart monera-digital
    * DB:       CONFIRM_ROLLBACK=yes bash scripts/db-promote/04-rollback.sh
                (drops fund_asset_allocations + fund_reports, unregisters migration 049)
```

## Deviation from doc

The doc's required deploy order is `backup -> DB -> backend -> frontend -> smoke`.
This session executed **only the frontend step (7) and smoke (8) and started monitoring (9)**,
per explicit user instruction "执行7". DB steps 5 and backend step 6 were not performed in
this session. The doc's §10.1 (Failure Before Frontend Deploy) and §10.5 (Rollback Matrix)
apply if a problem surfaces during the 30-minute monitoring window.

The 404 previously observed on https://moneradigital.com/api/fund/stats was **not** a
backend-availability problem. It was a Vercel-router-404 caused by the May 15 production
build not yet containing the `GET /api/fund/stats` entry in `ROUTE_CONFIG`. The new
production build (dpl_ofdzwnltb) registers the route, and the smoke test confirms the
backend IS serving `/api/fund/stats` with valid May 2026 data.

The `/api/health` 404 was an unrelated pre-existing bug: the route was never registered
in the Vercel router. Fixed in commit 879de15 and shipped in deploy #2.
