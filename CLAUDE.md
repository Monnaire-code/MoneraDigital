# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**MoneraDigital** is an institutional-grade digital asset platform offering secure, transparent static finance and lending solutions. Full-stack: React frontend (TypeScript, Vite) + Golang backend + Safeheron wallet integration.

**Key Stack:**
- Frontend: React 18, TypeScript, Vite, Tailwind CSS, Radix UI
- Backend: **Golang (Go)** — all business logic, database access, wallet operations
- Wallet: **Safeheron MPC** — 地址池管理、充值 webhook 处理、KYT 合规筛查
- External: **Monnaire Core API** — 核心账户管理（仅 Go 后端调用）
- Database: PostgreSQL (Neon)
- Testing: Vitest (frontend), Go test (backend)
- i18n: English + Chinese (i18next)

## Architecture

```
Frontend (React) → API Routes (Vercel proxy) → Go Backend → Safeheron SDK / Core API / Database
                                                    ↑
                                        Safeheron Webhook → Deposit Pipeline
```

### Critical Rules

1. **Frontend ONLY calls `/api/*` endpoints** — 禁止直接访问数据库或外部 API
2. **Go Backend handles ALL business logic** — 数据库、Safeheron SDK、Core API 集成
3. **Vercel API Routes 是纯代理** — 不含业务逻辑，只做路由转发和 auth check
4. **Safeheron webhook 直接打到 Go 后端** — 不经过 Vercel

### Layer Responsibilities

| Layer | Can Do | Cannot Do |
|-------|--------|-----------|
| Frontend (`src/`) | UI, form validation, call `/api/*` | Direct DB/SDK access |
| Vercel API (`api/`) | Route, auth check, proxy to Go | Business logic |
| Go Backend (`internal/`) | Business logic, DB, Safeheron SDK, Core API | — |

---

## Common Development Commands

### Frontend
```bash
npm run dev          # Vite dev server (http://localhost:5001)
npm run build        # Production build
npm run lint         # ESLint
npm run test         # Vitest (JSDOM)
```

### Go Backend
```bash
go build -o ./bin/server ./cmd/server/       # 构建后端
./bin/server                                  # 启动（项目标准本地配置为 :8081，读 .env）
go test ./...                                  # 全量 Go 测试
go test -race ./internal/companyfund/...      # 公司资金竞态回归
go test ./internal/wallet/deposit/... -v      # 单模块测试
go vet ./...                                  # 静态分析
```

### Database Migration
```bash
go run ./cmd/migrate -dry-run
EXPECTED_MIGRATION_CEILING=<version> go run ./cmd/migrate -exact-version <version>
```

- Stage/production 只执行本次新增的受控单版本迁移。
- 不要在发布环境使用默认全量 pending 入口，也不要重放历史迁移。

### Safeheron Address Pool
```bash
go run ./cmd/pool_init/ --evm-count=100 --tron-count=100  # 初始化地址池
go run ./cmd/pool_init/ --dry-run                          # 预览（不写 DB）
```

---

## JSON Naming Convention (Critical)

**All API request/response fields MUST use camelCase:**

```go
// Go struct tags
type LoginResponse struct {
    UserID      int       `json:"userId"`
    AccessToken string    `json:"accessToken"`
    CreatedAt   time.Time `json:"createdAt" db:"created_at"`  // camelCase JSON, snake_case DB
}
```

Frontend code must match: `data.userId`, `data.accessToken`, etc.

---

## Development Rules

**适用于新功能和 Bug 修复：**

1. **Tech Stack**: Frontend TypeScript, Backend Go（后端接口/数据库操作必须用 Go）
2. **Backend-Only Business Logic**: `api/` 纯代理，`internal/` 全部业务逻辑
3. **KISS**: 高内聚低耦合，不过度设计
4. **Testing**: TDD（先写测试）；新增或修改的业务行为分支以 100% 覆盖为目标
5. **Isolation**: 改动不影响无关功能
6. **Goroutine Recover 铁律**: 所有 `go func()` 或 `go obj.Method()` 启动的 goroutine **必须**在函数体开头加 `defer func() { if r := recover(); r != nil { log.Printf(...) } }()` 兜底。goroutine 内 panic 不会被外层捕获，会直接崩整个进程

---

## Go Backend Structure (`internal/`)

