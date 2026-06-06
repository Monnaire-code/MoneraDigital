# MoneraDigital — Production Deployment Guide

**Scope:** Deploys the C-1 (credential rotation), C-2 (migration rebuild),
H-1 (amount types), H-2 (foreign keys), and 404-fix changes to production.

**Date prepared:** 2026-06-05
**Owner of deploy:** Platform / Backend lead
**Required read time:** 20 min
**Estimated deploy window:** 60-90 min (DB first, then code, then verify)

---

## 0. TL;DR

1. **Confirm the Neon DB password is rotated** in your password manager
   (you said it is — verify before continuing).
2. **Update every `.env`** (local dev, EC2 prod, Vercel preview) with the
   new `DATABASE_URL`.
3. **Apply DB migrations on staging first** via
   `bash scripts/db-promote/02-promote.sh` (gated by a `yes` confirmation
   and a preflight check; must be green before prod).
4. **Apply DB migrations on production** via the same script (gated by
   the same `yes` confirmation; idempotent).
5. **Deploy new backend binary to EC2** via
   `bash scripts/deploy.sh --skip-migrate`. This script compiles,
   backs up the old binary, swaps it in, restarts the systemd service,
   and health-checks. **If the migrator fails, it auto-rolls back to
   the previous binary and restarts the service.**
6. **(Optional) Re-deploy frontend to Vercel** — this batch has zero
   frontend changes; redeploy only if you want a fresh Vercel build.
7. **End-to-end smoke + guard script + monitor** for 30 minutes.
8. **Install pre-commit hook** on every developer clone.

**Order matters:** DB first → backend second → frontend third (or
skip). The 404 fix in the new binary means a `/api/fund/stats` hit on
a DB that hasn't been migrated yet will return 404 "No fund report
available yet" (graceful empty state) instead of 500 — so the binary
is safe to deploy before migrations complete, but you should still run
migrations first to avoid the empty-state window for real users.

**Tooling anchors (the canonical scripts, not "go run" hacks):**
- DB apply: `bash scripts/db-promote/02-promote.sh` (yes-gated)
- DB preflight: `bash scripts/db-promote/01-preflight.sh`
- DB verify: `bash scripts/db-promote/03-verify.sh`
- DB rollback: `CONFIRM_ROLLBACK=yes bash scripts/db-promote/04-rollback.sh`
- Backend deploy: `bash scripts/deploy.sh [--skip-migrate]`
- DB snapshot: `bash scripts/db-promote/05-snapshot.sh`

---

## 1. Pre-deployment Checklist

Run this **the day before** the deploy. Every box must be checkable.

### 1.1 Code readiness

- [ ] All 5 audit fixes are committed in a single PR (or have a merge
      target branch agreed on):
      - [ ] C-1: hardcoded credentials removed from `seed.ts`,
            `seed.mjs`, `scripts/cleanup.go`,
            `cmd/{add_balance, delete_orders, db_check, wealth_test}/main.go`
      - [ ] C-1: 9 docs redacted under `docs/`, `cmd/wealth_test/`,
            `openspec/`
      - [ ] C-1: `scripts/check-secrets.sh`,
            `scripts/install-hooks.sh`,
            `.github/workflows/secret-scan.yml`,
            `docs/security/ROTATION_RUNBOOK.md` exist and are tracked
      - [ ] C-2: `cmd/migrate/main.go` no longer has
            `//go:build ignore`; all 16 + new 046/047/048 migrations
            registered in `registerMigrations()`;
            `internal/migration/migrator.go` has `pg_advisory_lock`
      - [ ] C-2: `internal/migration/migrations/046_*.go` exists;
            `00046.sql` and `00047.sql` removed from
            `internal/migration/migrations/`;
            `00047_2026-04-20_*.sql` lives in
            `scripts/archive/sql/`
      - [ ] C-2: `docs/security/MIGRATION-NOTES.md` exists
      - [ ] H-1: `internal/migration/migrations/047_*.go` exists;
            `internal/wallet/deposit/repository.go` and
            `internal/repository/postgres/deposit.go` use
            `$3::numeric`
      - [ ] H-2: `internal/migration/migrations/048_*.go` exists
      - [ ] H-1/H-2: `docs/security/H1-H2-NOTES.md` exists
      - [ ] 404 fix: `internal/repository/postgres/fund_report.go`
            has `isUndefinedTable` helper applied in all 3 query methods
      - [ ] 404 fix:
            `internal/repository/postgres/fund_report_test.go`
            has 6 new tests

