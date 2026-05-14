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
./bin/server                                  # 启动（默认 :8081，读 .env）
go test ./internal/... -cover                 # 全量测试 + 覆盖率
go test ./internal/wallet/deposit/... -v      # 单模块测试
go vet ./...                                  # 静态分析
```

### Database Migration
```bash
go run ./cmd/migrate/                         # 执行迁移（读 DATABASE_URL from .env）
```

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
4. **Testing**: TDD（先写测试），coverage ≥ 80%
5. **Isolation**: 改动不影响无关功能
6. **Goroutine Recover 铁律**: 所有 `go func()` 或 `go obj.Method()` 启动的 goroutine **必须**在函数体开头加 `defer func() { if r := recover(); r != nil { log.Printf(...) } }()` 兜底。goroutine 内 panic 不会被外层捕获，会直接崩整个进程

---

## Go Backend Structure (`internal/`)

```
internal/
├── handlers/           # HTTP handlers（Gin）
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

### Safeheron Phase 1 新增表（migration 015）
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

Phase 1 所有 schema 变更合并在 `015_safeheron_phase1.go` 一个文件中。

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

### Optional
```
UPSTASH_REDIS_REST_URL          # Redis（限流、会话）
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

### Test Environment (CI/CD)
- **Backend**: `test` 分支 push → GitHub Actions → 编译 Go binary → SCP 到测试服务器 → systemd 托管（端口 8086）
- **Frontend**: `scripts/deploy-remote.sh --frontend` → Vercel
- **Workflow**: `.github/workflows/deploy-backend-test-env.yml`
- **Deploy script**: `scripts/deploy-remote.sh`（`--env test` 后端 / `--frontend` 前端）

### Production
- **Frontend**: Vercel（main 分支自动部署）
- **Backend**: Docker 镜像 → GHCR → 服务器 docker compose（`.github/workflows/deploy.yml`）

---

## Authentication & Security

- **JWT**: 24h expiry, localStorage 存储
- **2FA**: TOTP (otplib) + AES-256-GCM 加密存储
- **Rate Limiting**: Redis-backed, per-endpoint 配置
- **Webhook 安全**: IP 白名单 → RSA 验签 → 解密（三层校验）
- **KYT 合规**: 生产环境强制开启，LOW 自动放行，MEDIUM+ 人工审核

---

## Gotchas

- **viper.AutomaticEnv** 优先读 shell 环境变量，会覆盖 .env 文件中的值。如果本地 shell 有残留的 `DATABASE_URL` 等，.env 里的值不会生效
- **WalletRegistry 有内存缓存**：修改 DB 中 coin_chains 后需重启后端才能生效
- **coin_chains.safeheron_coin_key 是 UNIQUE 约束**：测试网和主网 coinKey 不同，seed 数据按 APP_ENV 区分
- **Vercel Hobby 限制 12 个 Functions**：所有 API 路由必须通过 `api/[...route].ts` 统一处理
- **015 migration 是幂等的**：可重跑，但已部署环境新增 KYT 字段需手动 ALTER
- **Go 后端端口**：本地 .env 中 `PORT=8081`，Vite dev server 在 5001

---

## Resources

- **Safeheron Phase 1 Spec**: `docs/spec/safeheron-phase1-spec.md`
- **Phase 1 Plan + Todo**: `docs/plans/safeheron-phase1-plan.md`, `docs/plans/safeheron-phase1-todo.md`
- **Architecture Audit**: `docs/ARCHITECT-AUDIT-REPORT.md`
- **Security Fixes**: `docs/SECURITY-FIXES.md`