```
internal/
├── handlers/           # HTTP handlers（Gin）
├── companyfund/        # 公司资金 Provider 采集、幂等合并、风险、估值和对账
├── services/           # 业务服务（Auth, Lending, Withdrawal, Wallet, Wealth...）
├── repository/         # 数据库仓储（postgres/）
├── middleware/         # 认证、限流中间件
├── config/            # 应用配置加载
├── container/         # DI 容器（所有服务组装）
├── migration/         # Go-based DB migration（015_safeheron_phase1.go 等）
├── routes/            # Gin 路由注册
├── safeheron/         # Safeheron SDK adapter（RSA 签名、API 调用、webhook 验签）
├── wallet/
│   ├── config/        # 链/币注册表（chains + coins + coin_chains → Registry）
│   ├── pool/          # 地址池管理（Manager + Replenisher 后台协程）
│   └── deposit/       # 充值流水线（webhook 入库 → worker 异步处理 → KYT → 入账）
├── alert/             # 告警服务（飞书 webhook + 邮件）
├── coreapi/           # Monnaire Core API 客户端
└── db/                # 数据库连接初始化
```

### Safeheron Deposit Pipeline

充值流程：Safeheron webhook → 同步入库 → Worker 异步处理

```
Safeheron Console
    ↓ webhook (POST /api/webhooks/safeheron)
IP 白名单 → RSA 验签 → 解密 payload → 写入 safeheron_webhook_events
    ↓
Deposit Worker（1s 轮询）
    ↓ ProcessOne()
匹配 address_pool → 创建 deposits 记录 → KYT 合规筛查
    ↓
KYT 结果：
  LOW      → CREDITED（入账 + journal）
  MEDIUM+  → MANUAL_REVIEW（飞书告警）
  超时     → 兜底 API 查询（20min 后）
```

**Deposit 状态机：**
```
PENDING → CHAIN_VERIFYING → CHAIN_VERIFIED → KYT_PENDING → CREDITED
                                                         → MANUAL_REVIEW
                                                         → FAILED
```

**关键不变量：**
- CREDITED / FAILED 是终态，不可覆写（ErrDepositTerminalState）
- MANUAL_REVIEW 不可被 MarkDepositFailed 覆写
- KYT_ENABLED=false 在 production 环境启动会 panic（K-16）
- 余额不能为负（account 表有 CHECK 约束）

### WalletRegistry（链/币配置）

`internal/wallet/config/Registry` 从 DB 加载 chains + coins + coin_chains，内存缓存 + 后台定时刷新。代码中通过 `registry.CoinChainByCoinKey(coinKey)` 查找配置。

DB 表关系：`coins 1:N coin_chains N:1 chains`

coin_chains 按 APP_ENV 区分 seed 数据：
- `production`: 主网 coinKey（`ETH`, `USDC_ERC20`, `TRX`, ...）
- `local/development/test`: 测试网 coinKey（`ETH(SEPOLIA)_ETHEREUM_SEPOLIA`, ...）

### Company Funds

公司资金与客户充值是两个独立的领域。公司资金的规范流程为：

```text
company_fund_provider_events
  → company_fund_provider_transaction_facts
  → company_fund_transactions
```

- Safeheron/Airwallex 提供自动事实，MGT 维护账户配置和财务业务字段。
- `Account Channel` 与 `Transaction Source` 必须分离；开工前阅读根目录 `CONTEXT.md` 和相关 ADR。
- 批量交易允许在同一 TxHash 下产生多条 movement，不得按 TxHash 去重。

---

## Frontend Structure (`src/`)

**Pages** (`src/pages/`):
- `Index.tsx` — Landing page
- `Login.tsx` — 两步认证（密码 + 2FA）
- `Register.tsx` — 注册 + 邮件验证
- `dashboard/` — Overview, Assets, **Deposit**, Lending, Addresses, Withdraw, Security, FixedDeposit

**Services** (`src/lib/`):
- `auth-service.ts` — `/api/auth/*`
- `two-factor-service.ts` — `/api/auth/2fa/*`
- `wallet-service.ts` — `/api/wallet/deposit-address`, `/api/wallet/deposit-coins`, `/api/deposits`
- `lending-service.ts`, `withdrawal-service.ts`, `address-whitelist-service.ts`

**Deposit 页面** (`src/pages/dashboard/Deposit.tsx`):
选币 → 选链 → 获取充值地址 → 展示地址 + QR 码 + 充值记录

---

## Vercel API Routes

**统一路由处理器**：`api/[...route].ts`（唯一 Serverless Function）

Vercel Hobby 限制 12 个 Functions，所有路由在 `ROUTE_CONFIG` 配置表中注册，不创建单独文件。

**添加新路由：**
1. 在 `api/[...route].ts` 的 `ROUTE_CONFIG` 中添加配置
2. 在 Go 后端 `internal/routes/routes.go` 中添加 handler
3. 在 `api/__route__.test.ts` 中添加测试

---

## Database Schema

### 原有表
- **users** — id, email, password, twoFactorSecret/Enabled/BackupCodes, createdAt
- **account** — 用户资产账户（含 `ck_balance_non_negative` / `ck_frozen_non_negative` CHECK 约束）
- **journal** — 账务流水（biz_type=10 为充值入账）
- **lending_positions** — 借贷仓位
- **withdrawal_addresses** / **address_verifications** / **withdrawals** — 提币白名单 + 提币记录