### 1.2 Database readiness

- [ ] Neon console → project → **Settings → Reset password** done
      (you said yes on 2026-06-05; verify the timestamp).
- [ ] New password is in the team password manager (1Password /
      Bitwarden / equivalent). Only one or two people need it.
- [ ] The new DSN is constructable:
      `postgresql://neondb_owner:<new-password>@ep-bold-cloud-adfpuk12-pooler.c-2.us-east-1.aws.neon.tech/neondb?sslmode=require&channel_binding=require`
- [ ] An unused staging or dev branch of the Neon project is
      available for the staging-apply dry run.

### 1.3 Infrastructure readiness

- [ ] SSH access to the EC2 backend host (e.g.
      `ec2-user@<host>`).
- [ ] EC2 host has `git`, `go` (matching the local version), and
      `node` available. **`go build` and `systemctl` and `sudo` are
      required** for `scripts/deploy.sh`.
- [ ] The repo is cloned at `/home/ec2-user/monera` (the path
      `scripts/deploy.sh` hard-codes; if your deploy path is
      different, update `APP_DIR` at the top of `deploy.sh` first).
- [ ] The systemd unit name is `monera-digital` (this is what
      `deploy.sh:33` hard-codes; verify with
      `systemctl list-units --type=service | grep monera`).
- [ ] Vercel CLI is installed and authenticated locally for the
      (optional) frontend redeploy.

### 1.4 Team readiness

- [ ] On-call SRE is paged 30 min before deploy start.
- [ ] Slack/Discord deploy channel has a pinned "deploy in progress"
      notice.
- [ ] Rollback owner is identified (different from the deploy owner,
      to avoid a single point of failure).

---

## 2. Local pre-deploy verification

Run this on the dev machine. ~15 min. **Do not skip — it's the
regression net.**

### 2.1 Build

```bash
cd /path/to/MoneraDigital

# Full project build
go build -o /dev/null ./...
# 预期: exit 0
# 已知非致命: cmd/simulation_test 有一行 type error（pre-existing）

# Frontend build
npm run build
# 预期: ✓ built in <N>s
```

### 2.2 Tests

```bash
# Go: 只跑我新加的测试（scope 限于 migration + 404 fix 改的包, 不跑 pre-existing
# 失败的 safeheron_migrations_test / TestWealthService_GetAssets）
go test -v -run "TestAddPendingStatus|TestNormalizeAmount|TestAddMissingForeignKeys|TestMigrationOrder|TestFundReportRepository|TestFundService" \
  ./internal/migration/migrations/ ./internal/repository/postgres/ ./internal/services/

# 预期: 全部 PASS（不包含 pre-existing 的 fail）
# 已知 8 fail 都在别的包：safeheron_migrations_test 7 个 + TestWealthService_GetAssets 1 个

# Go vet
go vet ./internal/migration/... ./internal/repository/... ./cmd/migrate/... ./cmd/server/...
# 预期: 0 警告
```

### 2.3 Secret-scan guard

```bash
bash scripts/check-secrets.sh
# 预期: "==> OK: no rotated literals, no hardcoded DSNs, migration runner intact."
# exit 0
```

### 2.4 Lint (baseline comparison)

```bash
# Capture baseline (before any of this batch) for comparison
# (run this on main before the audit-fixes branch is merged)
echo "BASELINE pre-audit-fixes:"
npm run lint 2>&1 | tail -2

# Current state (post-audit-fixes branch)
echo "CURRENT:"
npm run lint 2>&1 | tail -2
# 预期: 422 个问题（与 baseline 相同的数字）
# 如果 current 比 baseline 多: STOP, 找出新加的 lint 错
```

### 2.5 Build the migrator binary (备查)

```bash
go build -o /tmp/monera-migrate ./cmd/migrate/
# 预期: 13MB binary, exit 0
chmod +x /tmp/monera-migrate
# 这个 binary 在生产用 scripts/deploy.sh 时会重新编译一次,
# 但本地预编译一份让你可以提前 dry-run 验证.
```

### 2.6 Dry-run the migrator against the LIVE DB (read-equivalent)

This proves the new migrator binary can talk to the DB endpoint
**with the current `.env`**. It will print "all 18 pending" because
production has never had any Go-migrator-applied migration recorded.