### Safeheron Phase 1 历史新增表（migration 015）
- **chains** — 链配置（code PK, network_family: EVM/TRON, explorer_url）
- **coins** — 币种（symbol UNIQUE, is_stable）
- **coin_chains** — 链上币种配置（safeheron_coin_key UNIQUE, token_contract, decimals, min_deposit_amount）
- **address_pool** — Safeheron MPC 地址池（status: AVAILABLE/ASSIGNED/ERROR, customer_ref_id）
- **deposits** — 充值记录（safeheron_tx_key, status, aml_screening_state, kyt_report JSONB）
- **safeheron_webhook_events** — Webhook 原始事件（event_type, raw_payload, processed bool）

### Migration 方式

Go-based migrator（`internal/migration/`），不是 Drizzle：
```bash
go run ./cmd/migrate/     # 执行
go run ./cmd/migrate-drop/ # 回滚（慎用）
```

Migration 015 只是 Safeheron 客户充值 Phase 1 的历史迁移，不代表当前全部 schema。公司资金 schema 由 `050` 及后续受控迁移演进。

---

## Environment Variables

### Required
```
DATABASE_URL                    # PostgreSQL 连接串
JWT_SECRET                      # JWT 签名密钥（≥32 bytes）
ENCRYPTION_KEY                  # AES-256-GCM 密钥（64 hex chars）
```

### Safeheron

⚠️ **v1.6 起 4 个 RSA 密钥不再以 PEM 内容形式配置，改为指向 `secrets/` 目录的文件路径**，详见 SPEC §10.1。运维需 `mkdir -p secrets && chmod 0700 secrets`，把 4 个 PEM 放进去并 `chmod 0600 *-private.pem *-priv.pem` / `chmod 0644 *-pub.pem`。`secrets/` 已加入 `.gitignore`。

```
SAFEHERON_API_KEY                       # API Key（Console 生成）
SAFEHERON_API_BASE_URL                  # https://api.safeheron.com（生产）
SAFEHERON_PRIVATE_KEY_PATH              # 客户端 RSA 私钥文件路径（0600）
SAFEHERON_PLATFORM_PUBLIC_KEY_PATH      # Safeheron 平台公钥文件路径（0644）
SAFEHERON_WEBHOOK_PUBLIC_KEY_PATH       # Webhook 验签公钥文件路径（0644）
SAFEHERON_WEBHOOK_PRIVATE_KEY_PATH      # Webhook 解密私钥文件路径（0600）
SAFEHERON_WEBHOOK_ALLOWED_IPS           # Webhook 源 IP 白名单（逗号分隔）
```

### KYT / Alert
```
KYT_ENABLED                    # true（production 必须 true，否则 panic）
KYT_TIMEOUT                    # KYT_PENDING 终态兜底阈值（默认 20m，强制 MANUAL_REVIEW）
KYT_SCAN_INTERVAL              # 超时扫描 ticker 间隔（默认 1m）
AML_FIRST_POLL_DELAY           # AML 安全网最小等待时长（默认 5m；AML_KYT_ALERT webhook ~78s 内到达为主路径）
AML_POLL_INTERVAL              # AML 安全网 ticker 频率（默认 60s；SQL minAge 条件过滤，5m 内不触发）
ALERT_WEBHOOK_URL               # 飞书机器人 webhook
ALERT_EMAIL_RECIPIENTS          # 告警邮件收件人（逗号分隔）
RESEND_API_KEY                  # Resend 邮件 API Key
SENDER_EMAIL                    # 发件人邮箱
```

### Pool / Worker
```
POOL_REPLENISH_INTERVAL         # 地址池补充间隔（默认 10m）
POOL_REPLENISH_LOW_EVM          # EVM 低水位（默认 50）
POOL_REPLENISH_TARGET_EVM       # EVM 目标数（默认 100）
POOL_REPLENISH_LOW_TRON         # TRON 低水位（默认 50）
POOL_REPLENISH_TARGET_TRON      # TRON 目标数（默认 100）
DEPOSIT_WORKER_INTERVAL         # Worker 轮询间隔（默认 1s）
```

### Company Funds

`COMPANY_FUND_*`、Airwallex、CoinGecko 和公司资金 Safeheron collector 变量以 `.env.example` 为权威清单。修改运行时配置时，同步更新 `.env.example`、Stage/production 环境变量和配置测试。

### Optional
```
UPSTASH_REDIS_REST_URL          # 可选 Redis 加速（不是公司资金当前功能的强制依赖）
UPSTASH_REDIS_REST_TOKEN
MONNAIRE_CORE_API_URL           # Core API（默认 http://198.13.57.142:8080）
APP_ENV                         # production / local / development / test
```