```bash
# Use the CURRENT .env DATABASE_URL. If this is the OLD password, you'll
# get an auth error — that's a signal the password was rotated and
# .env needs updating BEFORE you continue.
DATABASE_URL="$(grep ^DATABASE_URL= .env | head -1 | sed 's/^DATABASE_URL=//' | sed "s/^'//" | sed "s/'$//")" \
  /tmp/monera-migrate -dry-run
# 预期: 18 行 pending (001-005, 007-016, 046, 047, 048)
#       exit 0

# 如果这一步 auth 失败: 你说的"已轮换"是真的, .env 没更新.
# 立刻:
#   1. 从密码管理器拿新密码
#   2. 改 .env: DATABASE_URL='postgresql://neondb_owner:NEW_PASSWORD@...'
#   3. 重新跑这条 dry-run. 必须 200 列出 18 个 pending.
```

---

## 3. Staging DB apply (mandatory gate)

Apply the migration runner to a **staging or dev Neon branch** first.
This catches data-shape surprises that unit tests can't.

### 3.1 Staging pre-flight

```bash
# Option A: Neon branching
#   In Neon console: create a branch "staging-2026-06-05" from main
#   Copy the new DATABASE_URL for the branch

# Option B: Use an existing dev DB
#   Make sure .env points to a non-prod database

# 验证 (read-only, no side effects)
bash scripts/db-promote/01-preflight.sh
# 预期: PASS (info: 016 not yet applied)
```

### 3.2 Staging apply

```bash
bash scripts/db-promote/02-promote.sh
# Type 'yes' to confirm.
# 预期: 18 个 migration 跑完
#       包括创建 18 个表 + 016 种子 fund_reports (5 行) + 种子 fund_asset_allocations (4 行)

# 关键中间检查 (手工):
# 1. 看输出 log，应该没有 ERROR/FAIL
# 2. 看 migrations 表:
psql "$STAGING_DSN" -c "SELECT version, name, executed_at FROM migrations ORDER BY version"
# 预期: 18 行, version 001-005, 007-016, 046, 047, 048
```

### 3.3 Staging pre-check verification

如果**任一** pre-check 失败, 立即 stop, 不要 retry, 先看对应 runbook:

| Migration | Pre-check | 失败信息 | 恢复 |
|---|---|---|---|
| 047 (H-1) | `WHERE amount !~ '^-?[0-9]+(\.[0-9]+)?$'` | `H-1: cannot migrate deposits.amount — N rows are not numeric literals` | `SELECT id, amount FROM deposits WHERE amount !~ '^-?[0-9]+(\.[0-9]+)?$' LIMIT 50;` 手动修复（见 H1-H2-NOTES §4） |
| 048 (H-2) | orphan check on 3 FK | `H-2: cannot add FK — N withdrawal_verification rows have no matching withdrawal_order` | 找出 orphan, 决定保留/删除（见 H1-H2-NOTES §4） |
| 016 (fund_reports) | none (DML with `ON CONFLICT DO NOTHING` only) | 不会在 pre-check 失败 | — |

如果 pre-check 成功, 跑下一步。

### 3.4 Staging 端到端验证

```bash
# 1. Schema check
psql "$STAGING_DSN" -c "\d fund_reports"
psql "$STAGING_DSN" -c "\d fund_asset_allocations"

# 2. Data check
psql "$STAGING_DSN" -c "SELECT to_char(report_date,'YYYY-MM') AS m, total_aum FROM fund_reports ORDER BY report_date"
# 预期: 5 行从 1000000.00 涨到 14820125.94
psql "$STAGING_DSN" -c "SELECT category, amount, pct FROM fund_asset_allocations fa JOIN fund_reports fr ON fr.id=fa.report_id WHERE fr.report_date='2026-05-01' ORDER BY sort_order"
# 预期: 4 行, sum(pct)=1.0000

# 3. 启动 staging backend, 跑 API smoke
DATABASE_URL="$STAGING_DSN" go run ./cmd/server &
sleep 10
curl -s http://localhost:8081/api/fund/stats | head -c 500
# 预期: {"success":true,"data":{"current":{"reportDate":"2026-05","totalAum":14820125.94,...}, ...}}
```

如果 staging 端到端是 200 with 真实 May 2026 data, **go ahead to 生产**。如果任何一步异常, stop, 调查。

---

## 4. Production DB apply

**`★ 此节会改生产数据。在跑每条命令前想清楚。`**

### 4.1 Backup (mandatory before any DB mutation)

```bash
# Option A: pg_dump 整个 schema
pg_dump "$PROD_DSN" --schema-only > /tmp/prod-schema-before-c2h1h2-$(date +%Y%m%d-%H%M%S).sql

# Option B: pg_dump 整个 DB (更慢但更保险; 这是推荐路径)
pg_dump "$PROD_DSN" -Fc > /tmp/prod-full-before-c2h1h2-$(date +%Y%m%d-%H%M%S).dump

# Option C: 用 db-promote 工具 (只覆盖 fund_* 两表, 不够)
bash scripts/db-promote/05-snapshot.sh
#   Type 'snapshot' to confirm

# 验证 backup 成功:
ls -la /tmp/prod-*.sql /tmp/prod-*.dump 2>/dev/null
# 必须 > 0 byte
```

**重要**: 验证 backup 后才能继续。**没有 verified backup, 不跑 §4.3。**

### 4.2 Pre-deploy check (read-only, safe)

```bash
bash scripts/db-promote/01-preflight.sh
# 预期: PASS (info: 016 not yet applied)
```

如果 preflight fail, **STOP, 不继续**。 看输出找原因。

### 4.3 Production apply (canonical path: only one way)

```bash
bash scripts/db-promote/02-promote.sh
# 输出会显示 18 个 pending migrations, 然后问:
#   "Type 'yes' to continue (any other input aborts):"
# Type 'yes' to confirm.
# 预期: 18 个 migration 跑完
```

**Do NOT bypass this script with raw `go run ./cmd/migrate`**.
`02-promote.sh` provides:
- Interactive `yes` confirmation (prevents accidents)
- Pre-flight validation (the `01-preflight.sh` logic)
- A `godotenv` load that respects `--env-file` overrides
- A log destination that's predictable for debugging

### 4.4 Production apply verification (read-only)

```bash
# 1. migrations 表应该有 18 行
psql "$PROD_DSN" -c "SELECT count(*) FROM migrations"
# 预期: 18

# 2. fund_reports/fund_asset_allocations 表已创建 + 种子
psql "$PROD_DSN" -c "SELECT to_char(report_date,'YYYY-MM') AS m, total_aum::text FROM fund_reports ORDER BY report_date"
# 预期: 5 行

psql "$PROD_DSN" -c "SELECT category, amount::text, pct::text FROM fund_asset_allocations fa JOIN fund_reports fr ON fr.id=fa.report_id WHERE fr.report_date='2026-05-01' ORDER BY sort_order"
# 预期: 4 行, pct 之和 = 1.0000

# 3. 047 的列类型已改
psql "$PROD_DSN" -c "\d deposits" | grep "amount"
# 预期: "amount | numeric(32,8)" (不再是 character varying)

# 4. 048 的 FK 已加
psql "$PROD_DSN" -c "SELECT conname, conrelid::regclass, confrelid::regclass FROM pg_constraint WHERE conname IN ('fk_withdrawal_verification_order','fk_withdrawal_freeze_log_order','fk_address_pool_user')"
# 预期: 3 行
```

### 4.5 DB apply 出错时的恢复

**047 pre-check 失败**（"H-1: cannot migrate deposits.amount — N rows"）:
- 不要重试. 看具体哪几行.
- `SELECT id, amount FROM deposits WHERE amount !~ '^-?[0-9]+(\.[0-9]+)?$' LIMIT 50;`
- 决定: UPDATE 到 NULL 或 0 (人工审查后). 然后重跑迁移.
- 没有数据损失 (因为 BEFORE 是 VARCHAR, AFTER 失败时整个事务回滚).

**048 pre-check 失败**（"H-2: cannot add FK — N ... rows"）:
- 同上. 找出 orphan, 决定保留/删除.
- `DELETE FROM withdrawal_verification WHERE id IN (...)` (人工审查后).
- 重跑迁移.

**任何其他错误**:
- 不要重试. 把日志和出错步骤贴给团队.
- 数据库本身没受影响 (整个事务在 BEGIN/COMMIT 包裹里, 失败 = 自动 ROLLBACK).
- 重跑 `02-promote.sh` 是 idempotent 的 (用 IF NOT EXISTS / pg_constraint 预检).

---

## 5. Backend binary deploy (EC2)

**Use `scripts/deploy.sh` — the canonical production deploy tool. It
handles build, backup, swap, restart, health-check, AND auto-rollback
on migrate failure. Do NOT hand-roll these steps.**

### 5.1 Prepare

```bash
# SSH 到 prod host
ssh ec2-user@<prod-host>

cd /home/ec2-user/monera

# 拉最新代码 (deploy 前确保 main 分支是审计-fix batch)
git fetch origin
git checkout main
git pull

# 验证 commit
git log --oneline -5
# 应该看到含这 5 批改动的 commit

# 关键检查: .env 不应在 git status 里 (如果出现, 见 §5.6)
git status --short
```