**Local Development**: 复制 `.env.example` 到 `.env`，非 production 环境自动用 `godotenv.Overload` 加载。

---

## Routing

```
/                          → Landing (public)
/login                     → Login (public)
/register                  → Register (public)
/activation                → 邮件激活 (public)
/dashboard                 → DashboardLayout (protected)
  /dashboard               → Overview
  /dashboard/assets        → Assets
  /dashboard/deposit       → Deposit（充值）
  /dashboard/lending       → Lending（借贷，Coming Soon）
  /dashboard/addresses     → Addresses（白名单）
  /dashboard/withdraw      → Withdraw
  /dashboard/fixed-deposit → Fixed Deposit
  /dashboard/security      → Security (2FA)
```

---

## Deployment

### Stage

- **Frontend**: `stage` push 触发 Vercel Preview；`main` 不自动发布前端。
- **Backend**: `stage` push 触发 `.github/workflows/deploy-backend-stage.yml` 的 **standard 一条龙**（编译 → 上传 → 受控迁库 → 换 server → 重启）。不再支持 cutover 双锁或多 mode 菜单。
- 每次新增受控迁移时，必须同步更新迁移 ceiling、stage/prod workflow 中的 ceiling 和对应测试。

### Production
- **Frontend**: Vercel（main 分支**不自动构建**，手动 `vercel deploy --prod --scope monera-digital`；配置见 `vercel.json` git.deploymentEnabled）
- **Backend**: `.github/workflows/deploy-backend-prod.yml` 仅允许在 `main` 上手动触发 **standard 一条龙**（与 stage 同路径；GitHub Environment 审批作为人工闸门）。输入 `expected_migration_ceiling` 做受控单版本迁移。
- **生产服务器**: `52.195.194.71`（ec2-user），binary `/home/ec2-user/monera/server`，systemd `monera-digital.service`，端口 8081
- **部署前置**: GitHub repo Settings → Environments → `production` 须配 required reviewers（workflow 无 confirm input，靠 env protection 兜底）
- **本地手动运维脚本**（CI 不依赖，仍可用）: `scripts/deploy.sh`（服务器现场编译 + SCP+systemd）、`scripts/deploy-remote.sh`

> 旧 `test` 分支环境已废弃；当前测试环境使用 `stage` 分支。

---

## Authentication & Security

- **JWT**: 24h expiry, localStorage 存储
- **2FA**: TOTP (otplib) + AES-256-GCM 加密存储
- **Rate Limiting**: 高风险端点限流；Redis 可用时作为分布式存储，缺失时按实现使用 fallback
- **Webhook 安全**: IP 白名单 → RSA 验签 → 解密（三层校验）
- **KYT 合规**: 生产环境强制开启，LOW 自动放行，MEDIUM+ 人工审核

---

## Gotchas

- **viper.AutomaticEnv** 优先读 shell 环境变量，会覆盖 .env 文件中的值。如果本地 shell 有残留的 `DATABASE_URL` 等，.env 里的值不会生效
- **WalletRegistry 有内存缓存**：修改 DB 中 coin_chains 后需重启后端才能生效
- **coin_chains.safeheron_coin_key 是 UNIQUE 约束**：测试网和主网 coinKey 不同，seed 数据按 APP_ENV 区分
- **Vercel Hobby 限制 12 个 Functions**：所有 API 路由必须通过 `api/[...route].ts` 统一处理
- **发布迁移必须是受控单版本**：使用 `-exact-version` 和一致的 `EXPECTED_MIGRATION_CEILING`，禁止在 Stage/production 重跑历史全量 pending 迁移
- **Go 后端端口**：本地 .env 中 `PORT=8081`，Vite dev server 在 5001

---

## Resources

- **Safeheron Phase 1 Spec**: `docs/spec/safeheron-phase1-spec.md`
- **Phase 1 Plan + Todo**: `docs/plans/safeheron-phase1-plan.md`, `docs/plans/safeheron-phase1-todo.md`
- **Architecture Audit**: `docs/ARCHITECT-AUDIT-REPORT.md`
- **Security Fixes**: `docs/SECURITY-FIXES.md`
- **Company-fund release control**: `docs/company-fund-stage-release-control.md`
- **Company-fund domain language**: `CONTEXT.md`, `docs/adr/`

## Agent skills

### Issue tracker

Issues are tracked in GitHub Issues for `Monnaire-code/MoneraDigital`. See `docs/agents/issue-tracker.md`.

### Triage labels

Use the canonical five-role triage vocabulary. See `docs/agents/triage-labels.md`.

### Domain docs

This repository uses a single-context domain layout with `CONTEXT.md` and `docs/adr/`. See `docs/agents/domain.md`.