### 5.2 Update .env (BEFORE deploy.sh)

```bash
# ⚠️ 这里更新到新密码 (rotated 那次的)
# 偏好: 使用密码管理器或 scp 复制 .env.example 修改版
# 避免: 直接用 vi/nano 编辑 (容易粘贴错误)

# 如果 vi/nano 是唯一选项:
sudo -e /home/ec2-user/monera/.env
# 改 DATABASE_URL 行的 password 部分
# (sudo -e 比 sudo vi 安全: 它创建临时文件 + 用 EDITOR 编辑, 防止覆盖 .env 错)
chmod 600 /home/ec2-user/monera/.env

# 验证 (密码会显示, 但仅本机)
sudo grep ^DATABASE_URL /home/ec2-user/monera/.env
# 预期: postgresql://neondb_owner:NEW_PASSWORD@...

# ⚠️ 验证 .env 绝不在 git tracking
git status
# 预期: .env 不在 M/A/?? 任何列表
# 如果出现: 立刻 §9.3 credential-leak 流程
```

### 5.3 Deploy via the canonical script

```bash
# 跑 deploy.sh, 跳过 migrator (因为 §4 已经跑了)
bash scripts/deploy.sh --skip-migrate

# 脚本会:
# 1. 验证环境 (go.mod, .env)
# 2. 编译 server + monera-migrate binaries
# 3. 备份现有 server → server.bak (用于 auto-rollback)
# 4. systemctl stop monera-digital
# 5. 复制新 binary 到部署目录
# 6. (skip-migrate: 跳过)
# 7. 更新 systemd service unit
# 8. systemctl start monera-digital
# 9. 健康检查 (5 次重试, /api/health)
# 预期: "部署成功！"
```

**为什么 `--skip-migrate`**: §4 已经跑过 migrator。`02-promote.sh` 写的
migrations 表已经有 18 行。`deploy.sh` 默认会再跑一次 migrator，那是
**无害的**（idempotent，全部 pending 跳掉），但日志会很乱且双重
检查。`--skip-migrate` 让 deploy 只做 binary swap。

**如果 deploy.sh 在编译失败 / 启动失败**: 它**不会**自动回滚 (auto-rollback only on migrate failure)。需要手动:
```bash
# 看错误
sudo journalctl -u monera-digital -n 100 --no-pager

# 手动恢复旧 binary
ls -la /home/ec2-user/monera/server.bak
sudo cp /home/ec2-user/monera/server.bak /home/ec2-user/monera/server
sudo systemctl start monera-digital
```

### 5.4 Backend 启动 log 检查

```bash
# 正确的 service name: monera-digital (deploy.sh:33 硬编码)
sudo journalctl -u monera-digital -n 200 --no-pager
# 预期看到:
#   "Database connected successfully"
#   "Server starting on port 8081"
#   "/api/health" 200 OK 多次 (deploy.sh 的 health check)
# 不应有: ERROR, FATAL, panic
```

### 5.5 验证 .env 没被 commit 到 git

```bash
git status
# 预期: .env 不在 M/A/?? 任何列表

git log -p --all --full-history -- .env 2>/dev/null | head -3
# 预期: 空 (git 拒绝 track .env)
```

如果 .env 出现在 git status 或 git log: **SECURITY INCIDENT**, 立即:
- `git filter-repo --invert-paths --path .env` 重写历史
- 通知 team + 客户如果合规要求
- 重新轮换 Neon 密码 (因为旧 .env 在 git 历史上 = 旧密码已公开)

---

## 6. Frontend deploy (Vercel) — **OPTIONAL**

**This batch has zero frontend changes.** C-1 (credential rotation),
C-2 (migration rebuild), H-1 (amount types), H-2 (foreign keys), and
the 404 fix are all backend changes. The frontend bundle is
functionally identical to the previous deploy.

You can skip this section unless:
- You want a fresh Vercel build for operational hygiene (cache
  invalidation, dep upgrade accumulation)
- Vercel build environment is out of sync with main

### 6.1 Verify there's nothing to change in Vercel env

Vercel env dashboard → Project → Settings → Environment Variables:
- `DATABASE_URL`: **Do NOT change** (frontend never directly
  connects to the DB; Vercel uses the backend API URL, configured
  in `vite.config.ts` via the `/api` proxy)
- `ENCRYPTION_KEY`, `JWT_SECRET`, `VITE_API_URL` (or equivalent):
  this batch didn't touch these. If any of them was leaked
  historically, **rotate them** in Vercel env (out of scope for this
  deploy; do in a separate change)

### 6.2 (If proceeding) Re-deploy

```bash
# Option A: Vercel CLI
vercel --prod

# Option B: git push (if Vercel GitHub integration is configured)
git push origin main
```

### 6.3 Verify

```bash
curl -s -w "  HTTP %{http_code}  body=%{size_download}B\n" -o /dev/null --max-time 10 https://moneradigital.com/
# 预期: 200
```

---

## 7. End-to-end smoke

跑完整 stack 验证。**全部必须 200/预期**:

```bash
echo "[A] Homepage (Vercel)"
curl -s -w "    HTTP %{http_code}\n" -o /dev/null --max-time 10 https://moneradigital.com/
# 预期: 200

echo "[B] /api/health (BFF, either via Vercel proxy or direct)"
curl -s -w "    HTTP %{http_code}\n" --max-time 10 https://moneradigital.com/api/health
# 预期: 200 {"status":"ok"}

echo "[C] /api/fund/stats (C-2 + 404 fix combined — the headline test)"
curl -s -w "    HTTP %{http_code}\n" --max-time 10 https://moneradigital.com/api/fund/stats
# 预期: 200 + May 2026 数据 (DB 已经种子了, fund_reports 5 行)

# 浏览器手测 (1 user 即可)
# - 打开 https://moneradigital.com
# - 主页 AUM widget 应显示: $14.82M, +4.61% MoM, 4 个 strategy 饼图
# - /wealth/products 页面能加载
```

### 7.1 如果 §7.C 返回 404 "No fund report available yet"

**这是 404 fix 生效的正常空状态**, 不是 deployment bug. 含义是:
- New binary 上线 ✓ (404 fix 在新 binary 里)
- 但 DB 里 `fund_reports` 表还不存在 (production DB 还没跑迁移)

处理:
1. 立即去 §4 跑 `bash scripts/db-promote/02-promote.sh`
2. 跑完后再 curl 一次 §7.C, 预期 200 with May 2026 data

### 7.2 如果 §7.C 返回 500

**这是 deployment bug**. 立刻:
- 看 `journalctl -u monera-digital -n 100`
- 99% 可能是 pre-check fail 在 production 但**没被检测出来** (因为 staging 跑过)
- Rollback via §9.2

---

## 8. Post-deployment tasks

### 8.1 监控 (前 30 分钟密集)

```bash
# 在 EC2 上开一个 monitor session
watch -n 5 'ps aux | grep -E "monera|server" | grep -v grep'
# 观察 backend 进程稳定

# Backend error rate (注意 service name)
sudo journalctl -u monera-digital -f | grep -iE "error|panic|fatal" | head
# 应该 0 错误

# API 5xx 监控 (这通常由 Vercel 或 UptimeRobot 监控, 这里只 sanity check)
for i in 1 2 3 4 5 6 7 8 9 10; do
  sleep 30
  code=$(curl -s -o /dev/null -w "%{http_code}" --max-time 5 https://moneradigital.com/api/health)
  echo "$(date +%H:%M:%S) /api/health = $code"
done
# 预期: 全部 200
```

### 8.2 Install pre-commit hook (开发机)

每个开发 clone 跑一次:

```bash
bash scripts/install-hooks.sh
# 预期: "Installed pre-commit hook at .git/hooks/pre-commit"

# 验证
ls -la .git/hooks/pre-commit
# 预期: -rwxr-xr-x (executable)

# 测试 (注意: 文件必须在 repo 内, git add 才能 add)
cd /path/to/MoneraDigital
echo "SELECT 1" > /tmp/hook-test.sql
git add /tmp/hook-test.sql
git commit -m "test pre-commit hook"
# 预期: guard script 跑过, commit 成功 (因为 /tmp/hook-test.sql 是新增但非代码文件,
#       guard 扫的是 .go/.ts/.sql 等特定类型, 应该不会 reject)
# 如果想真测 secret-scan: echo "npg_4zuq7JQNWFDB" > /tmp/test-leak.sql
#   然后 git add /tmp/test-leak.sql; git commit -m "test"
#   预期: commit 被 reject (guard 找到泄露 literal)
```

### 8.3 通知团队

按状态分三条模板 (deploy 前/中/后):

- **Deploy in progress** (§1.4 已设置, deploy 开始时再发一次):
  > `:rocket: Deploy 2026-06-05 启动. C-1/C-2/H-1/H-2 + 404 fix 上生产.`
  > `预计 60-90 min. 状态看 https://[deploy-log-url]`

- **Deploy complete** (§5 deploy.sh 成功跑完后):
  > `:white_check_mark: Deploy 2026-06-05 完成. 5 批 audit 修复 all live.`
  > `新行为: /api/fund/stats 返回 200 with May 2026 数据 ($14.82M, +4.61% MoM)`
  > `文档: docs/security/PRODUCTION-DEPLOY-2026-06-05.md, ROTATION_RUNBOOK.md, MIGRATION-NOTES.md, H1-H2-NOTES.md`

- **Deploy rolled back** (§9 触发时):
  > `:warning: Deploy 2026-06-05 已回滚. 原因: [原因]. 当前在 deploy 前状态.`
  > `下一步: [下一步]`

### 8.4 后续清理 (72 小时内)

- [ ] 把 `docs/security/PRODUCTION-DEPLOY-2026-06-05.md` 移到
      `docs/operations/` (deploy guide 应该是 ops 文档, 不是
      security 文档)
- [ ] 把 `docs/code-audit-report.md` 加上 changelog "已修复
      2026-06-05: C-1, C-2, H-1, H-2 + 404 fix"
- [ ] 在团队 wiki 加 entry: "How to onboard a new dev to MoneraDigital" —
      包含 `git clone → npm install → .env from password manager →
      scripts/install-hooks.sh`
- [ ] 下次 sprint: 把 `safeheron_migrations_test.go` 那 7 个 pre-existing
      fail 修了 (独立的 cleanup PR)

---

## 9. Rollback plan

**每阶段都有显式回滚路径**. deploy 出了任何问题, 不要慌, 不要瞎跑命令, 按下表回滚.

### 9.1 Rollback DB apply (§4)

`scripts/deploy.sh` 已经在 **migrator 失败时自动回滚 binary**. 但
**DB 本身的 schema 变化** 不会自动回滚. 如果你刚跑完 02-promote.sh,
发现 staging 验证某些东西坏了:

```bash
# Option A: 回滚整个 016 (drop 两个 fund 表 + unregister migration)
# 这是 db-promote 工具能回滚的. 它会:
#   DROP TABLE fund_asset_allocations;
#   DROP TABLE fund_reports;
#   DELETE FROM migrations WHERE version = '016';
CONFIRM_ROLLBACK=yes bash scripts/db-promote/04-rollback.sh
#   Type 'rollback' to confirm
```

**重要**: 04-rollback.sh **只** 滚 016. 001-015, 046, 047, 048 不在它
的 scope 里. 可逆性表:

| Migration | 可逆? | 怎么回滚 |
|---|---|---|
| 016 (fund_reports + fund_asset_allocations) | **是** | `04-rollback.sh` |
| 001-015 (其他) | **是** (大多是 IF NOT EXISTS, 跑一次已经是 no-op) | 重跑 `02-promote.sh` 是 idempotent |
| 046 (PENDING status + activation cols + rate_limits) | **是** (全 IF NOT EXISTS, no-op) | 同上 |
| 047 (deposits.amount, coin_chains.min_deposit_amount → NUMERIC) | **❌ 否** (Down 显式拒绝, "intended to be a no-op" 注释) | 必须用 `pg_dump` 备份恢复 (见下) |
| 048 (3 个 FK) | **是** (Down drops FK constraints; 不回滚数据) | 反向 migration: `ALTER TABLE ... DROP CONSTRAINT ...` 跑 3 次 |

```bash
# Option B: 恢复 pg_dump 备份 (针对 047 失败等不能逆向的情况)
# ⚠️ 这会恢复 **整个** DB, 不只是 fund_* 两表. 需 maintenance window.
# 建议: 找一个非高峰时段, 用备份 restore.
pg_restore -d "$PROD_DSN" --clean --if-exists /tmp/prod-full-before-c2h1h2-*.dump
```

**关键提示**: 如果你担心 047/048 pre-check 在生产失败, **必须**先在
staging 完整跑过 (§3). 这是为什么 §3 是 mandatory gate.

### 9.2 Rollback backend binary (§5)

`scripts/deploy.sh` **已经自动备份旧 binary** 到 `${APP_DIR}/server.bak`
和 migrator 到 `${APP_DIR}/monera-migrate.bak` (deploy.sh:82-83). 如果
新 binary 上线后发现问题:

**Option A: 用 deploy.sh 的备份 (fastest)**

```bash
# SSH 到 prod host
ssh ec2-user@<prod-host>
cd /home/ec2-user/monera

# 1. 停止服务
sudo systemctl stop monera-digital

# 2. 恢复备份
sudo cp /home/ec2-user/monera/server.bak /home/ec2-user/monera/server
sudo chmod +x /home/ec2-user/monera/server

# 3. 启动
sudo systemctl start monera-digital

# 4. 验证
sleep 2
curl -s -w "  HTTP %{http_code}\n" http://localhost:8081/api/health
# 预期: 200

# 5. 用 git revert 撤销新代码 (而不是 git checkout 避免 detached HEAD)
git revert --no-edit HEAD  # 撤销最近一次 commit (即这次 deploy)
git push origin main
# 或: git revert <specific-deploy-commit-sha>
```

**Option B: git checkout 旧 commit (有风险)**

```bash
# ⚠️ 此操作会让 working tree 进入 detached HEAD. 跑完必须 git checkout main.
git log --oneline -5  # 找 deploy 前的 commit SHA
git checkout <pre-deploy-commit-sha>
go build -o /home/ec2-user/monera/server ./cmd/server
sudo systemctl restart monera-digital
git checkout main  # 回到 main 分支
```

**不推荐 Option B**; 用 Option A (deploy.sh 已自动备份).

### 9.3 Rollback frontend (Vercel) — if you deployed

```bash
# Vercel CLI
vercel rollback

# 或: Vercel dashboard → Deployments → 找上一个 good → "Promote to Production"
```

### 9.4 凭证泄露紧急情况 (worst case)

如果你 deploy 过程中 **意外 commit 了带新密码的 .env**:

1. **立刻** 在 Neon console → Reset password (新密码)
2. `git filter-repo --invert-paths --path .env` 重写历史
3. Force push: `git push --force-with-lease`
4. **强制**所有协作者 re-clone (git pull 不会重置已经 clone 的本地历史,
   必须 fresh clone). 用 Slack 发文字强制要求.
5. 重新跑整个 deploy 流程用新密码

---

## 10. 责任清单

| 角色 | 责任 |
|---|---|
| **Deploy owner** (本次 = 平台 lead) | 跑 §2-§5, 监控 §8.1, 写 deploy log (§12) |
| **SRE on-call** | §0 announce, 接到 escalation 时按 §9 回滚 |
| **Security** | §1.2 确认凭证轮换 + §8.4 凭证清理 |
| **Backend lead** | §3.3 验证 staging 通过, §4 production apply 监控 |
| **Frontend lead** | §6 Vercel 部署 (如果走), §7 e2e UI smoke |
| **Product owner** | §7 浏览器手测, 验证首页 AUM widget 视觉上 OK |

---

## 11. 时间估算

| Phase | 估计 | 阻塞恢复 |
|---|---|---|
| §1 Pre-checklist | 1h | 凭证管理器 (1Password 等) |
| §2 Local verify | 15 min | Go toolchain, node_modules |
| §3 Staging apply | 30 min | Neon branch 准备 |
| §4 Prod DB apply | 10 min + 5 min 验证 | 凭证从管理器取 |
| §5 Backend deploy (via deploy.sh) | 5 min + 5 min 验证 | systemd / sudo / Go on EC2 |
| §6 Frontend deploy (optional) | 10 min + 5 min 验证 | Vercel auth |
| §7 E2E smoke | 10 min | — |
| §8 Post-deploy | 30 min 监控 + 异步清理 | — |
| **总 (critical path)** | **~3h** | |

---

## 12. Sign-off

部署完成后, 部署负责人在 deploy log 里填:

```
[YYYY-MM-DD HH:MM] deploy owner: <name>
  - §1 pre-checklist: PASS / FAIL (notes)
  - §2 local verify: PASS / FAIL (notes)
  - §3 staging apply: PASS / FAIL (notes)
  - §4 prod DB apply: PASS / FAIL (notes)
  - §5 backend deploy (via deploy.sh): PASS / FAIL (notes)
  - §6 frontend deploy (optional): SKIPPED / PASS / FAIL (notes)
  - §7 e2e smoke: PASS / FAIL (notes)
  - §8 post-deploy: PASS / FAIL (notes)
  - 30 min monitor window: clean / observed N issues
  - rollback needed: yes / no
  - production user-facing status: working / degraded / down
```

把填好的 deploy log 附在 git tag `prod-deploy-2026-06-05` 上, 团队审计。
