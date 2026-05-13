# Safeheron Phase 1 任务清单

> 配套文档: `docs/plans/safeheron-phase1-plan.md`（含决策记录、依赖图、验收基线）
> SPEC: `docs/spec/safeheron-phase1-spec.md` v1.5
> Last updated: 2026-05-12（v1.5 KYT 合规筛查补充）

任务格式约定：
- **依赖**：必须先完成的 task ID
- **DoD（Definition of Done）**：每条都是可观察的、可被外部人 5 分钟验证的
- **验证命令**：能直接 copy-paste 跑的命令

---

## 施工现状（2026-05-12）

| 任务 | 状态 | 说明 |
|------|------|------|
| **T1 ~ T9** | ✅ **已完成并合并到 dev 分支** | 见 `git log --oneline | grep "feat(safeheron)"`：T2/T3 SDK Adapter + Registry、T4/T5 Pool Init + Manager + Replenisher、T6 用户 API、T7 Webhook 同步 + 异步 worker、T8 前端切换、T9 充值页面 UX 重构都已 commit |
| **T10** | ✅ **已完成** | v1.5 KYT 合规筛查已实现（三事务架构 + 决策矩阵 + 超时扫描） |
| **T11** | ⏸️ **待施工** | v1.6 安全审计 8 项修复 + .env 优先（共 9 项）：状态覆写保护 / IP 白名单 / 余额约束 / KYT 校验 / fallback 锁 / RowsAffected / network_family / 注释 / .env 优先 |
| **T12** | ⏸️ 待 T11 完成后验收 | Sandbox 端到端 + 灰度上线 |

> **施工者必读**：T10 改造 `internal/wallet/deposit/service.go` 现有的 `ProcessOne` 方法，**这是已上线代码**（仅本地 dev 环境，prod 还未上线），改造时必须保证原 T7 测试用例（`service_test.go`）全部不退化（见 T10.9）。

> **本地数据库状态**：`monera_local` 已经跑过 migration 015，开发者本地需要手动 ALTER 补 KYT 字段（T10.1 给出命令）；prod 还未跑过任何 Phase 1 migration，部署时 015 一次到位。

---

## T1. 数据库迁移 + Seed [✅ 已完成]

**依赖**：无
**估时**：1d
**输出**：7 个迁移文件 + 启动后 schema 就位

**全局约束（plan §4 D-9）**：每个 Up SQL **必须自带幂等性**：
- `CREATE TABLE IF NOT EXISTS`
- `CREATE INDEX IF NOT EXISTS` / `CREATE UNIQUE INDEX IF NOT EXISTS`
- `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`
- `INSERT ... ON CONFLICT DO NOTHING`
- 约束：`DO $$ BEGIN ... IF NOT EXISTS ... END $$` 包裹 `ADD CONSTRAINT`

理由：`migrator.Migrate()` 按 version 跳过已 applied，但**单次迁移中途失败**（部分 SQL 执行了但 INSERT migrations 表没跑）会重跑同一 SQL → 必须 SQL 层兜底。参考 `internal/migration/migrations/014_*.go` 风格。

### T1.1 — Migration 015: `chains` 表

- 文件：`internal/migration/migrations/015_create_chains_table.go`
- Up：`CREATE TABLE chains (...)` 按 SPEC §4.1 完整 schema
- Down：`DROP TABLE chains`
- 注意 `network_family` 用 `VARCHAR(16)`，不创建 enum（保持简单）

**DoD**：
- [ ] `\d chains` 显示所有字段、类型与 SPEC §4.1 一致
- [ ] Down 能干净回滚

### T1.2 — Migration 016: `coins` 表

- 文件：`016_create_coins_table.go`
- 按 SPEC §4.2 schema

**DoD**：
- [ ] `\d coins` 字段与 SPEC §4.2 一致
- [ ] `symbol` 字段 UNIQUE

### T1.3 — Migration 017: `coin_chains` 表

- 文件：`017_create_coin_chains_table.go`
- 按 SPEC §4.3 schema，含 3 个索引
- 外键约束 `coin_id REFERENCES coins(id)`, `chain_code REFERENCES chains(code)`

**DoD**：
- [ ] `\d coin_chains` 字段、外键、3 个索引、UNIQUE(chain_code, coin_id) 与 SPEC §4.3 一致

### T1.4 — Migration 018: `address_pool` 表

- 文件：`018_create_address_pool_table.go`
- 按 SPEC §4.4 schema，含 2 个索引、UNIQUE(network_family, address)、`customer_ref_id` UNIQUE

**DoD**：
- [ ] `\d address_pool` 与 SPEC §4.4 一致

### T1.5 — Migration 019: `safeheron_webhook_events` 表

- 文件：`019_create_safeheron_webhook_events_table.go`
- 按 SPEC §4.5 schema，含 `event_id` UNIQUE、2 个索引、`raw_payload` JSONB

**DoD**：
- [ ] `\d safeheron_webhook_events` 与 SPEC §4.5 一致

### T1.6 — Migration 020: `deposits` 扩展 + `account` UNIQUE

- 文件：`020_extend_deposits_for_safeheron.go`
- 7 个 `ADD COLUMN`（按 SPEC §4.6）
- 部分唯一索引 `idx_deposits_safeheron_tx_key ON (safeheron_tx_key) WHERE safeheron_tx_key IS NOT NULL`
- CHECK 约束 `ck_deposits_status` 含 6 个状态
- 新增 `idx_account_user_currency ON account(user_id, currency)` UNIQUE
- Down：DROP CONSTRAINT + DROP INDEX + DROP COLUMN 全部回滚

**DoD**：
- [ ] `\d deposits` 含 `safeheron_tx_key/safeheron_coin_key/chain_code/coin_chain_id/block_height/block_hash/safeheron_status/safeheron_sub_status/status_rank/credited_at/failed_reason` 全部字段
- [ ] `SELECT indexdef FROM pg_indexes WHERE indexname='idx_deposits_safeheron_tx_key'` 含 `WHERE` 子句
- [ ] `\d account` 含 `idx_account_user_currency`
- [ ] CHECK 约束 `ck_deposits_status` 拒绝写入 `WRONG_STATUS`

### T1.7 — Migration 021: Seed (chains + coins + coin_chains)

- 文件：`021_seed_safeheron_phase1.go`
- Up 内嵌完整 SQL：
  - `INSERT INTO chains` 3 行（ETHEREUM/BSC/TRON）
  - `INSERT INTO coins` 5 行（ETH/BNB/TRX/USDT/USDC）
  - 读 `os.Getenv("APP_ENV")`：
    - `"production"` → `INSERT INTO coin_chains` 8 行（mainnet，按 SPEC §4.7）
    - 其余 → `INSERT INTO coin_chains` 3 行（testnet，按 SPEC §4.7）
- 所有 INSERT 用 `ON CONFLICT DO NOTHING`（幂等）
- Down：`DELETE FROM coin_chains; DELETE FROM coins; DELETE FROM chains;`

**DoD**：
- [ ] `APP_ENV=production` 启动后 `SELECT count(*) FROM coin_chains` = 8
- [ ] `APP_ENV=local` 启动后 `SELECT count(*) FROM coin_chains` = 3
- [ ] `SELECT safeheron_coin_key FROM coin_chains ORDER BY id` 与 SPEC §4.7 字面量完全一致（特别注意 `USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET`、`USDCOIN_ERC20_ETHEREUM_SEPOLIA`）
- [ ] 重复跑迁移不报错（幂等）

### T1.8 — `.env.example` 更新

- 文件：`.env.example`
- 按 `docs/plans/safeheron-phase1-plan.md` §3.10 追加全部新增变量，私钥占位符 `<paste-pem-here>`
- 注释每个变量的取值约束

**DoD**：
- [ ] `git diff .env.example` 显示新增 14 个变量
- [ ] 占位符无真实密钥

---

## T2. Safeheron SDK Adapter [✅ 已完成]

**依赖**：无（但 §3.2 的 SDK 决策见 plan.md 已锁定）
**估时**：0.5d
**输出**：`internal/safeheron/` 包 + 单测

### T2.1 — 添加 SDK 依赖

- `go get github.com/Safeheron/safeheron-api-sdk-go@latest`
- Import 路径已锁定（plan §4 D-1）：
  - `github.com/Safeheron/safeheron-api-sdk-go/safeheron`
  - `github.com/Safeheron/safeheron-api-sdk-go/safeheron/api`
  - `github.com/Safeheron/safeheron-api-sdk-go/safeheron/webhook`

**DoD**：
- [ ] `go.mod` 含 safeheron-api-sdk-go
- [ ] `go build ./...` 不报错

### T2.2 — `internal/safeheron/client.go`

实现 `Client` struct，包装 SDK。**关键约束**（plan §4 D-3）：SDK 配置字段 `RsaPrivateKey` / `SafeheronRsaPublicKey` 接受**文件路径**而非 PEM 字符串。Phase 1 实现：

```go
type Config struct {
    BaseURL                    string
    APIKey                     string
    PrivateKeyPEM              string  // env 传字符串
    PlatformPublicKeyPEM       string
    WebhookPublicKeyPEM        string
    WebhookPrivateKeyPEM       string  // 解密用
    RequestTimeoutMS           int64
}

type Client struct {
    accountAPI  api.AccountApi
    coinAPI     api.CoinApi
    txAPI       api.TransactionApi
    webhookConv webhook.WebhookConverter
    tempFiles   []string  // 退出时清理
}

func NewClient(cfg Config) (*Client, error) {
    // 1. 把 4 个 PEM 字符串写入 /tmp/safeheron-{private,platform,whpub,whpriv}-{pid}-{rand}.pem
    //    权限 0600；记录路径到 tempFiles
    // 2. 用 safeheron.Client{Config: safeheron.ApiConfig{...}} 构造
    // 3. 用 webhook.WebhookConverter{Config: webhook.WebHookConfig{...}} 构造
}

func (c *Client) Close() error  // 删除 tempFiles

// 业务方法（不暴露 SDK 类型）
func (c *Client) CreateAssetWallet(ctx, req) (*Wallet, error)
func (c *Client) AddCoin(ctx, accountKey string, coinKeyList []string) error
func (c *Client) ListAccountCoin(ctx, accountKey string) ([]Coin, error)
func (c *Client) GetAccountByAddress(ctx, address string) (*Account, error)
func (c *Client) WebhookConvert(rawBody []byte) (plaintext string, err error)
```

参考实测代码 `~/scratch/safeheron-sandbox-test/client.go:137-170`。

**DoD**：
- [ ] `Client` 实现以上 6 个方法 + `Close()`
- [ ] `NewClient` 缺失任一 env 返回 error；临时文件写入失败也返回 error
- [ ] 临时文件权限 `0600`，程序退出时 `Close()` 删除
- [ ] 单测验证：构造 → 临时文件存在 → Close → 文件删除

### T2.3 — `internal/safeheron/types.go`

定义 Go 友好的请求/响应类型，**不**直接暴露 SDK 类型给业务层。

**DoD**：
- [ ] 业务代码引用 `safeheron.Wallet` / `safeheron.Coin` 等本包类型，不引用 SDK 包

### T2.4 — Webhook 验签单测

- 文件：`internal/safeheron/client_test.go`
- 用 `~/scratch/safeheron-sandbox-test/results/` 实测产出的 sample payload + Safeheron 平台公钥 fixture
- 测试用例：
  - [ ] (a) 正常 payload → `WebhookConvert` 返回明文含 `eventType` 字段
  - [ ] (b) 篡改 `sig` → 返回 error
  - [ ] (c) 篡改 `bizContent` → 返回 error
  - [ ] (d) 缺少字段 → 返回 error

**DoD**：
- [ ] `go test ./internal/safeheron/... -run Webhook -v` 4 个用例全过
- [ ] `go test ./internal/safeheron/... -cover` ≥ 80%

---

## T3. Registry [✅ 已完成]

**依赖**：T1
**估时**：0.5d
**输出**：`internal/wallet/config/` 包

### T3.1 — `internal/wallet/config/{chain,coin_chain}.go`

定义 `Chain` / `Coin` / `CoinChain` model + DB row 映射

**DoD**：
- [ ] 字段与 SPEC §5.1 一致
- [ ] `CoinChain` 含 `Chain *Chain` 和 `Coin *Coin` 引用

### T3.2 — `internal/wallet/config/repository.go`

DB 访问层，从 3 张表 LOAD 全部 enabled 行

```go
func (r *Repo) LoadAll(ctx) (chains []*Chain, coins []*Coin, coinChains []*CoinChain, err error)
```

**DoD**：
- [ ] 一次性 3 个 SELECT，不在循环里 N+1
- [ ] 内联 join 不必要，先各自取再在 Go 里组装

### T3.3 — `internal/wallet/config/registry.go`

按 SPEC §5.1 实现 6 个 map + 2 个公开方法 + 后台刷新

**关键实现要点**：
- `Load` 失败：第一次（启动时）返回 error；后续刷新失败保留旧值
- 整体替换：用临时变量构建新 map，最后一次 atomic store
- 刷新间隔从 env `WALLET_CONFIG_REFRESH_INTERVAL` 读，默认 60s

**DoD**：
- [ ] `Registry.Get*` 系列方法对应 6 个查询路径
- [ ] `StartBackgroundRefresh(ctx)` 启动 goroutine
- [ ] 单测覆盖：(a) Load 成功; (b) Load 失败保留旧值 + 告警 hook 被调用; (c) 并发读写安全（race detector 通过）

### T3.4 — 接入 `internal/container/`

`container.NewContainer()` 内：
- 创建 Registry 实例
- 同步 `Load()`，失败 panic
- 启动 `StartBackgroundRefresh(ctx)`

**DoD**：
- [ ] 启动日志含 `Registry loaded: chains=3 coins=5 coin_chains=N`
- [ ] 60s 后看到 `Registry refresh OK`
- [ ] 故意把 `coin_chains` 表删掉重启 → panic 拒绝启动

---

## T4. `cmd/pool_init/main.go` 预生成脚本 [✅ 已完成]

**依赖**：T1, T2, T3
**估时**：0.5d
**输出**：cmd 程序 + sandbox 实测 1 个钱包

### T4.1 — `cmd/pool_init/main.go`

- 命令行参数：`--evm-count`（默认 100）、`--tron-count`（默认 100）、`--retry-errors`（重试 ERROR 状态的）、`--dry-run`
- 流程按 SPEC §6.1：
  1. Load Registry
  2. 对每个 `network_family in [EVM, TRON]` 循环 N 次：
     - 生成 UUID 作 `customer_ref_id`
     - 调 `safeheronClient.CreateAssetWallet`（参数固定：accountTag=DEPOSIT, hiddenOnUI=true, autoFuel=false）
     - 调 `safeheronClient.AddCoin(accountKey, registry.ListEnabledCoinChainsByFamily(family).MapToSafeheronKeys())`
     - 写 `address_pool` (status=AVAILABLE)
  3. 失败重试 5s/30s/120s，3 次后落 `status=ERROR`
- 进度输出：每 10 个钱包打一行 `[EVM] 50/100 done`

**DoD**：
- [ ] `go run ./cmd/pool_init --dry-run` 不写 DB，打印 200 个预期 customer_ref_id
- [ ] sandbox 真跑 `--evm-count=1 --tron-count=1`：
  - `SELECT count(*) FROM address_pool` = 2
  - EVM 那行 `SELECT safeheron_account_key, address FROM address_pool WHERE network_family='EVM'` 返回真实 `account*` 和 `0x...`
  - Safeheron 控制台对应钱包可见，AddCoin 列表含 §2.3.2 的 3 个 testnet coinKey（local env）或 §2.3.1 的 mainnet 列表（prod）
- [ ] 强制制造一次 AddCoin 失败（用错误 coinKey）→ 地址进 `status=ERROR`，日志告警

---

## T5. Pool Manager + Replenisher [✅ 已完成]

**依赖**：T1, T2, T3, T4
**估时**：0.5d
**输出**：`internal/wallet/pool/` 包

### T5.1 — `internal/wallet/pool/repository.go`

实现：
- `GetUserAddress(ctx, userID, networkFamily) (*Address, error)` — 查已分配
- `AssignAvailable(ctx, userID, networkFamily) (*Address, error)` — 单事务内 SELECT FOR UPDATE SKIP LOCKED + UPDATE
- `CountByStatus(ctx, networkFamily, status) (int, error)` — 给补水用
- `BulkInsert(ctx, addrs []*Address) error` — pool_init 用

**DoD**：
- [ ] `AssignAvailable` 单测：模拟并发 10 调用，断言获得 10 个不同 ID
- [ ] 池子空了 → 返回 sentinel error `ErrPoolEmpty`

### T5.2 — `internal/wallet/pool/manager.go`

业务层 service：
- `GetOrAssign(ctx, userID, networkFamily)` — 先查已分配，无则 AssignAvailable
- 池空 → 同步触发一次补水（边界 case，避免 demo 时硬卡死），再尝试一次

**DoD**：
- [ ] 单测覆盖：(a) 同用户两次返回同地址; (b) 不同用户拿不同地址; (c) 池空触发补水后成功; (d) 池空且补水失败返回明确 error

### T5.3 — `internal/wallet/pool/replenisher.go`

**不复用** `InterestScheduler`（plan §4 D-6：它是每日 UTC 阻塞 `time.Sleep`，不适合 10 分钟周期）。新建独立实现：

```go
type Replenisher struct {
    mgr      *Manager
    interval time.Duration
    low      map[string]int  // family → low watermark
    target   map[string]int  // family → target capacity
    log      Logger
}

func (r *Replenisher) Run(ctx context.Context) {
    ticker := time.NewTicker(r.interval)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done(): return
        case <-ticker.C:
            r.tick(ctx)  // 内部 recover panic
        }
    }
}
```

要点：
- 周期：env `POOL_REPLENISH_INTERVAL` 默认 10m
- 检查 EVM + TRON 两个 family
- 低于水位时调 T4 的预生成逻辑（**抽公共方法到 `pool.Manager.Replenish(family, target)`**，避免代码复制）
- 单次补水失败：`logger.Error` + alert，不阻塞下次 tick
- panic 时 `recover` + 日志，不让 goroutine 死掉

**DoD**：
- [ ] 启动日志含 `pool replenisher started: interval=10m low=EVM:50,TRON:50 target=EVM:100,TRON:100`
- [ ] 手动 `DELETE FROM address_pool WHERE id IN (...)` 让 EVM 降到 49，下一次 tick（≤10min）看日志 `pool replenish: EVM 49→100`，DB 实际补到 100
- [ ] 单测：模拟 `Manager.Replenish` panic，replenisher 不退出

---

## T6. 用户侧 API + Vercel 路由 [✅ 已完成]

**依赖**：T5
**估时**：0.5d
**输出**：2 个新端点 + 老端点 410 + Vercel 配置

### T6.1 — `internal/handlers/wallet_handler.go` 改造

新增方法：
- `GetDepositAddress(c *gin.Context)` — 读 query `network_family`，验证 in [EVM, TRON]，调 `pool.Manager.GetOrAssign`，返回 JSON
- `GetSupportedChains(c *gin.Context)` — 从 Registry 读所有 enabled coin_chains，按 chain 分组返回

老方法改造：
- `CreateWallet` / `AddAddress` / `GetAddressInfo` / `IncomeHistory` 直接返回 410 Gone：
  ```json
  {"error":"DEPRECATED","message":"Use GET /api/wallet/deposit-address instead"}
  ```

**DoD**：
- [ ] `curl /api/wallet/deposit-address?network_family=EVM` 带 JWT 返回 SPEC §8.1 示例 JSON 结构（含 address/networkFamily/supportedCoins）
- [ ] `network_family=XYZ` 返回 400
- [ ] `curl POST /api/wallet/create` 返回 HTTP 410 + 提示
- [ ] 响应字段全部 camelCase

### T6.2 — `internal/routes/routes.go` 注册

加 2 个新路由（JWT middleware 保护）：

```go
r.GET("/api/wallet/deposit-address", authMiddleware, walletHandler.GetDepositAddress)
r.GET("/api/wallet/supported-chains", authMiddleware, walletHandler.GetSupportedChains)
```

**DoD**：
- [ ] 路由表打印（启动日志）含这两行
- [ ] 未带 JWT 的请求返回 401

### T6.3 — `api/[...route].ts` ROUTE_CONFIG

按 SPEC §8.3 追加 3 行：

```ts
'GET /api/wallet/deposit-address':  { requiresAuth: true,  backendPath: '/api/wallet/deposit-address' },
'GET /api/wallet/supported-chains': { requiresAuth: true,  backendPath: '/api/wallet/supported-chains' },
'POST /api/webhooks/safeheron':     { requiresAuth: false, backendPath: '/api/webhooks/safeheron' },
```

**注意**：旧的 `POST /api/wallet/create` 等条目**保留**（让前端旧调用走到 410 上）。

**DoD**：
- [ ] `npm run test -- api/__route__.test.ts` 通过（如有相关用例，更新断言）
- [ ] Vercel 本地 dev 跑 `curl localhost:3000/api/wallet/deposit-address?network_family=EVM` 正确代理到 Go

### T6.4 — 并发分配集成测试

`internal/handlers/wallet_handler_test.go` 新增：

- [ ] 10 个用户并发调 GetDepositAddress(EVM) → 10 个不同 address
- [ ] 同用户串行 2 次 → 同 address
- [ ] 池空 → 触发补水或返回 503

---

## T7. Webhook 处理（同步 + 异步 worker） [✅ 已完成]

**依赖**：T1, T2, T3, T6
**估时**：1.5d
**输出**：`internal/wallet/deposit/` 包 + `safeheron_webhook_handler.go` + 告警

### T7.1 — `internal/wallet/deposit/repository.go`

实现：
- `InsertEventOrSkip(ctx, evt) (inserted bool, err error)` — INSERT ... ON CONFLICT (event_id) DO NOTHING
- `LockNextPendingEvent(ctx, tx) (*Event, error)` — SELECT FOR UPDATE SKIP LOCKED LIMIT 1
- `UpsertDeposit(ctx, tx, d) error` — SPEC §6.4 的 INSERT ... ON CONFLICT (safeheron_tx_key) DO UPDATE WHERE status_rank <= EXCLUDED.status_rank
- `MarkEventDone(ctx, tx, id, err) error`
- `CreditAccount(ctx, tx, userID, currency, amount) (newBalance string, err error)` — `INSERT ... ON CONFLICT (user_id, currency) DO UPDATE SET balance = account.balance + EXCLUDED.balance`
- `WriteJournal(ctx, tx, ...) error`

**DoD**：
- [ ] 单测：(a) ON CONFLICT 静默 0 行（status_rank 回退）; (b) UPSERT 正向更新; (c) 并发 5 个 worker 拉同一行 → 只一个拿到

### T7.2 — `internal/wallet/deposit/service.go`

核心入账状态机，**单事务**包裹 SPEC §6.4 全流程：

```go
func (s *Service) ProcessEvent(ctx, eventID int) error {
    // BEGIN tx
    // 1. LockNextPendingEvent
    // 2. Parse eventDetail
    // 3. eventType 过滤 → DONE 跳过
    // 4. transactionDirection != INFLOW → DONE 跳过
    // 5. 路由：address_pool 查找
    // 6. Registry 查 coinChain
    // 7. min_deposit 校验
    // 8. UpsertDeposit
    // 9. IF COMPLETED + CONFIRMED + status=PENDING → CreditAccount + WriteJournal + UPDATE deposits
    // 10. IF FAILED/CANCELLED/REJECTED → UPDATE deposits + alert
    // 11. MarkEventDone
    // COMMIT
}
```

**关键测试用例**（必须全过）：
- [ ] 完整 COMPLETED 流程 → CREDITED + balance 增加 + journal 写入
- [ ] 同 (txKey, status) 重发 → ON CONFLICT 静默，balance 不变
- [ ] COMPLETED 后再来 CONFIRMING → status_rank=100 不动
- [ ] 地址无主 → MANUAL_REVIEW + 告警
- [ ] coinKey 未注册 → MANUAL_REVIEW + 告警
- [ ] 金额低于 min → MANUAL_REVIEW + 告警
- [ ] FAILED 终态 → status=FAILED + 告警，无 journal
- [ ] eventType 不在白名单 → DONE 跳过，不入账
- [ ] transactionDirection=OUTFLOW → DONE 跳过

### T7.3 — `internal/wallet/deposit/worker.go`

后台 goroutine：

```go
func (w *Worker) Run(ctx context.Context) {
    ticker := time.NewTicker(1 * time.Second)
    for {
        select {
        case <-ctx.Done(): return
        case <-ticker.C:
            for {
                processed, err := w.processOne(ctx)
                if !processed { break }
                if err != nil { log.Error(...); break }
            }
        }
    }
}
```

**DoD**：
- [ ] 启动日志 `deposit worker started`
- [ ] worker panic 时不阻塞主进程（recover + 日志 + 1s 后重启）
- [ ] 优雅停止：context 取消后 worker 退出

### T7.4 — `internal/handlers/safeheron_webhook_handler.go`

同步部分（按 SPEC §6.4）：

```go
func (h *Handler) Receive(c *gin.Context) {
    body, _ := io.ReadAll(c.Request.Body)
    plaintext, err := h.client.WebhookConvert(body)
    if err != nil {
        log.Warn("webhook verify failed", "err", err)
        c.AbortWithStatus(401)
        return
    }

    var evt webhookEvent  // {eventType, eventDetail{txKey, transactionStatus, ...}}
    json.Unmarshal([]byte(plaintext), &evt)

    eventID := evt.EventDetail.TxKey + ":" + evt.EventDetail.TransactionStatus
    h.repo.InsertEventOrSkip(ctx, &Event{
        EventID: eventID,
        EventType: evt.EventType,
        SafeheronTxKey: evt.EventDetail.TxKey,
        CustomerRefId: evt.EventDetail.CustomerRefId,
        RawPayload: plaintext,
    })

    // 严格 ack body
    c.Header("Content-Type", "application/json")
    c.String(200, `{"code":"200","message":"SUCCESS"}`)
}
```

**关键 DoD**：
- [ ] **ack body 字面量必须用单测断言**：`assert.Equal(t, `{"code":"200","message":"SUCCESS"}`, w.Body.String())`
- [ ] 验签失败 → 401 + 不写 DB
- [ ] 重复 event_id INSERT 静默 → 仍返回 200 + 标准 ack
- [ ] handler 整个流程 P99 < 2s（用 benchmark 测）

### T7.5 — Webhook 路由注册

`internal/routes/routes.go`：

```go
r.POST("/api/webhooks/safeheron", webhookHandler.Receive)  // NO authMiddleware
```

**DoD**：
- [ ] 路由表无 JWT
- [ ] 启动后 `curl -X POST http://localhost:8080/api/webhooks/safeheron -d 'garbage'` 返回 401（验签失败）

### T7.6 — 告警发送

`internal/services/alert_service.go`（新建）：

```go
type AlertService struct {
    feishuURL string
    emailRecipients []string
    httpClient *http.Client
    emailSvc *EmailService
}

func (a *AlertService) Send(ctx, level, title, fields map[string]string) error
```

- 飞书：POST 简单文本卡片到 `ALERT_WEBHOOK_URL`
- 邮件：复用 `email_service.go`
- 失败仅日志，不阻塞

**DoD**：
- [ ] 单测用 httptest server 验证发送格式
- [ ] 触发 MANUAL_REVIEW 时收到飞书消息 + 邮件（手动测）

### T7.7 — Vercel 路由

已在 T6.3 加 `POST /api/webhooks/safeheron`，检查 `requiresAuth: false`。

**DoD**：
- [ ] Vercel 本地跑 webhook 转发到 Go 后端成功

---

## T8. 前端切换 [✅ 已完成]

**依赖**：T6, T7
**估时**：0.5d
**输出**：前端调用切到新端点 + UI 适配

### T8.1 — `src/lib/wallet-service.ts` 改造

- 新增方法 `getDepositAddress(networkFamily: 'EVM' | 'TRON')`
- 新增方法 `getSupportedChains()`
- **删除**对老端点的调用（`createWallet` / `addAddress` / `getAddressInfo` / `getIncomeHistory`）

**DoD**：
- [ ] `grep -r "wallet/create\|wallet/addresses\|wallet/address/get" src/` 无业务代码命中（仅类型定义残留可接受）
- [ ] TypeScript 编译通过

### T8.2 — `Deposit.tsx` 重写

**已锁定**（plan §4 D-7）：改造目标是 `src/pages/dashboard/Deposit.tsx`（当前 94 行 "Coming Soon" 占位），**不动** `Addresses.tsx`（507 行提现地址白名单是二期）。

页面结构：
```tsx
<Tabs defaultValue="EVM">
  <TabsList>
    <TabsTrigger value="EVM">EVM (ETH/BSC)</TabsTrigger>
    <TabsTrigger value="TRON">TRON</TabsTrigger>
  </TabsList>
  <TabsContent value="EVM">
    <DepositAddressCard networkFamily="EVM" />
  </TabsContent>
  <TabsContent value="TRON">
    <DepositAddressCard networkFamily="TRON" />
  </TabsContent>
</Tabs>
```

`DepositAddressCard` 内：
- `useQuery(['deposit-address', family], () => walletService.getDepositAddress(family), { staleTime: 5 * 60_000 })`
- 显示 address + 复制按钮（用 `navigator.clipboard.writeText`）
- 二维码（可选，用现有 lib 或 `qrcode.react`，若引入新依赖需走 ASK FIRST）
- supportedCoins 表格：chainCode | symbol | minDeposit 三列
- Loading / Error 状态

i18n（同步加到 `en.json` + `zh.json`）：
- `deposit.tabs.evm` / `deposit.tabs.tron`
- `deposit.address.label` / `deposit.address.copy` / `deposit.address.copied`
- `deposit.supportedCoins.title` / `deposit.supportedCoins.chain` / `deposit.supportedCoins.coin` / `deposit.supportedCoins.minDeposit`
- 旧 `deposit.comingSoon.*` 保留到下个 release 清理

**DoD**：
- [ ] 浏览器打开 `/dashboard/deposit`，默认显示 EVM tab 含地址
- [ ] 切到 TRON tab 显示 TRON 地址
- [ ] supportedCoins 列表行数：local/test 环境 EVM tab 1 行（仅 Sepolia ETH/USDC 走 EVM）+ TRON tab 1 行；prod 环境 EVM tab 6 行 + TRON tab 2 行
- [ ] 复制按钮点击后 toast "已复制"
- [ ] 切换 i18n 中英文都正常
- [ ] `npm run test -- Deposit.test.tsx` 通过（更新现有测试）

### T8.3 — i18n 字符串

如新增 UI 文案，按项目惯例同时更新：
- `src/i18n/locales/en.json`
- `src/i18n/locales/zh.json`

**DoD**：
- [ ] 切换语言两边都显示正常

---

## T9. 充值页面 UX 重构（选币 → 选链 → 展示地址） [✅ 已完成]

**依赖**：T6（deposit-address 端点）, T8（前端 service + Deposit 页面）
**估时**：1d
**输出**：后端新增 `/api/wallet/deposit-coins` 端点 + `chains`/`coin_chains` 新增展示字段 + 前端 Deposit 页面重写为三步流程 + 测试重写

**全局约束**：
- 所有决策已在 `plan.md` §4 D-13 ~ D-30 锁定，施工时若发现与代码冲突先停并提出
- Migration 沿用 T1 D-9 幂等约束（`ADD COLUMN IF NOT EXISTS` + `UPDATE ... WHERE col IS NULL`）
- 不动 `Addresses.tsx`（提现白名单二期）
- 不改 lazy assign 行为（激活预分配作为独立 ticket）

### T9.1 — Migration 022（DB 展示字段）

**目标**：`coin_chains` 新增 `token_standard` + `estimated_arrival_minutes`；`chains` 新增 `short_name`；存量行 seed 默认值。

**文件**：
- 新增 `internal/migration/migrations/022_add_deposit_display_fields.go`
- 修改 `cmd/migrate/main.go`（注册 022）

**实现要点**：
- Up SQL：3 个 `ADD COLUMN IF NOT EXISTS` + 9 个 `UPDATE ... WHERE col IS NULL`（seed 值见 SPEC §4.7.1）
- Down SQL：3 个 `DROP COLUMN IF EXISTS`

**DoD**：
- [ ] `DATABASE_URL=postgresql://linden@localhost/monera_local?sslmode=disable go run cmd/migrate/main.go` 显示 022 状态 applied
- [ ] `psql monera_local -c "SELECT code, short_name FROM chains;"` 三行都有值
- [ ] `psql monera_local -c "SELECT chain_code, symbol, token_standard, estimated_arrival_minutes FROM coin_chains;"` 三行都有值
- [ ] 重跑 migration（幂等）不报错

### T9.2 — 后端 models + repository + registry

**目标**：把新增 DB 列加载进 Registry。

**文件**：
- 修改 `internal/wallet/config/models.go`：`Chain` 加 `ShortName string`；`CoinChain` 加 `TokenStandard string`、`EstimatedArrivalMinutes int`
- 修改 `internal/wallet/config/repository.go`：`loadChains` / `loadCoinChains` 两个 SELECT 加新列；用 `COALESCE(col, '' / 0)` 防 NULL
- 修改 `internal/wallet/config/registry.go`：新增 `AllEnabledCoinChains() []*CoinChain`（聚合 `byChain` 所有切片）和 `AllCoins() []*Coin`（filter `enabled=true`）

**DoD**：
- [ ] `go build ./...` 编译通过
- [ ] 启动 server 日志显示 `Registry loaded: chains=3 coins=5 coin_chains=3`，不报 "column does not exist"
- [ ] `go test ./internal/wallet/config/... -race` 通过

### T9.3 — GetDepositCoins handler + 路由 + Go 测试

**目标**：新增 `GET /api/wallet/deposit-coins` 端点，按 coin 分组返回 networks。

**文件**：
- 修改 `internal/handlers/deposit_address_handler.go`：
  - `ChainsRegistry` 接口扩展 `AllCoins() []*Coin` + `AllEnabledCoinChains() []*CoinChain`
  - 新增响应类型 `coinNetwork` / `depositCoin` / `depositCoinsResponse`
  - 新增 `GetDepositCoins(c *gin.Context)` handler
- 修改 `internal/handlers/deposit_address_handler_test.go`：`mockChainsRegistry` 补 2 个新方法 + 4 个新测试
- 修改 `internal/handlers/setsafeheron_deps_test.go`：同步 mock 补全
- 修改 `internal/routes/routes.go`：`wallet.GET("/deposit-coins", h.GetDepositCoins)`
- 修改 `api/[...route].ts`：`ROUTE_CONFIG` 加 `'GET /api/wallet/deposit-coins'`

**Handler 逻辑**（详见 plan Slice 2 伪代码）：
1. `getUserID` → 401
2. `walletRegistry == nil` → 503
3. `AllEnabledCoinChains()` 按 `coin.symbol` 分组
4. 每 cc 转 `coinNetwork`：`tokenContract` 空字符串 → nil；`shortName` 空 → fallback `cc.Chain.Code`
5. 输出按 coin 首次出现的 `display_order` 升序
6. 200 OK

**Go 测试**（4 个新用例 + 旧用例不退化）：
- [ ] `TestGetDepositCoins_Unauthorized` → 401
- [ ] `TestGetDepositCoins_RegistryUnavailable` → 503
- [ ] `TestGetDepositCoins_GroupingAndShape` → 2 coin × 2 chain，断言响应结构、`tokenContract` null/string 正确、`shortName` fallback 正确
- [ ] `TestGetDepositCoins_EmptyRegistry` → `{"coins":[]}` 不是 null

**DoD**：
- [ ] `go test ./internal/handlers/... -race` 全绿（含旧用例不退化）
- [ ] `go vet ./...` 无新增问题
- [ ] 重启 server，`curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/wallet/deposit-coins | jq` 返回 3 个 coin（local 环境 ETH/USDC/TRX）
- [ ] 401 路径：`curl http://localhost:8081/api/wallet/deposit-coins -i | head -1` → `HTTP/1.1 401`

### T9.4 — 前端 wallet-service + i18n

**目标**：前端 service 加新类型 + 方法；i18n 重写 `deposit.*` 子树。

**文件**：
- 修改 `src/lib/wallet-service.ts`：加 `DepositCoinNetwork` / `DepositCoin` / `DepositCoinsResponse` 类型 + `WalletService.getDepositCoins()` 静态方法
- 修改 `src/i18n/locales/en.json` 和 `zh.json`：
  - **删除**：`deposit.tabs.*` / `deposit.supportedCoins.*` / `deposit.comingSoon.*` / `deposit.addressCard.{label,hint}` / `deposit.selectAsset` / `deposit.address` / `deposit.copy` / `deposit.copied` / `deposit.minDeposit` / `deposit.warning` / `deposit.testnetWarning` / `deposit.history` / `deposit.noHistory`
  - **新增**：`deposit.{steps, coinSelector, networkSelector, addressCard, details, recent}.*` 完整树（见 SPEC §8.4 i18n 命名空间）
  - **保留**：`deposit.title` / `deposit.description` / `deposit.status.*` / `deposit.activate*`

**DoD**：
- [ ] `npm run lint` 通过
- [ ] `npx tsc --noEmit` 类型检查通过
- [ ] 浏览器 dev tools console 无 i18n missing-key warning

### T9.5 — Deposit.tsx 重写

**目标**：把现 EVM/TRON tab 页改写为「选币 → 选链 → 展示地址」三步流程。

**文件**：
- 重写 `src/pages/dashboard/Deposit.tsx`（单文件 < 400 行）

**组件结构**（全部内联）：
```tsx
Deposit (page)
├── Header              // title + description
├── StepIndicator       // 手写 diamond + 序号 + 灰显逻辑
├── CoinSelector        // chip 列表 + CryptoIcon + Skeleton
├── NetworkSelector     // shadcn Select + 警告 Alert + 单网络自动选中
├── AddressDisplay      // QR + 复制 + 合约链接 + 详情区
└── RecentDeposits      // 右侧 sidebar 320px，sm 端折底部
```

**关键交互**（详见 SPEC §8.4 跳转规则）：
- 选币时清空 `selectedNetwork`，单网络 coin 用 useEffect 自动 setSelectedNetwork
- 非原生币显示「Contract address ends in {last4}」+ 外链按钮 → `${explorerUrl}/token/${tokenContract}`
- 详情区固定展开（不做 Accordion）
- RecentDeposits 用 `useQuery(['recent-deposits'], () => fetch('/api/deposits?limit=5'))`；TxID 链接 → `${explorerUrl}/tx/${txHash}`（用 deposit-coins 数据反查 chainCode→explorerUrl）

**辅助函数**（文件顶部）：
```ts
function truncateAddress(addr, head=6, tail=4): string
function explorerTokenUrl(explorerUrl, contract): string
function explorerTxUrl(explorerUrl, txHash): string
```

**DoD**（浏览器手测，对应 SPEC §11.1 F-T9-1 ~ F-T9-9）：
- [ ] `npm run dev`，访问 `/dashboard/deposit`：StepIndicator ① 高亮 ② ③ 灰显；看到 ETH / USDC / TRX chip
- [ ] 点 USDC：步骤 ② 激活；只 1 个网络自动选中并跳到 ③；地址 = `address_pool` 表中该用户 EVM 地址
- [ ] 详情区显示 "Minimum deposit: 0.1 USDC" + "Credited after: N network confirmations"
- [ ] 非原生币 USDC 显示「Contract ends in 6eB48」+ 跳转 etherscan 按钮 href 正确
- [ ] 点 ETH（原生币）：合约地址行消失；同 EVM 家族地址不变
- [ ] 点 TRX：StepIndicator 完整切换；TRON 地址显示正确
- [ ] 复制按钮点击后 toast「Address copied」
- [ ] 浏览器 console 无 React duplicate key / act() / missing-key warning
- [ ] 响应式：375px / 768px / 1440px 三档下布局不破

### T9.6 — Deposit.test.tsx 重写

**目标**：整文件重写测试，覆盖三步流程 + 边界。

**文件**：
- 重写 `src/pages/dashboard/Deposit.test.tsx`

**Mock 策略**：沿用旧文件 URL 解析 fetch mock，扩展到 3 个端点（`/wallet/deposit-coins` + `/wallet/deposit-address` + `/deposits`）；保留 `qrcode` mock。

**测试用例**（12 个，AAA 结构）：
1. renders step indicator with step 1 highlighted initially
2. lists deposit coins after load
3. selecting a coin activates step 2 and shows its networks
4. auto-selects the only network and shows the address
5. renders the QR code for the selected network's address
6. non-native coin shows contract address suffix and explorer link
7. native coin hides contract address row
8. switching coin resets the network selection
9. copies the address to clipboard
10. renders empty state when there are no recent deposits
11. shows skeletons while initial coin list is loading
12. shows error state when deposit-coins endpoint fails

**注意**：用 `npx vitest run src/pages/dashboard/Deposit.test.tsx`，不要用 `npm run test --`（避开 vitest_config_lets_node_modules_leak 那个坑）。

**DoD**：
- [ ] `npx vitest run src/pages/dashboard/Deposit.test.tsx` 全绿
- [ ] 12 个用例全覆盖
- [ ] 旧 Tabs 相关用例已删除
- [ ] 控制台无 React act() 警告

---

## T10. KYT 合规筛查（v1.5 spec 新增）

**依赖**：T7（webhook 入账闭环）；建议在 T9 之后施工以避免与前端冲突
**估时**：1.5d
**输出**：015 migration 追加 AML 字段 + `ProcessOne` 两阶段重构 + KytReport adapter + AML_KYT_ALERT webhook 分支 + 超时兜底扫描 + 前端文案

**全局约束**（plan.md §3.11 + §4 D-31~D-40）：
- DB 变动**全部并入 `015_safeheron_phase1.go`**，**不**新增 016+ migration（用户决策）
- ALTER SQL 用 `ADD COLUMN IF NOT EXISTS` 保持幂等（防 migrator 中途失败重跑）
- 本地 monera_local 数据库已经跑过 015，由开发者手动 ALTER 补字段（用户授权直接 SQL）
- 拆 `ProcessOne` 为三事务结构：(T-α) 锁事件 → (T-β KYT API，事务外) → (T-γ 入账/MR + 标 DONE)
- KYT 失败 / API error 不标 DONE，事件回 PENDING 自动重试，超过 `KYT_ORPHAN_ALERT_MAX_RETRY=100` 转 MR
- KYT API mirror 类型放 `internal/safeheron/types.go`，**不暴露 SDK 类型**
- **代码注释要详细**：明天用户去公司电脑配 Safeheron 密钥实测，注释清楚减少沟通歧义

### T10.1 — Migration 015 追加 AML 字段（不新增文件）

**目标**：本地手动 ALTER + 改 `015_safeheron_phase1.go` 让生产首次执行一次到位。

**文件**：
- 修改 `internal/migration/migrations/015_safeheron_phase1.go`

**Up 追加 SQL**（在现有 ALTER TABLE deposits 之后）：
```sql
-- T10 KYT 合规筛查字段（v1.5 spec 新增）
ALTER TABLE deposits
    ADD COLUMN IF NOT EXISTS aml_screening_state VARCHAR(16),
    ADD COLUMN IF NOT EXISTS aml_risk_level      VARCHAR(8),
    ADD COLUMN IF NOT EXISTS aml_evaluated_at    TIMESTAMP,
    ADD COLUMN IF NOT EXISTS aml_list            JSONB;

-- KYT_PENDING 状态: 链上 COMPLETED 但 KYT 评估未结束
ALTER TABLE deposits DROP CONSTRAINT IF EXISTS ck_deposits_status;
ALTER TABLE deposits ADD CONSTRAINT ck_deposits_status
    CHECK (status IN ('PENDING', 'CHAIN_VERIFYING', 'CHAIN_VERIFIED',
                      'KYT_PENDING',
                      'CREDITED', 'FAILED', 'MANUAL_REVIEW'));

-- 超时扫描索引（仅 KYT_PENDING 行，节省空间）
CREATE INDEX IF NOT EXISTS idx_deposits_kyt_pending
    ON deposits(updated_at)
    WHERE status = 'KYT_PENDING';
```

**Down 追加 SQL**：DROP 4 个 column + DROP index + 恢复原 CHECK 约束。

**本地数据库手动补字段命令**（开发者本地执行）：
```bash
psql "$LOCAL_DATABASE_URL" <<'EOF'
ALTER TABLE deposits
    ADD COLUMN IF NOT EXISTS aml_screening_state VARCHAR(16),
    ADD COLUMN IF NOT EXISTS aml_risk_level      VARCHAR(8),
    ADD COLUMN IF NOT EXISTS aml_evaluated_at    TIMESTAMP,
    ADD COLUMN IF NOT EXISTS aml_list            JSONB;
ALTER TABLE deposits DROP CONSTRAINT IF EXISTS ck_deposits_status;
ALTER TABLE deposits ADD CONSTRAINT ck_deposits_status
    CHECK (status IN ('PENDING','CHAIN_VERIFYING','CHAIN_VERIFIED','KYT_PENDING','CREDITED','FAILED','MANUAL_REVIEW'));
CREATE INDEX IF NOT EXISTS idx_deposits_kyt_pending ON deposits(updated_at) WHERE status='KYT_PENDING';
EOF
```

**DoD**：
- [ ] `psql $LOCAL_DATABASE_URL -c "\d deposits"` 显示 4 个新字段
- [ ] CHECK 约束包含 `KYT_PENDING`
- [ ] `psql -c "SELECT indexdef FROM pg_indexes WHERE indexname='idx_deposits_kyt_pending'"` 含 `WHERE` 子句
- [ ] 重跑 015 migration（幂等）不报错
- [ ] Down 能干净回滚

### T10.2 — Safeheron Adapter 扩展 KytReport

**目标**：项目内 mirror KYT 类型 + adapter 包装 SDK `ComplianceApi.KytReport()`。

**SDK 真实签名（已实测确认，无需明天再验）**：

源码位置：`/Users/linden/workbench/src/github.com/safeheron/safeheron-api-sdk-go/safeheron/api/compliance_api.go`

```go
// SDK 源码原文：
package api

type ComplianceApi struct {
    Client safeheron.Client   // 注意是 safeheron 包的 Client, 不是 *Client
}

type KytReportRequest struct {
    TxKey         string `json:"txKey,omitempty"`
    CustomerRefId string `json:"customerRefId,omitempty"`
}

type KytReportResponse struct {
    TxKey                      string      `json:"txKey"`
    CustomerRefId              string      `json:"customerRefId"`
    AmlScreeningTriggeredState string      `json:"amlScreeningTriggeredState"`
    AmlList                    []AmlReport `json:"amlList"`
}

type AmlReport struct {
    Provider       string `json:"provider"`
    Timestamp      string `json:"timestamp"`
    Status         string `json:"status"`
    RiskLevel      string `json:"riskLevel"`
    LastUpdateTime string `json:"lastUpdateTime"`
    Payload        any    `json:"payload"`   // SDK 用 any, 项目 mirror 用 json.RawMessage
}

// 关键：方法签名不带 ctx (与现有 CoinApi.ListCoin / AccountApi.* 一致)
func (e *ComplianceApi) KytReport(d KytReportRequest, r *KytReportResponse) error {
    return e.Client.SendRequest(d, r, "/v1/compliance/kyt/report")
}
```

**改造文件**（确认现有目录布局：`internal/safeheron/` 下只有 `iface.go` + `client.go` + `types.go` + `client_test.go`，**没有** `safeheron_client.go` / `client_impl.go`）：

1. **修改 `internal/safeheron/types.go`**：新增 mirror 类型
  ```go
  // KytReportResponse 是 /v1/compliance/kyt/report 的项目内 mirror,
  // 与 SDK api.KytReportResponse 字段对齐, 但不暴露 SDK 类型给业务层。
  type KytReportResponse struct {
      TxKey                      string      `json:"txKey"`
      CustomerRefID              string      `json:"customerRefId"`
      // IN_PROGRESS / TRIGGERED / UNTRIGGERED, 见 SPEC §6.5.1
      AmlScreeningTriggeredState string      `json:"amlScreeningTriggeredState"`
      AmlList                    []AmlReport `json:"amlList"`
  }

  // AmlReport 是单个 provider (MistTrack / Chainalysis / Elliptic) 的筛查结果。
  // Phase 1 只接 MistTrack 一家, AmlList 数组长度恒为 1。
  type AmlReport struct {
      Provider       string          `json:"provider"`        // MistTrack / Chainalysis / Elliptic
      Timestamp      string          `json:"timestamp"`       // UNIX 毫秒
      Status         string          `json:"status"`          // PENDING / COMPLETED / SKIPPED / FAILED
      RiskLevel      string          `json:"riskLevel"`       // LOW / MEDIUM / HIGH / SEVERE / UNKNOWN
      LastUpdateTime string          `json:"lastUpdateTime"`  // UNIX 毫秒
      // 各 provider 详细数据 (SDK 是 any, mirror 用 RawMessage 避免业务层意外解析),
      // 业务层仅存档到 deposits.aml_list JSONB
      Payload        json.RawMessage `json:"payload"`
  }
  ```

2. **修改 `internal/safeheron/iface.go`**：`SafeheronClient` 接口加方法
  ```go
  // KytReport 查询交易的 KYT/AML 筛查报告。
  //
  // 调用时机仅两处 (Phase 1 主路径配合 AML_KYT_ALERT webhook 使用, 节省 API 配额):
  //   1. ProcessOne 初查 (COMPLETED+CONFIRMED 时)
  //   2. 超时兜底扫描 (KYT_PENDING 超过 KYTTimeout 后)
  // 详见 SPEC §6.5.3。
  //
  // 注意: ctx 在此接口层保留 (内部不会传给 SDK——SDK 方法不接受 ctx),
  // 仅用于 caller 端的取消传递和未来扩展。
  KytReport(ctx context.Context, txKey string) (*KytReportResponse, error)
  ```

3. **修改 `internal/safeheron/client.go`**：`Client` struct 实现 adapter
  ```go
  // 在文件顶部 import 块加 (与现有 import 别名风格一致, 现有用的就是 api):
  //   safeheronapi "github.com/Safeheron/safeheron-api-sdk-go/safeheron/api"
  // 实际上现有 client.go 直接用 `api` 别名, 但 deposit 包等业务层也用了 api 别名
  // 容易冲突——KYT 这块用 `safeheronapi` 别名更安全, 不影响现有方法。

  func (c *Client) KytReport(_ context.Context, txKey string) (*KytReportResponse, error) {
      complianceAPI := safeheronapi.ComplianceApi{Client: c.sdkClient}
      var sdkResp safeheronapi.KytReportResponse
      // SDK 方法不带 ctx——已实测确认 (compliance_api.go:33)
      if err := complianceAPI.KytReport(safeheronapi.KytReportRequest{TxKey: txKey}, &sdkResp); err != nil {
          return nil, fmt.Errorf("safeheron KytReport txKey=%s: %w", txKey, err)
      }
      // SDK → 项目 mirror 类型转换 (隔离 SDK 类型)
      out := &KytReportResponse{
          TxKey:                      sdkResp.TxKey,
          CustomerRefID:              sdkResp.CustomerRefId,  // SDK 用 CustomerRefId, mirror 用 CustomerRefID
          AmlScreeningTriggeredState: sdkResp.AmlScreeningTriggeredState,
          AmlList:                    make([]AmlReport, 0, len(sdkResp.AmlList)),
      }
      for _, r := range sdkResp.AmlList {
          payload, _ := json.Marshal(r.Payload)   // SDK any → mirror RawMessage
          out.AmlList = append(out.AmlList, AmlReport{
              Provider:       r.Provider,
              Timestamp:      r.Timestamp,
              Status:         r.Status,
              RiskLevel:      r.RiskLevel,
              LastUpdateTime: r.LastUpdateTime,
              Payload:        payload,
          })
      }
      return out, nil
  }
  ```

  **⚠️ 重要：必须先新增 `sdkClient` 字段**（实测 `client.go:35-39` 确认）：

  现有 `Client` struct **只有 3 个字段**：
  ```go
  type Client struct {
      account     accountAPIClient   // *api.AccountApi 实现
      webhookConv webhookConverter
      tempFiles   []string
  }
  ```

  现有 `NewClient` 里的 `baseClient := sdk.Client{...}`（client.go:67-73）是**局部变量**，没存进 struct。要实现 `KytReport`，**必须**：

  1. 在 `Client` struct 加字段：
     ```go
     type Client struct {
         account     accountAPIClient
         webhookConv webhookConverter
         tempFiles   []string
         sdkClient   sdk.Client   // 新增 — 给 ComplianceApi 等后续 API 复用
     }
     ```

  2. 在 `NewClient` 内 `baseClient := sdk.Client{...}` 之后加一行赋值：
     ```go
     c := &Client{
         account:   sdkAccount,
         tempFiles: tempFiles,
         sdkClient: baseClient,   // 新增
     }
     ```

  3. `KytReport` 实现里用 `c.sdkClient`（而非 `c.account.Client`，因为 `accountAPIClient` 是窄接口不暴露底层 Client）：
     ```go
     complianceAPI := safeheronapi.ComplianceApi{Client: c.sdkClient}
     ```

4. **`internal/safeheron/client_test.go` 新增测试**（保持现有测试文件，不另起 `client_kyt_test.go`）：
   - mock 一个实现 `safeheron.Client.SendRequest` 行为的 fake，断言：(a) 正常 path → 字段一一对应；(b) `sdkResp.AmlList[].Payload any` → mirror `json.RawMessage` 序列化正确；(c) SDK 返回 error → adapter 包装错误信息含 txKey

**DoD**：
- [ ] `go build ./...` 通过
- [ ] `internal/safeheron/client_test.go` 新增 3 个 KytReport 用例全过
- [ ] 业务代码（deposit 包）只 import `internal/safeheron`，不直接 import `safeheron-api-sdk-go/safeheron/api`
- [ ] `go test ./internal/safeheron/... -race -cover` 覆盖率 ≥ 80%
- [ ] `grep -r "safeheronapi" internal/` 只命中 `internal/safeheron/client.go`（不污染业务层）

### T10.3 — KYT 决策核心 (`internal/wallet/deposit/kyt.go`)

**目标**：实现 `SummarizeRiskLevel` 函数 + 风险等级常量 + 告警级别映射。

**文件**：新建 `internal/wallet/deposit/kyt.go`

```go
package deposit

import "monera-digital/internal/safeheron"

// KYT 风险等级 (业务汇总, 来自 SPEC §6.5.1 处置矩阵)。
// SummarizeRiskLevel 的返回值, 也是 deposits.aml_risk_level 列的取值。
const (
    KytLow     = "LOW"      // 全部 provider COMPLETED 且最高 riskLevel=LOW → CREDITED
    KytMedium  = "MEDIUM"
    KytHigh    = "HIGH"
    KytSevere  = "SEVERE"
    KytUnknown = "UNKNOWN"
    KytFailed  = "FAILED"   // 任一 provider status=FAILED, 服务商不可用
    KytSkipped = "SKIPPED"  // 任一 provider 被人工跳过
    KytPending = "PENDING"  // 任一 provider status=PENDING, 尚未完成评估
)

// KYT 失败原因码 (写入 deposits.failed_reason, 用于运营人工审核分辨)
//
// 命名约定 (I-5 决策, 选项 A):
//   - 初查路径 (ProcessOne 内): 不带后缀 — 例如 KYT_UNTRIGGERED / KYT_RISK_HIGH
//   - 超时兜底路径 (ScanKYTTimeouts 内): 带 _AFTER_TIMEOUT 后缀
//   - 这样运营从 failed_reason 一眼就能区分: 初查直接转人工 vs 等了 20min 仍未完成才转人工
//     后者意味着 Safeheron / MistTrack 响应慢, 可能需要联系 Safeheron support
const (
    // ============ 初查路径 (ProcessOne, COMPLETED+CONFIRMED 时调用) ============
    ReasonKytUntriggered          = "KYT_UNTRIGGERED"           // amlScreeningTriggeredState=UNTRIGGERED, 该币种不支持 KYT
    ReasonKytRiskPrefix           = "KYT_RISK_"                 // 拼接 risk level: KYT_RISK_HIGH / KYT_RISK_MEDIUM ...
    ReasonKytProviderFailed       = "KYT_PROVIDER_FAILED"       // 服务商不可用 / 8h 未完成
    ReasonKytSkipped              = "KYT_SKIPPED"               // 人工跳过筛查

    // ============ 超时兜底路径 (ScanKYTTimeouts, KYT_PENDING > 20min 时调用) ============
    // 注: 风险等级原因码 (KYT_RISK_HIGH_AFTER_TIMEOUT 等) 不另立常量,
    //     统一用 BuildKytTimeoutRiskReason(riskLevel) 在调用点拼接 — 避免与
    //     ReasonKytRiskPrefix 完全同值的冗余常量混淆 (N-S1 修正)。
    ReasonKytUntriggeredAfterTimeout    = "KYT_UNTRIGGERED_AFTER_TIMEOUT"    // 超时后再查仍 UNTRIGGERED (异常状态)
    ReasonKytProviderFailedAfterTimeout = "KYT_PROVIDER_FAILED_AFTER_TIMEOUT"
    ReasonKytSkippedAfterTimeout        = "KYT_SKIPPED_AFTER_TIMEOUT"
    ReasonKytTimeoutStillPending        = "KYT_TIMEOUT_STILL_PENDING"        // 20min 超时兜底 API 仍返回 IN_PROGRESS, K-19 不延长

    // ============ 系统异常路径 ============
    ReasonKytOrphanAlert          = "KYT_ORPHAN_ALERT"          // AML_KYT_ALERT 找不到 deposit 超过 100 次
    ReasonKytApiFailed            = "KYT_API_FAILED"            // KytReport API 调用失败 > 100 次
)

// BuildKytRiskReason 构造初查路径的风险原因码: KYT_RISK_HIGH / KYT_RISK_MEDIUM 等
func BuildKytRiskReason(riskLevel string) string {
    return ReasonKytRiskPrefix + riskLevel
}

// BuildKytTimeoutRiskReason 构造超时兜底路径的风险原因码: KYT_RISK_HIGH_AFTER_TIMEOUT 等
func BuildKytTimeoutRiskReason(riskLevel string) string {
    return ReasonKytRiskPrefix + riskLevel + "_AFTER_TIMEOUT"
}

// SummarizeRiskLevel 取 amlList 中所有 provider riskLevel 的最高严重度,
// 同时处理 status=PENDING/FAILED/SKIPPED 的优先返回。
//
// 优先级 (从高到低):
//   1. 任一 status=PENDING       → PENDING       (整体未完成, 进 KYT_PENDING)
//   2. 任一 status=FAILED        → FAILED        (MR)
//   3. 任一 status=SKIPPED       → SKIPPED       (MR)
//   4. 全部 COMPLETED, 最高 riskLevel:
//      SEVERE > HIGH > MEDIUM > UNKNOWN > LOW
//
// Phase 1 只接 MistTrack 一家, amlList 长度恒为 1, 但算法已按"任一"写好,
// 未来三家服务商扩展时无需改逻辑。
func SummarizeRiskLevel(amlList []safeheron.AmlReport) string {
    // ... 实现
}

// AlertLevelForKyt 把 KYT 风险等级映射到 alert 告警级别 (K-17)。
// HIGH/SEVERE → ERROR, 其余 → WARN。
func AlertLevelForKyt(riskLevel string) string {
    switch riskLevel {
    case KytHigh, KytSevere:
        return "ERROR"
    default:
        return "WARN"
    }
}

// KytDecisionAction 表示根据 KYT 数据应该采取的下一步行动 (S-2 决策)。
type KytDecisionAction int

const (
    KytActionCredit       KytDecisionAction = iota  // 放行入账 (status=CREDITED)
    KytActionKeepPending                            // 保持 KYT_PENDING 等下一条 AML_KYT_ALERT 或超时
    KytActionManualReview                           // 转人工审核 (status=MANUAL_REVIEW)
)

// KytDecision 是 DecideKYT 的返回值。
type KytDecision struct {
    Action       KytDecisionAction
    RiskLevel    string  // SummarizeRiskLevel 返回值, 写入 deposits.aml_risk_level
    Reason       string  // 仅当 Action=ManualReview 时填; failed_reason 列的值
    AlertLevel   string  // 仅当 Action=ManualReview 时填; "WARN" 或 "ERROR"
}

// DecideKYT 根据 amlScreeningState 和 amlList 决定下一步动作 (S-2 决策)。
//
// 两条调用路径共用同一函数, 输入参数统一为 (state, amlList) 而非 *KytReportResponse:
//   1. ProcessOne 初查: state=report.AmlScreeningTriggeredState, amlList=report.AmlList
//   2. processKYTAlert (AML_KYT_ALERT webhook): state="TRIGGERED" (隐含, 因为收到 alert),
//                                                amlList=alert.AmlList
//
// isAfterTimeout 决定 reason 是否带 _AFTER_TIMEOUT 后缀:
//   - false → 初查路径 + AML_KYT_ALERT 路径 (默认)
//   - true  → 超时兜底扫描路径 (ScanKYTTimeouts)
//
// 处置矩阵见 SPEC §6.5.1 (与 K-1 / K-5 / K-6 / K-7 / K-8 决策对齐):
//   UNTRIGGERED                   → ManualReview (WARN)
//   TRIGGERED, all COMPLETED+LOW  → Credit
//   TRIGGERED, any PENDING        → KeepPending
//   TRIGGERED, any MEDIUM         → ManualReview (WARN)
//   TRIGGERED, any HIGH           → ManualReview (ERROR)
//   TRIGGERED, any SEVERE         → ManualReview (ERROR)
//   TRIGGERED, any UNKNOWN        → ManualReview (WARN)
//   TRIGGERED, any FAILED         → ManualReview (WARN)
//   TRIGGERED, any SKIPPED        → ManualReview (WARN)
//   IN_PROGRESS                   → KeepPending
func DecideKYT(state string, amlList []safeheron.AmlReport, isAfterTimeout bool) KytDecision {
    // 实现见伪代码 §6.5.1 处置矩阵; isAfterTimeout=true 时所有 Reason 走 BuildKytTimeoutRiskReason / ReasonKyt*AfterTimeout
}

// maxLastUpdateTime 取 amlList 中 lastUpdateTime (UNIX 毫秒字符串) 的最大值, 写入
// deposits.aml_evaluated_at。被 T10.4 (T-γ) / T10.5 (ScanKYTTimeouts) / T10.6 (processKYTAlert)
// 三处共用。
//
// 解析失败回退 time.Now() (容忍 Safeheron 偶发字段缺失 / 测试事件 lastUpdateTime 为空)。
// Phase 1 只 1 个 provider, amlList 长度恒为 1; 多 provider 时取最新评估时间最稳健。
func maxLastUpdateTime(amlList []safeheron.AmlReport) time.Time {
    var max int64
    for _, r := range amlList {
        if ts, err := strconv.ParseInt(r.LastUpdateTime, 10, 64); err == nil && ts > max {
            max = ts
        }
    }
    if max == 0 {
        return time.Now()
    }
    return time.UnixMilli(max)
}
```

**DoD**：
- [ ] `internal/wallet/deposit/kyt_test.go` 单测覆盖 8 个 `SummarizeRiskLevel` 分支（5 个 riskLevel + 3 个特殊 status），表驱动
- [ ] `DecideKYT` 单测覆盖 10 个分支 × 2 (isAfterTimeout=false/true) = 20 行表驱动用例
- [ ] `maxLastUpdateTime` 单测：(a) 1 条正常 → 解析正确；(b) 1 条 LastUpdateTime="" → fallback time.Now()；(c) 3 条 → 取最大；(d) 所有解析失败 → fallback
- [ ] `go test ./internal/wallet/deposit/... -run "SummarizeRiskLevel|DecideKYT|AlertLevelForKyt|MaxLastUpdateTime" -race` 全绿
- [ ] 单测覆盖率 100%（纯函数）

### T10.4 — Service.ProcessOne 拆三事务

**目标**：把现有单事务 `ProcessOne` 拆为 (T-α 锁事件 + 临时 KYT_PENDING) → (T-β KYT API，事务外) → (T-γ 入账或 MR + 标 DONE)。

**文件**：修改 `internal/wallet/deposit/service.go`、`internal/wallet/deposit/repository.go`、`internal/wallet/deposit/models.go`

#### 改造点 1：`models.go`

新增常量：
```go
const StatusKYTPending = "KYT_PENDING"   // SPEC §7.1 新增
```
MANUAL_REVIEW 原因码引用 `kyt.go` 定义（T10.3）。

#### 改造点 2：`repository.go` 新增方法

**⚠️ 必须同时改 2 处**（不改会导致编译错误，因为现有文件末尾有 `var _ Repository = (*DBRepository)(nil)` 断言）：

1. **`Repository` interface（`repository.go:22-36`，紧跟 `MarkDepositManualReview` 之后）加 5 个新方法签名**，前后加 `// === AML/KYT (Phase 1 v1.5) ===` 区块注释
2. **`DBRepository` struct 同步加 5 个新方法实现**
3. **`DepositRow` struct（`repository.go:39-57`）追加 4 个 AML 字段，并同步改 SELECT/INSERT/UPDATE SQL 列**

> **方案锁定（R3-C1+C2）**：5 个新方法**全部进 Repository interface**（含 `IncrementEventAttemptsNoTx`，方案 A）。
> - 不再走「Service 加 db 字段 + NewService 签名扩 4 参数」路线（已废弃）；
> - 不再走类型断言 `repo.(*DBRepository)` 路线；
> - 所有调用点统一 `s.repo.X(...)`，与现有所有 Repository 方法调用风格一致；
> - 接口"全部 write 接 Tx"惯例被破坏一处，但用方法名后缀 `NoTx` 显式标注，可读性高于隐式类型断言。

**所有方法签名（返回类型用现有 `*DepositRow`）**：

```go
// DepositRow 在 T10 追加 4 个 AML 字段 (与 SPEC §4.6 一致):
//   AMLScreeningState string    `db:"aml_screening_state"`   // UNTRIGGERED/TRIGGERED, nullable → ""
//   AMLRiskLevel      string    `db:"aml_risk_level"`        // LOW/MEDIUM/HIGH/SEVERE/UNKNOWN/FAILED/SKIPPED/PENDING, nullable → ""
//   AMLEvaluatedAt    time.Time `db:"aml_evaluated_at"`      // amlList[].lastUpdateTime 的最大值, nullable → zero
//   AMLListJSON       []byte    `db:"aml_list"`              // JSONB raw bytes; 业务端按需 unmarshal
//
// 现有 SELECT * / INSERT / UPDATE 语句要同步加上新列, 否则 sqlx StructScan 会 panic。
```


```go
// === AML/KYT (Phase 1 v1.5) — 5 个全部进 Repository interface ===

// UpdateAMLFields 写入 4 个 AML 字段 (来自 KYT API 或 AML_KYT_ALERT)。
// amlListJSON 是 json.Marshal([]AmlReport) 的字节, 直接存进 deposits.aml_list JSONB。
UpdateAMLFields(ctx context.Context, tx Tx, depositID int64, screeningState, riskLevel string,
                evaluatedAt time.Time, amlListJSON []byte) error

// MoveToKYTPending 将 deposit 临时置为 KYT_PENDING (T-α 末尾, KYT_ENABLED=true 时)。
// UPDATE deposits SET status='KYT_PENDING', updated_at=NOW() WHERE id=? AND status='PENDING'
// 注意 AND status='PENDING' 守卫: 防止竞态下重复推进。
MoveToKYTPending(ctx context.Context, tx Tx, depositID int64) error

// LockOneKYTPendingTimeout 超时扫描用 (T10.5), 单行加锁拉取一笔 KYT_PENDING 超时的:
// SELECT * FROM deposits WHERE status='KYT_PENDING' AND updated_at < NOW() - threshold
//   ORDER BY updated_at ASC FOR UPDATE SKIP LOCKED LIMIT 1
// 返回 (nil, ErrNoPending) 表示无超时行 (与现有 LockNextPendingEvent 风格一致)。
// 返回非 nil *DepositRow 时, **该行已持有 FOR UPDATE 行锁直到调用方 tx Commit/Rollback**;
// 调用方可直接 creditDepositFromRow / MarkDepositManualReview 而无需二次 SELECT FOR UPDATE。(R3-S3)
LockOneKYTPendingTimeout(ctx context.Context, tx Tx, threshold time.Duration) (*DepositRow, error)

// FindDepositByTxKey 用于 AML_KYT_ALERT 关联 (T10.6)。
// 内部用 SELECT ... FOR UPDATE 持锁; 返回 (nil, false, nil) 表示未找到 (乱序场景), 不是 error。
// 行锁与外层 tx 同生命周期; 调用方可直接 creditDepositFromRow / MarkDepositManualReview 不需二次锁。
FindDepositByTxKey(ctx context.Context, tx Tx, txKey string) (*DepositRow, bool, error)

// IncrementEventAttemptsNoTx 在 ROLLBACK 场景下推进 process_attempts (C-3 + R3-C2 修正)。
//
// 关键设计: **独立非事务 UPDATE** — DBRepository 内部用 r.db.ExecContext 直接执行,
//   不挂任何外层 tx, 因为调用时机是主事务 ROLLBACK 之后, 必须脱离外层事务才能让 UPDATE 持久化:
//     UPDATE safeheron_webhook_events SET process_attempts = process_attempts + 1 WHERE id = ?
//
// 方法名后缀 `NoTx` 显式标注违反"接口所有 write 都接 Tx"的惯例 — 这是有意设计,
// 仅 2 个场景需要它: (a) 乱序 AML_KYT_ALERT 找不到 deposit (T10.6); (b) KytReport API 失败 (T10.4)。
//
// 现有 MarkEventDone / MarkEventError 已经包含 attempts++, 此方法仅在**不 Mark Done/Error**的
// 场景使用; 调用顺序通常是: ROLLBACK → IncrementEventAttemptsNoTx → return processed (让事件回 PENDING)。
//
// 实现细节: mock 实现按需返回 nil 即可 (测试不验证持久化, 用 sqlmock 拦 SQL)。
IncrementEventAttemptsNoTx(ctx context.Context, eventID int64) error
```

> **NewService 签名保持不变**（现有 `NewService(repo Repository, reg ChainsRegistry, alertFn AlertFunc) *Service`）— Service 通过 `s.repo.IncrementEventAttemptsNoTx(...)` 调用，无需新增 `db *sql.DB` 字段。所有 KYT 依赖通过 `SetKYTDeps` setter 注入（见改造点 4）。

#### 改造点 3：`service.go` 改 `ProcessOne` 主流程

> **风格约定（R3-C3）**：所有 Deposit 状态写都通过 `s.repo.MarkDeposit*` 方法**直调**，不在 Service 层另起小写 helper（与现有 T7 风格一致）。例外只有 `creditDepositFromRow` 和 `markOrphanAlertDone` 两个 Service-level helper，它们组合多个 Repository 方法且有专门 doc 说明。

```go
// ProcessOne 是 KYT 改造后的入账状态机入口 (SPEC §6.4 + §6.5)。
//
// 三事务结构 (v1.5 新增, D-33):
//   T-α: BEGIN → LockNextPendingEvent FOR UPDATE → 解析 + 路由判定 + UPSERT deposits;
//        if needsKYT && kytEnabled: MoveToKYTPending; COMMIT (释放行锁, 不标 DONE)
//        else (KYT_ENABLED=false 或不需要 KYT): 走原有入账逻辑 + MarkEventDone + COMMIT
//   T-β: 调 KytReport API (事务外, 无 DB 连接占用)
//        失败 → handleKYTApiFailure (见下), 不进 T-γ
//   T-γ: BEGIN → UpdateAMLFields + DecideKYT → 三选一执行 → MarkEventDone + COMMIT
//
// 崩溃重启行为 (C-4 修正):
//   - T-α 已 COMMIT, T-β 进行中或之前进程崩溃 → 重启后 deposit 状态=KYT_PENDING,
//     webhook event 仍 PENDING。worker 再次拉取该 event 时, 主流程会重新走到
//     "UPSERT deposits + dep.Status != PENDING 判定", 不会再次进入 KYT 初查 (避免重复调用 API)。
//     此时事件会被静默标 DONE (event 已无实际推进作用), deposit 留给 T10.5 超时扫描兜底。
//   - 这是有意设计: 超时扫描 (KYTScanInterval=1m, KYTTimeout=20m) 是 KYT_PENDING 的终极兜底,
//     不依赖 ProcessOne 二次进入。
func (s *Service) ProcessOne(ctx context.Context) (processed bool, err error) {

    // ========== T-α START ==========
    tx1, _ := s.repo.BeginTx(ctx)
    committed := false
    defer func() { if !committed { _ = tx1.Rollback() } }()

    evt, err := s.repo.LockNextPendingEvent(ctx, tx1)
    if errors.Is(err, ErrNoPending) { return false, nil }

    // 解析 envelope — 现有写法保留 (service.go:128-132)
    var env PayloadEnvelope
    if err := json.Unmarshal(evt.RawPayload, &env); err != nil {
        // payload 损坏: MarkEventError + Commit (现有早退逻辑)
        // ...
        committed = true; return true, fmt.Errorf("unmarshal raw_payload: %w", err)
    }

    // === Dispatch by EventType (R3-I1 合并 — 与 T10.6 改造点 2 写法保持一致) ===
    switch evt.EventType {
    case "AML_KYT_ALERT":
        // AML_KYT_ALERT 的 eventDetail 字段集与 PayloadEventDetail 不同 (无 transactionStatus,
        // 多 amlList), 走二次 unmarshal 到独立 struct, 整个流程在 T-α 内完成, 不进 T-β/T-γ
        var w struct {
            EventDetail AMLKYTAlertDetail `json:"eventDetail"`
        }
        if err := json.Unmarshal(evt.RawPayload, &w); err != nil {
            committed = true; return true, fmt.Errorf("unmarshal AML_KYT_ALERT: %w", err)
        }
        return s.processKYTAlert(ctx, tx1, evt, &w.EventDetail)   // 见 T10.6
    case "TRANSACTION_CREATED", "TRANSACTION_STATUS_CHANGED":
        // 现有 T7 TRANSACTION_* 处理路径, 走 T-β/T-γ — 落入下方主流程
    default:
        // 其他 12 种 eventType (UNVERIFIED_TX / KYC_* / ...) 静默 MarkEventDone, 不处理
        if err := s.repo.MarkEventDone(ctx, tx1, evt.ID); err != nil { return true, err }
        if err := tx1.Commit(); err != nil { return true, err }
        committed = true; return true, nil
    }

    // 以下是 TRANSACTION_CREATED / TRANSACTION_STATUS_CHANGED 的主路径
    d := env.EventDetail   // PayloadEventDetail (现有 service.go:139)

    // 早退: direction != INFLOW
    if d.TransactionDirection != "INFLOW" {
        if err := s.repo.MarkEventDone(ctx, tx1, evt.ID); err != nil { return true, err }
        if err := tx1.Commit(); err != nil { return true, err }
        committed = true; return true, nil
    }

    // 路由判定 (地址无主 / 币种不支持 / 金额异常 → MR + Commit; return)
    // ... 现有 service.go:146-180 逻辑保留, 共用 d ...

    // UPSERT deposits (status_rank 守卫, 现有逻辑保留)
    dep, _ := s.repo.UpsertDeposit(ctx, tx1, /* DepositRow from d */)

    // KYT 初查触发条件
    needsKYT := (d.TransactionStatus == "COMPLETED"
              && d.TransactionSubStatus == "CONFIRMED"
              && dep.Status == "PENDING")   // 注意此处会拒绝崩溃重启后的 status=KYT_PENDING (C-4 设计)

    if !needsKYT {
        // 现有 FAILED/中间态分支, 不变
        // ... 现有逻辑 ...
        MarkEventDone + Commit; committed=true; return true, nil
    }

    // KYT_ENABLED=false: 直接入账 (本地/sandbox, D-35)
    if !s.kytEnabled {
        CreditAccount + WriteJournal + UPDATE deposits SET status='CREDITED'
        MarkEventDone + Commit; committed=true; return true, nil
    }

    // KYT_ENABLED=true: 临时 KYT_PENDING, 提交 T-α
    if err := s.repo.MoveToKYTPending(ctx, tx1, dep.ID); err != nil { return true, err }
    if err := tx1.Commit(); err != nil { return true, err }
    committed = true
    // ========== T-α END (committed, row lock released) ==========


    // ========== T-β START (no DB transaction) ==========
    report, kytErr := s.safeheronClient.KytReport(ctx, d.TxKey)
    if kytErr != nil {
        // I-7 / C-3 修正: 展开 handleKYTApiFailure 内联实现, 让施工者看清完整逻辑
        return s.handleKYTApiFailure(ctx, evt, dep, kytErr)
    }
    // ========== T-β END ==========


    // ========== T-γ START ==========
    tx2, _ := s.repo.BeginTx(ctx)
    committed2 := false
    defer func() { if !committed2 { _ = tx2.Rollback() } }()

    amlListJSON, _ := json.Marshal(report.AmlList)
    evaluatedAt := maxLastUpdateTime(report.AmlList)   // 取 amlList[].lastUpdateTime 最大值
    riskLevel := SummarizeRiskLevel(report.AmlList)
    if err := s.repo.UpdateAMLFields(ctx, tx2, dep.ID,
        report.AmlScreeningTriggeredState, riskLevel, evaluatedAt, amlListJSON); err != nil { return true, err }

    decision := DecideKYT(report.AmlScreeningTriggeredState, report.AmlList, false /* isAfterTimeout */)

    var alerts []alertPayload
    switch decision.Action {
    case KytActionCredit:
        // 复用 creditDepositFromRow (T10.4 新增 helper, 见改造点 3 末尾):
        //   account upsert + journal + UPDATE deposits SET status='CREDITED', 不需要 parsed envelope,
        //   因为 user_id/asset/amount 已挂在 dep 上 (UpsertDeposit 已落库)
        if err := s.creditDepositFromRow(ctx, tx2, dep); err != nil { return true, err }
    case KytActionKeepPending:
        // 保持 KYT_PENDING (理论上初查路径不会进此分支, Phase 1 只 1 个 provider,
        // 一次 KytReport 应返回完整或 IN_PROGRESS——后者已在 SummarizeRiskLevel 返回 PENDING)
        // 仅 UpdateAMLFields, 不动 deposit.status
    case KytActionManualReview:
        if err := s.repo.MarkDepositManualReview(ctx, tx2, dep.ID, decision.Reason); err != nil { return true, err }
        alerts = append(alerts, alertPayload{level: decision.AlertLevel, title: "KYT manual review", fields: ...})
    }

    if err := s.repo.MarkEventDone(ctx, tx2, evt.ID); err != nil { return true, err }
    if err := tx2.Commit(); err != nil { return true, err }
    committed2 = true
    // ========== T-γ END ==========

    s.fireAlerts(alerts)
    return true, nil
}

// handleKYTApiFailure 处理 T-β KYT API 调用失败 (I-7 修正展开伪代码, C-3 计数推进)。
//
// 期望行为:
//   1. 用独立非事务 UPDATE 推进 webhook event 的 process_attempts (C-3 必须, 否则上限永不触发)
//   2. 如果 process_attempts < KYTOrphanAlertMaxRetry (默认 100):
//        deposit 保持 KYT_PENDING; webhook event 保持 PENDING;
//        下次 worker 轮询会重新拉到, 主流程的 needsKYT 判定为 false (因 dep.Status='KYT_PENDING'),
//        事件会被静默 MarkEventDone; deposit 留给超时扫描兜底 (C-4)
//      - 注: 这意味着 KYT API 短期故障只会让事件被消费, 不会无限重试 KytReport;
//        超时扫描会在 20min 后重新调 API 兜底, 这是符合 K-19 的设计
//   3. 如果 process_attempts >= KYTOrphanAlertMaxRetry (说明持续失败超过 ~100 次):
//        开新 tx 标 deposit MANUAL_REVIEW(KYT_API_FAILED) + ERROR 告警 + MarkEventDone
func (s *Service) handleKYTApiFailure(ctx context.Context, evt *Event, dep *DepositRow, kytErr error) (bool, error) {
    log.Printf("KYT API failed: txKey=%s err=%v attempts=%d", dep.SafeheronTxKey, kytErr, evt.ProcessAttempts)

    // 步骤 1: 独立非事务推进 attempts (C-3)
    if err := s.repo.IncrementEventAttemptsNoTx(ctx, evt.ID); err != nil {
        // 推进失败 → 仅日志, 不阻塞 (worker 下次 tick 会重新拉)
        log.Printf("IncrementEventAttempts failed: %v", err)
    }

    // 步骤 2: 判断阈值 (字段名与 T10.4 改造点 4 Service struct 一致, 不走 s.cfg)
    if evt.ProcessAttempts+1 < s.kytOrphanMaxRetry {
        // 还没到上限, 让 worker 自然重试; 当前事件保持 PENDING
        // 返回值语义 (R3-S2):
        //   processed=true → worker drainQueue 继续拉下一条, 不进入 sleep
        //   err=kytErr 非 nil → 触发 worker ERROR 日志 (含 attempts 计数), 但 drainQueue 内只 log 不 return,
        //                     worker 主循环不会因此退出。
        //   对比 T10.6 orphan AML_KYT_ALERT 返回 (true, nil): 静默不打 ERROR 日志, 因为乱序是预期行为。
        return true, kytErr
    }

    // 步骤 3: 超过上限, 强转 MANUAL_REVIEW + 标 DONE
    tx3, _ := s.repo.BeginTx(ctx)
    committed3 := false
    defer func() { if !committed3 { _ = tx3.Rollback() } }()

    if err := s.repo.MarkDepositManualReview(ctx, tx3, dep.ID, ReasonKytApiFailed); err != nil { return true, err }
    if err := s.repo.MarkEventDone(ctx, tx3, evt.ID); err != nil { return true, err }
    if err := tx3.Commit(); err != nil { return true, err }
    committed3 = true

    s.fireAlerts([]alertPayload{{level: "ERROR", title: "KYT API failed after retries",
        fields: map[string]string{"depositId": fmt.Sprint(dep.ID), "txKey": dep.SafeheronTxKey,
                                  "attempts": fmt.Sprint(evt.ProcessAttempts+1)}}})
    return true, nil
}

// creditDepositFromRow 入账 helper, T10.4/T10.5/T10.6 三处入账路径共用 (N-I3 修正)。
//
// 复用现有 4 个 Repository 方法的组合 (与改造前 ProcessOne 入账段逻辑一致):
//   1. repo.FindOrCreateAccountForUpdate(ctx, tx, dep.UserID, dep.Asset)  — 行锁拿/建账户
//   2. repo.CreditAccount(ctx, tx, accountID, dep.Amount)                  — 余额 +amount
//   3. repo.WriteJournal(ctx, tx, JournalEntry{BizType: 10, RefID: dep.ID, Amount: dep.Amount, ...})
//   4. repo.MarkDepositCredited(ctx, tx, dep.ID)                           — UPDATE deposits SET status='CREDITED'
//
// 不需要 parsed webhook envelope, 因为 user_id/asset/amount 在 UpsertDeposit (T-α) 时已落到
// deposits 行上, 此后超时扫描/AML_KYT_ALERT 路径都能直接从 dep 取到。
//
// 注意: 调用方必须已经持有 dep 行锁 (SELECT ... FOR UPDATE), 否则并发场景会导致重复入账。
// T-γ / ScanKYTTimeouts / processKYTAlert 三处都已通过 LockNextPendingEvent / LockOneKYTPendingTimeout
// / FindDepositByTxKey(tx) 持锁, 直接调用即安全。
func (s *Service) creditDepositFromRow(ctx context.Context, tx Tx, dep *DepositRow) error {
    // 实现照搬原 ProcessOne 入账段, 仅签名变化
}
```

#### 改造点 4：Service struct + setter 风格依赖注入（I-2 修正）

**保持现有 `NewService(repo, reg, alertFn)` 签名不变**，新增 setter（与现有 `SetSerialFunc` 风格一致）：

```go
type Service struct {
    repo         Repository
    registry     ChainsRegistry
    alertFn      AlertFunc
    serialFn     SerialNoFunc
    allowedTypes map[string]bool
    // === KYT 字段 (v1.5 T10) ===
    kytEnabled       bool
    safeheronClient  KYTClient         // 仅依赖 KytReport 方法, 见下
    kytOrphanMaxRetry int
    kytTimeout        time.Duration    // KYT_PENDING 超时阈值, 仅 ScanKYTTimeouts 用; ProcessOne 不读
}

// KYTClient 是 Service 需要的最小 Safeheron 接口 (依赖倒置, 便于测试 mock)。
// 实际实现是 internal/safeheron.Client; 测试用 fake 即可。
type KYTClient interface {
    KytReport(ctx context.Context, txKey string) (*safeheron.KytReportResponse, error)
}

// SetKYTDeps 注入 KYT 相关依赖 (容器在 container.go 内调用, 时机: NewService 之后, Worker.Run 之前)。
// 与现有 SetSerialFunc 风格一致, 避免重构为 functional options。
// 参数说明:
//   client            — Safeheron adapter (Phase 1 接 MistTrack); 必填, 否则 KYT 流程会 panic
//   enabled           — true 走完整 KYT 路径; false 仅本地/sandbox 用 (生产启动校验阻止 false)
//   orphanMaxRetry    — AML_KYT_ALERT 找不到 deposit / KytReport API 失败的最大重试次数, 默认 100
//   timeout           — KYT_PENDING 超时阈值, 默认 20min
// 任一参数 zero 值时使用默认值。
func (s *Service) SetKYTDeps(client KYTClient, enabled bool, orphanMaxRetry int, timeout time.Duration) {
    s.safeheronClient = client
    s.kytEnabled = enabled
    if orphanMaxRetry <= 0 { orphanMaxRetry = 100 }
    s.kytOrphanMaxRetry = orphanMaxRetry
    if timeout <= 0 { timeout = 20 * time.Minute }
    s.kytTimeout = timeout
}
```

**注释要求**（明天用户实测，必须清楚）：
- 每个事务边界标注 `// ========== T-α START ==========` / `// ========== T-α END (committed) ==========` 注释
- `ProcessOne` 顶部 doc comment 解释三事务结构 + 崩溃重启行为（已给出，照搬即可）
- 处置矩阵代码段配上 SPEC §6.5.1 行号注释（每个 case 标对应行）
- `handleKYTApiFailure` 内三个步骤都加单行注释
- KYT_ENABLED 分支加注释解释何时进入（仅本地/sandbox 且显式关闭）

**DoD**：
- [ ] `go test ./internal/wallet/deposit/... -race` 全绿（含 15 个 F-KYT 用例 + 原 T7 用例不退化）
- [ ] 单测覆盖 ≥ 80%
- [ ] 代码审阅时事务边界注释清晰可见
- [ ] `handleKYTApiFailure` 单测：(a) attempts < max → 返回 processed=true，attempts 已 +1，deposit 仍 KYT_PENDING；(b) attempts >= max → deposit MANUAL_REVIEW(KYT_API_FAILED) + ERROR alert

### T10.5 — Worker 超时扫描分支

**目标**：在 `Worker.Run` 主循环加 `kytScanTicker` 分支，周期扫描 `status=KYT_PENDING AND updated_at < NOW() - INTERVAL '20 min'` 的 deposit，逐行调 KYT Report API 兜底。

**文件**：修改 `internal/wallet/deposit/worker.go`

```go
type WorkerConfig struct {
    Interval         time.Duration  // 主轮询间隔 (现有, 1s)
    KYTScanInterval  time.Duration  // 超时扫描 ticker 间隔 (新增, 默认 1m)
    PanicBackoff     time.Duration
    // 注: KYTTimeout 阈值不放 Worker 配置, 由 Service 通过 SetKYTDeps 持有;
    //     Service.ScanKYTTimeouts() 内部直接读 s.kytTimeout, 避免双份字段歧义。(R3-I5)
}

func (w *Worker) Run(ctx context.Context) {
    mainTicker := time.NewTicker(w.cfg.Interval)
    kytScanTicker := time.NewTicker(w.cfg.KYTScanInterval)
    defer mainTicker.Stop()
    defer kytScanTicker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-mainTicker.C:
            w.drainQueue(ctx)  // 现有逻辑
        case <-kytScanTicker.C:
            w.scanKYTTimeouts(ctx)  // 新增
        }
    }
}

func (w *Worker) scanKYTTimeouts(ctx context.Context) {
    // 调用 Service.ScanKYTTimeouts(ctx) — timeout 阈值由 Service 自持, Worker 不传
    // 复用现有 panic recover 包装
}
```

`Service.ScanKYTTimeouts(ctx)` 实现（I-4 修正：**每行独立 tx**，FOR UPDATE 持续保护到处理完毕）：

```go
// ScanKYTTimeouts 扫描 KYT_PENDING 超时行, 每次最多处理 N 行;
// 每行用独立 tx 处理: tx 持续到该行处理完毕, FOR UPDATE 锁全程有效, 防多 worker race。
//
// 注意: 不是"先批量 SELECT 拿一批再 COMMIT 锁释放然后逐行处理" (那样会 race);
//      而是"循环 N 次, 每次单独 BEGIN → LockOneKYTPendingTimeout (LIMIT 1 FOR UPDATE) → 处理 → COMMIT"。
//
// 这样即使 worker A 在处理第 3 行 KYT API 调用时 (慢调用 ~5s), worker B 也能拉到第 4 行处理,
// 不会重复处理同一行。
func (s *Service) ScanKYTTimeouts(ctx context.Context) {
    const maxPerTick = 50    // 单次 tick 最多处理 50 个超时, 防 KYT API 突发流量
    for i := 0; i < maxPerTick; i++ {
        processed, err := s.scanOneKYTTimeout(ctx)
        if err != nil { log.Printf("scan KYT timeout: %v", err) }
        if !processed { break }   // 没有更多超时行, 退出
    }
}

func (s *Service) scanOneKYTTimeout(ctx context.Context) (processed bool, err error) {
    tx1, _ := s.repo.BeginTx(ctx)
    committed := false
    defer func() { if !committed { _ = tx1.Rollback() } }()

    // 单行加锁拉取 — LockOneKYTPendingTimeout SQL:
    //   SELECT * FROM deposits WHERE status='KYT_PENDING' AND updated_at < NOW() - $1
    //     ORDER BY updated_at ASC FOR UPDATE SKIP LOCKED LIMIT 1
    dep, err := s.repo.LockOneKYTPendingTimeout(ctx, tx1, s.kytTimeout)
    if errors.Is(err, ErrNoPending) || dep == nil { return false, nil }
    if err != nil { return false, err }

    // KYT API 调用 (事务**外**? 这里是关键决策):
    //   选项 A: API 调用放事务内 — 5s 慢调用持续占行锁, 但保证状态切换原子
    //   选项 B: COMMIT tx1 释放锁, 调 API, 开 tx2 写结果 — 但中间另一 worker 又拉到同行会重复
    //
    // 决策: **选 A**, 让行锁全程保护, 避免重复 API 调用。
    //       50 行 × 5s = 250s 最坏情况, 但实际 Safeheron API ~300ms, 不影响吞吐。
    //       相比 ProcessOne 主路径 (T-α 后释放锁, 因为入账事务窗口窄), 超时扫描的事务窗口可以更长。
    report, kytErr := s.safeheronClient.KytReport(ctx, dep.SafeheronTxKey)
    if kytErr != nil {
        log.Printf("scan KYT API failed for deposit=%d: %v", dep.ID, kytErr)
        // 不动 deposit, 让下一次 tick 重试; 不需要 attempt 计数 (扫描有间隔自然限流)
        return true, nil
    }

    amlListJSON, _ := json.Marshal(report.AmlList)
    evaluatedAt := maxLastUpdateTime(report.AmlList)
    riskLevel := SummarizeRiskLevel(report.AmlList)
    _ = s.repo.UpdateAMLFields(ctx, tx1, dep.ID,
        report.AmlScreeningTriggeredState, riskLevel, evaluatedAt, amlListJSON)

    decision := DecideKYT(report.AmlScreeningTriggeredState, report.AmlList, true /* isAfterTimeout=true, I-5 */)

    var alerts []alertPayload
    switch decision.Action {
    case KytActionCredit:
        // 同 T10.4 改造点 3 末尾定义的 helper, 共用同一签名
        _ = s.creditDepositFromRow(ctx, tx1, dep)
    case KytActionKeepPending:
        // K-19 决策: 仍 IN_PROGRESS → 不再延长, 强转 MR(KYT_TIMEOUT_STILL_PENDING) + ERROR
        _ = s.repo.MarkDepositManualReview(ctx, tx1, dep.ID, ReasonKytTimeoutStillPending)
        alerts = append(alerts, alertPayload{level: "ERROR", title: "KYT timeout still pending", fields: ...})
    case KytActionManualReview:
        // decision.Reason 已带 _AFTER_TIMEOUT 后缀 (DecideKYT(isAfterTimeout=true))
        _ = s.repo.MarkDepositManualReview(ctx, tx1, dep.ID, decision.Reason)
        alerts = append(alerts, alertPayload{level: decision.AlertLevel, title: "KYT timeout review", fields: ...})
    }

    if err := tx1.Commit(); err != nil { return true, err }
    committed = true
    s.fireAlerts(alerts)
    return true, nil
}
```

**关于 creditDepositFromRow helper**：超时扫描场景没有原始 webhook envelope 在手，但入账只需要 `(user_id, currency, amount)`，这些都在 `dep` 上有（`dep.UserID` / `dep.Asset` / `dep.Amount`）。**T10.4 改造点 3 末尾已定义统一 helper `creditDepositFromRow(ctx, tx, dep *DepositRow) error`**，T10.4/T10.5/T10.6 三处入账路径共用此函数，**不存在两个版本**。

**DoD**：
- [ ] 单测 `mock SafeheronClient.KytReport` 返回 `TRIGGERED+LOW` → CREDITED + balance 增加
- [ ] 单测：返回 `IN_PROGRESS` → MR(`KYT_TIMEOUT_STILL_PENDING`) + **ERROR** alert（K-19）
- [ ] 单测：返回 `TRIGGERED+HIGH` → MR(`KYT_RISK_HIGH_AFTER_TIMEOUT`) + **ERROR** alert（I-5 带后缀）
- [ ] 单测：返回 `UNTRIGGERED` → MR(`KYT_UNTRIGGERED_AFTER_TIMEOUT`) + WARN
- [ ] 单测：50 行超时并发处理，行锁正确保护，无重复 API 调用（用 mock 计数）
- [ ] worker panic 时不阻塞主 ticker（recover）

### T10.6 — AML_KYT_ALERT webhook 分支

**目标**：扩展 `processEvent` 处理 `eventType=AML_KYT_ALERT`，含乱序保护（找不到 deposit 时事件回 PENDING）。

**文件**：修改 `internal/wallet/deposit/service.go`、`internal/wallet/deposit/models.go`

#### 改造点 1：`models.go` 新增 AML_KYT_ALERT 专用 detail struct（S-1 修正）

```go
// AMLKYTAlertDetail 是 AML_KYT_ALERT webhook 的 eventDetail (独立 struct, 不与
// TransactionEventDetail 共用; 见 SPEC §8.2)。
//
// 字段来自 Safeheron 文档 "AMLKYTAlertParam":
//   https://docs.safeheron.com/api/zh.html#Webhook
type AMLKYTAlertDetail struct {
    TxKey                  string                `json:"txKey"`
    CustomerRefID          string                `json:"customerRefId"`
    TxHash                 string                `json:"txHash"`
    CoinKey                string                `json:"coinKey"`
    TxAmount               string                `json:"txAmount"`
    SourceAccountKey       string                `json:"sourceAccountKey"`
    SourceAddress          string                `json:"sourceAddress"`
    DestinationAccountKey  string                `json:"destinationAccountKey"`
    DestinationAddress     string                `json:"destinationAddress"`
    TransactionDirection   string                `json:"transactionDirection"`
    AlertTime              string                `json:"alertTime"`
    AmlList                []safeheron.AmlReport `json:"amlList"`
}
```

#### 改造点 2：dispatch 入口已合并进 T10.4 改造点 3

**已合并**：dispatch 逻辑（`switch evt.EventType`）写在 **T10.4 改造点 3** 的 `ProcessOne` 函数顶部（T-α 内 `LockNextPendingEvent` 之后），不在 T10.6 这里另起一份。施工时**只在 T10.4 那个 switch 块里**展开：

- `case "TRANSACTION_CREATED", "TRANSACTION_STATUS_CHANGED"` — 现有 T7 路径，落入主流程
- `case "AML_KYT_ALERT"` — 用专门 wrap struct 二次 unmarshal 到 `AMLKYTAlertDetail`，调 `s.processKYTAlert(ctx, tx1, evt, &w.EventDetail)`
- `default` — 静默 `MarkEventDone` + Commit

> **关于 AML_KYT_ALERT 的 unmarshal 写法（R3-I2 修正）**：现有 `PayloadEnvelope.EventDetail` 类型是 `PayloadEventDetail`，不含 `AmlList` 字段。AML_KYT_ALERT 不能复用 `env.EventDetail`，必须从 `evt.RawPayload`（已是 `[]byte`，无需类型转换）二次 unmarshal 到一个 `wrap struct`：
>
> ```go
> var w struct { EventDetail AMLKYTAlertDetail `json:"eventDetail"` }
> if err := json.Unmarshal(evt.RawPayload, &w); err != nil { return true, err }
> return s.processKYTAlert(ctx, tx, evt, &w.EventDetail)
> ```
>
> 该写法已经在 T10.4 改造点 3 的 dispatch switch 块里就位，T10.6 这里不重复。

> **关于扩 PayloadEventDetail 而非二次 unmarshal 的替代方案**：可以给 `PayloadEventDetail` 加 `AmlList []safeheron.AmlReport \`json:"amlList,omitempty"\`` 字段，省一次 unmarshal；但这样会把 KYT 字段污染到所有 TRANSACTION_* 事件的解析路径。**当前方案选二次 unmarshal**，保持 PayloadEventDetail 的关注点单一。

#### 改造点 3：`processKYTAlert` 完整实现

```go
// processKYTAlert 处理 AML_KYT_ALERT webhook 事件 (Phase 1 主路径)。
// 进入时 tx 已持有 webhook_events 行锁 (FOR UPDATE);
// 退出时根据情况 Commit 标 DONE / Rollback 让事件回 PENDING。
func (s *Service) processKYTAlert(ctx context.Context, tx Tx, evt *Event, alert *AMLKYTAlertDetail) (processed bool, err error) {

    // 步骤 1: 按 txKey 查 deposit
    dep, found, err := s.repo.FindDepositByTxKey(ctx, tx, alert.TxKey)
    if err != nil { return true, err }

    if !found {
        // === 乱序场景: AML_KYT_ALERT 比 TRANSACTION_STATUS_CHANGED 先到 (K-13) ===
        //
        // 关键决策 (C-3): worker_events 的 process_attempts 推进必须用**独立非事务 UPDATE**,
        // 因为下面的 Rollback 会回滚事务内的 UPDATE, attempts 永远不会增长, 永远触发不到上限。
        //
        // 注意: IncrementEventAttemptsNoTx 用 *sql.DB (无 tx), 不受外层 tx 影响,
        //       Rollback 之后该 UPDATE 仍然生效。
        if err := s.repo.IncrementEventAttemptsNoTx(ctx, evt.ID); err != nil {
            log.Printf("IncrementEventAttempts failed for orphan AML_KYT_ALERT eventId=%d: %v", evt.ID, err)
            // 推进失败也不阻塞 (下一轮 tick 会重新拉)
        }

        // 判断阈值: 注意 evt.ProcessAttempts 是 LockNextPendingEvent 拿到时的快照 (未 +1),
        //          所以这里用 evt.ProcessAttempts+1 与上限比较
        if evt.ProcessAttempts+1 >= s.kytOrphanMaxRetry {
            // === 超过 100 次仍找不到 deposit → 强转 MR(KYT_ORPHAN_ALERT) ===
            // 这是个异常状态: 100 次重试每次间隔 1s, 累计 ~100s 都找不到 deposit,
            // 说明该 AML_KYT_ALERT 对应的交易确实没有走入金管道 (可能是 OUTFLOW 或其他异常)。
            return s.markOrphanAlertDone(ctx, tx, evt, alert)   // 标 DONE + ERROR alert
        }

        // === I-8 注释: 未到上限, 让事件回 PENDING 等下次轮询 ===
        //
        // 返回值语义说明:
        //   processed=true 是为了让 worker 主循环继续 drain 下一条事件;
        //   processed=false 会让 worker 进入 sleep, 但此处希望立即处理下一条 (这条 AML_KYT_ALERT
        //   下次 tick 还会被重新拉到, 不会丢)。
        //
        //   err=nil 是因为这不是错误状态, 是预期的乱序保护; 如果返 err 会触发 worker 的错误日志噪音。
        //
        //   Rollback 不显式调用: 函数末尾 deferred handler 会在 committed=false 时自动 Rollback,
        //   外层 ProcessOne 的 defer 即可。
        return true, nil
    }

    // === 步骤 2: 找到 deposit, 写入 AML 数据 ===
    amlListJSON, _ := json.Marshal(alert.AmlList)
    evaluatedAt := maxLastUpdateTime(alert.AmlList)
    riskLevel := SummarizeRiskLevel(alert.AmlList)
    if err := s.repo.UpdateAMLFields(ctx, tx, dep.ID,
        "TRIGGERED",   // AML_KYT_ALERT 隐含 TRIGGERED (服务商已开始评估)
        riskLevel, evaluatedAt, amlListJSON); err != nil { return true, err }

    // === 步骤 3: 终态保护 (K-18) ===
    if dep.Status == "CREDITED" || dep.Status == "MANUAL_REVIEW" || dep.Status == "FAILED" {
        // 终态不动, 仅记录 AML 数据供审计; 标 DONE 让事件不再重试
        // K-18: Phase 1 不做"运维放行"接口, 这里不自动改回 CREDITED 即便 risk=LOW
        if err := s.repo.MarkEventDone(ctx, tx, evt.ID); err != nil { return true, err }
        if err := tx.Commit(); err != nil { return true, err }
        return true, nil
    }

    // === 步骤 4: dep.Status ∈ ('PENDING', 'KYT_PENDING') → 按 DecideKYT 推进 ===
    decision := DecideKYT("TRIGGERED", alert.AmlList, false /* isAfterTimeout */)

    var alerts []alertPayload
    switch decision.Action {
    case KytActionCredit:
        // 复用 creditDepositFromRow (T10.5 同款), 不需要 parsed envelope
        if err := s.creditDepositFromRow(ctx, tx, dep); err != nil { return true, err }
    case KytActionKeepPending:
        // 理论不进入: AML_KYT_ALERT 推送时 amlList 内 provider 应该都 COMPLETED
        // (Phase 1 只 1 个 provider); 但保留分支应对 Safeheron 改协议
        // 仅 UpdateAMLFields 已完成 (步骤 2), 不动 status
        // 注意此时 dep.Status 已经是 KYT_PENDING (T-α 设的), 留给超时扫描兜底
    case KytActionManualReview:
        if err := s.repo.MarkDepositManualReview(ctx, tx, dep.ID, decision.Reason); err != nil { return true, err }
        alerts = append(alerts, alertPayload{level: decision.AlertLevel, title: "KYT alert manual review",
            fields: map[string]string{"depositId": fmt.Sprint(dep.ID), "txKey": alert.TxKey,
                                      "riskLevel": riskLevel, "reason": decision.Reason}})
    }

    if err := s.repo.MarkEventDone(ctx, tx, evt.ID); err != nil { return true, err }
    if err := tx.Commit(); err != nil { return true, err }

    s.fireAlerts(alerts)
    return true, nil
}

// markOrphanAlertDone 在 AML_KYT_ALERT 找不到 deposit 重试超过上限时调用。
func (s *Service) markOrphanAlertDone(ctx context.Context, tx Tx, evt *Event, alert *AMLKYTAlertDetail) (bool, error) {
    // 注意: 这里没有对应 deposit, 只能在 webhook_events 上标 DONE; 不动 deposits 表。
    // 用 ERROR alert 通知运维: 收到了一个无主的 AML_KYT_ALERT (可能是 OUTFLOW 或测试事件)。
    if err := s.repo.MarkEventDone(ctx, tx, evt.ID); err != nil { return true, err }
    if err := tx.Commit(); err != nil { return true, err }
    s.fireAlerts([]alertPayload{{level: "ERROR", title: "Orphan AML_KYT_ALERT after retries",
        fields: map[string]string{"eventId": fmt.Sprint(evt.ID), "txKey": alert.TxKey,
                                  "txHash": alert.TxHash, "direction": alert.TransactionDirection}}})
    return true, nil
}
```

**DoD**：
- [ ] F-KYT-7、F-KYT-8、F-KYT-9 单测全过
- [ ] **乱序场景集成测试**：先 INSERT 一条 `eventType=AML_KYT_ALERT` 的 webhook_events 行（无对应 deposit），调一次 `ProcessOne` → `processed=true, err=nil`，event 仍 PENDING 且 `process_attempts=1`；再 INSERT `TRANSACTION_STATUS_CHANGED` 行并 worker 处理 → deposit 创建；再次 `ProcessOne` 处理 AML_KYT_ALERT → deposit 正确推进
- [ ] **orphan 重试上限测试**：mock `process_attempts=99`，处理一次后变 100，仍 PENDING；下次处理（attempts+1=101 >= 100）→ MR/标 DONE + ERROR alert
- [ ] **终态保护测试**（K-18）：deposit.status=CREDITED 时收到 AML_KYT_ALERT(LOW/HIGH)，status **不变**，AML 字段已写入

### T10.7 — Container 启动校验 + 配置注入

**目标**：`container.go` 加 KYT 配置读取 + prod 启动校验（KYT_ENABLED=false 在 prod 直接 panic）+ 通过 setter 注入 KYT 依赖到 Service（I-2 决策：setter 风格，不上 functional options）。

**文件**：修改 `internal/container/container.go`

```go
// 在 WithSafeheronPool 内, 创建 Service 之后, 调 SetKYTDeps 注入 KYT 相关依赖。
// SetKYTDeps 风格与现有 SetSerialFunc (service.go) 一致, 避免 NewService 签名膨胀。

// === KYT_ENABLED 配置读取 + 生产环境启动校验 (K-16) ===
// 默认值: 未设置时按 true (生产安全默认, 防止配置遗漏导致绕过 KYT)
kytEnabled := true
if viper.IsSet("KYT_ENABLED") {
    kytEnabled = viper.GetBool("KYT_ENABLED")
}
if viper.GetString("APP_ENV") == "production" && !kytEnabled {
    panic("KYT_ENABLED=false is not allowed in production (K-16): " +
          "set KYT_ENABLED=true or unset for production deployment")
}

// === KYT 配置读取 (其他三个有默认值, 不强制 env) ===
kytOrphanMaxRetry := viper.GetInt("KYT_ORPHAN_ALERT_MAX_RETRY")
if kytOrphanMaxRetry <= 0 { kytOrphanMaxRetry = 100 }

kytTimeout := viper.GetDuration("KYT_TIMEOUT")
if kytTimeout <= 0 { kytTimeout = 20 * time.Minute }

kytScanInterval := viper.GetDuration("KYT_SCAN_INTERVAL")
if kytScanInterval <= 0 { kytScanInterval = time.Minute }

// === Service 注入 (现有 NewService 签名不变, 通过 setter 加 KYT) ===
c.DepositPipeline = deposit.NewService(depRepo, registry, c.AlertService.Send)
c.DepositPipeline.SetKYTDeps(client, kytEnabled, kytOrphanMaxRetry, kytTimeout)
// 注: client 类型是 safeheron.SafeheronClient (现有接口), 它实现了 deposit.KYTClient
//     窄接口 (T10.4 改造点 4), 由 Go 接口隐式满足关系自动适配。

// === Worker 配置: 现有字段 + KYT 扫描字段 ===
workerInterval := viper.GetDuration("DEPOSIT_WORKER_INTERVAL")
if workerInterval <= 0 { workerInterval = time.Second }

c.DepositWorker = deposit.NewWorker(c.DepositPipeline, deposit.WorkerConfig{
    Interval:        workerInterval,
    KYTScanInterval: kytScanInterval,
    PanicBackoff:    5 * time.Second, // 显式给值; 不从 env 读 (NewWorker 内部 ≤0 兜底也是 5s, 写出来避免施工者困惑)
    // KYTTimeout 不放这里 — 已通过 SetKYTDeps 注入到 Service; Worker 调 Service.ScanKYTTimeouts() 时不再传 (R3-I5)
})

log.Printf("[KYT] enabled=%v scan_interval=%s timeout=%s orphan_max_retry=%d",
    kytEnabled, kytScanInterval, kytTimeout, kytOrphanMaxRetry)
```

**`.env.example` 追加**（按 plan.md §3.10 末尾的 KYT 块）：
```bash
# ============ KYT 合规筛查 (Phase 1, v1.5) ============
KYT_ENABLED=true                       # 仅 APP_ENV != production 允许设为 false
KYT_TIMEOUT=20m                        # KYT_PENDING 超时阈值 (默认 20m)
KYT_SCAN_INTERVAL=1m                   # 超时扫描 ticker 间隔
KYT_ORPHAN_ALERT_MAX_RETRY=100         # AML_KYT_ALERT 找不到 deposit 的最大重试次数
```

**DoD**：
- [ ] F-KYT-11：`APP_ENV=production KYT_ENABLED=false ./monera-server` 启动 panic + 日志含 "KYT_ENABLED=false is not allowed in production"
- [ ] F-KYT-12：`APP_ENV=local KYT_ENABLED=false ./monera-server` 启动正常，启动日志可见 `[KYT] enabled=false`，发起一笔充值后无 `safeheron KytReport` 调用日志
- [ ] `.env.example` 加 4 个 KYT 环境变量
- [ ] 启动日志含 `[KYT] enabled=true scan_interval=1m0s timeout=20m0s orphan_max_retry=100`
- [ ] `c.DepositPipeline` 通过 `SetKYTDeps` 注入后, `safeheronClient` 字段非 nil（用 reflect 或额外 getter 验证）

### T10.8 — 前端 i18n + Status Badge

**目标**：前端 `KYT_PENDING` 状态在 Recent deposits 显示「Under compliance review」。

**文件**：
- 修改 `src/i18n/locales/en.json`：加 `"deposit.status.KYT_PENDING": "Under compliance review"`
- 修改 `src/i18n/locales/zh.json`：加 `"deposit.status.KYT_PENDING": "合规审核中"`
- 修改 `src/pages/dashboard/Deposit.tsx`：Recent deposits 的 Badge 着色映射加 `KYT_PENDING: 'bg-blue-100 text-blue-700'`（与 PENDING 同色，不暴露 KYT 概念）

**DoD**：
- [ ] 浏览器手测：制造一笔 KYT_PENDING 状态的 deposit（local 直接 `UPDATE deposits SET status='KYT_PENDING' WHERE id=?`），前端「Recent deposits」对应行显示「Under compliance review」/「合规审核中」
- [ ] 中英文切换都正常
- [ ] 无 i18n missing-key warning

### T10.9 — 原 T7 入账测试回归

**目标**：确保 T10 改造不破坏 T7 的入账主路径测试。

**文件**：修改 `internal/wallet/deposit/service_test.go`（按需）

**思路**（推荐二选一）：
- **方案 A**（推荐）：在测试 setup 里 `service.kytEnabled = false`，所有原 T7 测试走 KYT 跳过分支，覆盖 KYT_ENABLED=false 路径
- **方案 B**：mock `SafeheronClient.KytReport` 始终返回 `TRIGGERED+LOW`，让原 T7 测试经过 KYT 后仍 CREDITED

**DoD**：
- [ ] `go test ./internal/wallet/deposit/... -race -v` 全绿，**无任何 T7 用例退化**
- [ ] `go test ./internal/handlers/... -race -v` 全绿
- [ ] 整体覆盖率不下降（`go test ./internal/wallet/... -cover` 与改造前对比）

---

## T11. 充值流水线安全加固 [⏸️ 待施工]

**依赖**：T7, T10
**估时**：0.5d
**输出**：9 项安全修复 + 015 追加 `AddAccountBalanceConstraints` step（不新增 migration 文件）+ 测试覆盖
**背景**：2026-05-13 安全审计发现充值入账流水线存在状态覆写、缺乏 IP 白名单、余额可负等风险点；同日用户反馈 shell env 串扰 `.env` 配置（反复踩坑）。修复范围锁定为 D-41 ~ D-51（D-49/D-50 明确不修复，详见 plan.md）。

---

### T11.1 — 状态覆写保护（D-41，HIGH）

**问题**：`MarkDepositFailed` 和 `MarkDepositManualReview` 无状态前置条件，已 CREDITED 的 deposit 可被迟到的 webhook 或 KYT 超时覆写为 FAILED/MANUAL_REVIEW。

**修改文件**：`internal/wallet/deposit/repository.go`

**修改内容**：

1. `MarkDepositFailed`（当前 ~L293）：
   ```go
   // 当前：WHERE id = $3（无状态保护）
   // 改为：WHERE id = $3 AND status NOT IN ('CREDITED')
   ```
   加 `RowsAffected == 0` 检查，返回 sentinel error `ErrDepositAlreadyCredited`。

2. `MarkDepositManualReview`（当前 ~L306）：
   ```go
   // 当前：WHERE id = $3（无状态保护）
   // 改为：WHERE id = $3 AND status NOT IN ('CREDITED', 'FAILED')
   ```
   同样加 `RowsAffected` 检查。FAILED 也是终态，不应被覆写为 MANUAL_REVIEW。

3. 调用方处理：`service.go` 中调用这两个方法的地方，捕获 `ErrDepositAlreadyCredited` 时：
   - 记录 WARN 日志（"attempted to overwrite terminal deposit status"）
   - **不**回滚整个事务，标记 webhook event 为 DONE（防止无限重试）

**锁与性能影响**：无新增锁。WHERE 条件在已有的 `UPDATE ... WHERE id = $N` 基础上多加一个 `AND status NOT IN (...)`，命中主键索引后内联判断，性能影响为零。

**DoD**：
- [ ] `MarkDepositFailed` 加 `WHERE status NOT IN ('CREDITED')` + `RowsAffected` 检查
- [ ] `MarkDepositManualReview` 加 `WHERE status NOT IN ('CREDITED', 'FAILED')` + `RowsAffected` 检查
- [ ] 新增测试：构造 CREDITED deposit → 调用 MarkDepositFailed → 返回 error，deposit 状态不变
- [ ] 新增测试：构造 CREDITED deposit → 调用 MarkDepositManualReview → 返回 error，deposit 状态不变
- [ ] service 层调用方正确处理 sentinel error（日志 + 标 DONE）

**验证命令**：
```bash
go test ./internal/wallet/deposit/... -run TestMarkDeposit -race -v
```

---

### T11.2 — Webhook IP 白名单（D-42，HIGH）

**问题**：webhook 端点暴露公网，无 IP 来源校验，任何人可以发请求触发 RSA 验签（CPU 密集型）。

**修改文件**：`internal/handlers/safeheron_webhook_handler.go`

**修改内容**：

1. `SafeheronWebhookHandler` 构造时注入 `allowedIPs []string`（来自 env `SAFEHERON_WEBHOOK_ALLOWED_IPS`，逗号分隔）
2. `Receive` 方法入口，在读 body 和 RSA 验签**之前**，校验 `c.ClientIP()`：
   ```go
   if len(h.allowedIPs) > 0 {
       clientIP := c.ClientIP()
       if !slices.Contains(h.allowedIPs, clientIP) {
           c.AbortWithStatus(http.StatusForbidden)
           return
       }
   }
   ```
3. `allowedIPs` 为空时跳过校验（本地开发兼容）

**环境变量**：
```bash
SAFEHERON_WEBHOOK_ALLOWED_IPS=52.77.227.200,13.213.192.137  # Safeheron 官方 IP
```

**锁与性能影响**：无锁。`slices.Contains` 在 2-5 个 IP 上是 O(n) 线性扫描，纳秒级。放在 RSA 验签之前，可以在 IP 不匹配时直接返回 403，避免 CPU 消耗。

**DoD**：
- [ ] handler 构造注入 `allowedIPs`
- [ ] Receive 入口校验 ClientIP
- [ ] 空 allowedIPs 时不校验（本地兼容）
- [ ] `.env.example` 添加 `SAFEHERON_WEBHOOK_ALLOWED_IPS` 条目
- [ ] 新增测试：allowedIPs 含 "1.2.3.4" 时，ClientIP="5.5.5.5" 返回 403
- [ ] 新增测试：allowedIPs 为空时，任何 IP 正常通过（进入后续验签）

**验证命令**：
```bash
go test ./internal/handlers/... -run TestWebhookIPWhitelist -race -v
```

---

### T11.3 — FindOrCreateAccountForUpdate 注释（D-43）

**修改文件**：`internal/wallet/deposit/repository.go`

在 `FindOrCreateAccountForUpdate` 方法上方加注释：
```go
// FindOrCreateAccountForUpdate inserts or locates the account row and holds a
// row-level exclusive lock until tx commits. The no-op DO UPDATE clause is
// intentional: PostgreSQL's INSERT ON CONFLICT DO UPDATE acquires an exclusive
// row lock identical to SELECT FOR UPDATE, serialising concurrent credits.
```

**DoD**：
- [ ] 注释已添加

---

### T11.4 — 余额非负 CHECK 约束（D-44）

**约定**：phase1 整个阶段**只有 1 个 migration 文件 `015_safeheron_phase1.go`**（memory `feedback_migration_consolidation`）。T10 KYT 字段已经按此约定并入 015 内部 step。T11 余额约束沿用同一模式：**新增 step struct，追加到 015 内部 step list 末尾，不新增独立 migration 文件**。

**修改文件**：`internal/migration/migrations/015_safeheron_phase1.go`

**修改内容**：

1. 在 015 文件末尾新增 step struct `AddAccountBalanceConstraints`：
   ```go
   type AddAccountBalanceConstraints struct{}

   func (m *AddAccountBalanceConstraints) Up(db *sql.DB) error {
       query := `
       DO $$ BEGIN
         IF NOT EXISTS (
           SELECT 1 FROM pg_constraint WHERE conname = 'ck_balance_non_negative'
         ) THEN
           ALTER TABLE account ADD CONSTRAINT ck_balance_non_negative CHECK (balance >= 0);
         END IF;
         IF NOT EXISTS (
           SELECT 1 FROM pg_constraint WHERE conname = 'ck_frozen_non_negative'
         ) THEN
           ALTER TABLE account ADD CONSTRAINT ck_frozen_non_negative CHECK (frozen_balance >= 0);
         END IF;
       END $$;`
       if _, err := db.Exec(query); err != nil {
           return fmt.Errorf("failed to add account balance constraints: %w", err)
       }
       return nil
   }

   func (m *AddAccountBalanceConstraints) Down(db *sql.DB) error {
       _, err := db.Exec(`
           ALTER TABLE account DROP CONSTRAINT IF EXISTS ck_balance_non_negative;
           ALTER TABLE account DROP CONSTRAINT IF EXISTS ck_frozen_non_negative;`)
       return err
   }
   ```

2. 在 `SafeheronPhase1.Up()` 的 step list **末尾**追加：
   ```go
   {"AddAccountBalanceConstraints", (&AddAccountBalanceConstraints{}).Up},
   ```

3. 在 `SafeheronPhase1.Down()` 的 step list **开头**追加（反向顺序）：
   ```go
   {"AddAccountBalanceConstraints", (&AddAccountBalanceConstraints{}).Down},
   ```

**关键事项**：
- **不新增 migration 文件**（如 `023_security_hardening.go`）
- **不修改 `cmd/migrate/main.go`**（015 已注册，新 step 自动随 015 执行）
- **本地 015 已 applied**：migrator 不会重跑 015，所以本地需手动 ALTER 补约束（见下方命令）
- **生产首次部署**：015 一次到位，包含 step list 所有 step（含新 AddAccountBalanceConstraints）

**锁与性能影响**：
- `ADD CONSTRAINT CHECK` 需要扫描 account 全表验证现有数据
- 当前 account 表行数极少（< 100 行），执行时间 < 1ms
- 锁类型：`ACCESS EXCLUSIVE` 短暂持有；线上加约束前应通过下方 SELECT 确认无违反数据
- 加完后对 INSERT/UPDATE 的性能影响为零（CHECK 内联判断，纳秒级）

**部署前置校验**（生产部署必须确认）：
```sql
SELECT count(*) FROM account WHERE balance < 0 OR frozen_balance < 0;
-- 必须返回 0；非 0 时先人工排查 + 修复数据，再加约束
```

**DoD**：
- [ ] 在 `015_safeheron_phase1.go` 中新增 `AddAccountBalanceConstraints` step struct（Up + Down）
- [ ] `SafeheronPhase1.Up()` step list 末尾追加新 step
- [ ] `SafeheronPhase1.Down()` step list 开头追加反向 step
- [ ] **不**新建 023 文件，**不**修改 `cmd/migrate/main.go`
- [ ] `internal/migration/migrations/safeheron_migrations_test.go` 追加测试：单跑 `AddAccountBalanceConstraints.Up` → `\d account` 含两条 CHECK 约束
- [ ] 本地数据库手动 ALTER 补约束（命令见下）
- [ ] 新增测试：`UPDATE account SET balance = -1` → PostgreSQL 报 CHECK violation

**本地补约束命令**（开发者本地 015 已 applied 必须手动跑）：
```bash
psql postgresql://linden@localhost/monera_local -c "
  ALTER TABLE account ADD CONSTRAINT ck_balance_non_negative CHECK (balance >= 0);
  ALTER TABLE account ADD CONSTRAINT ck_frozen_non_negative CHECK (frozen_balance >= 0);"
```

**验证命令**：
```bash
# 1. 单元测试
go test ./internal/migration/migrations/... -run TestAddAccountBalanceConstraints -race -v

# 2. 实际约束生效验证（任意已存在的 account 行）
psql postgresql://linden@localhost/monera_local -c \
  "UPDATE account SET balance = -1 WHERE id = (SELECT id FROM account LIMIT 1);"
# 预期：ERROR: new row for relation "account" violates check constraint "ck_balance_non_negative"
```

---

### T11.5 — KYT 环境二次校验（D-45）

**问题**：`container.go:142` 已有启动校验，但 `service.go` 的 `SetKYTDeps` 是运行时调用，如果 container 层因代码重构绕过了校验，service 层就是最后防线。

**修改文件**：`internal/wallet/deposit/service.go`

**修改内容**：在 `SetKYTDeps` 方法中加入：
```go
func (s *Service) SetKYTDeps(enabled bool, ...) {
    if !enabled && os.Getenv("APP_ENV") == "production" {
        panic("CRITICAL: KYT cannot be disabled in production (D-45 double-check)")
    }
    // ... 现有逻辑
}
```

**DoD**：
- [ ] `SetKYTDeps` 加 production + !enabled → panic
- [ ] 新增测试：`t.Setenv("APP_ENV", "production")` + `SetKYTDeps(false, ...)` → panic

---

### T11.6 — UpsertDeposit fallback 加 FOR UPDATE（D-46）

**问题**：当 status_rank 拦截 upsert 后，`fetchDepositByTxKey` 走普通 SELECT 读取 deposit，后续决策（如 isFailedStatus 分支）基于可能过时的数据。

**修改文件**：`internal/wallet/deposit/repository.go`

**修改内容**：`fetchDepositByTxKey` 的 SQL 加 `FOR UPDATE`：
```sql
SELECT ... FROM deposits WHERE safeheron_tx_key = $1 FOR UPDATE
```

**锁与性能影响分析**：
- **何时触发**：仅当 upsert 被 status_rank 拦截时（即旧状态 webhook 到达），属于低频路径
- **锁范围**：单行 deposits，在 T-alpha 事务内（事务很短：parse + upsert/fallback + MoveToKYTPending + COMMIT）
- **死锁风险**：T-alpha 只锁 deposits 行（不锁 account/journal），不与 T-gamma 的锁顺序冲突。T-gamma 的 `FindDepositByTxKey` 也对 deposits 加 FOR UPDATE，但两者通过 `safeheron_tx_key` 锁同一行——后到的事务自然等待前一个 COMMIT，无交叉锁
- **结论**：安全，无死锁风险

**DoD**：
- [ ] `fetchDepositByTxKey` 加 `FOR UPDATE`
- [ ] 确认现有测试仍全部通过（此处不需要新测试——锁语义改变不影响功能行为，只影响并发安全性）

**验证命令**：
```bash
go test ./internal/wallet/deposit/... -race -count=1
```

---

### T11.7 — MoveToKYTPending 检查 RowsAffected（D-47）

**问题**：`MoveToKYTPending` 有 `WHERE status = 'PENDING'` 保护，但不检查 RowsAffected。并发场景下 deposit 已被另一个 worker 推进，当前 worker 仍然继续调用 KYT API（浪费配额）。

**修改文件**：`internal/wallet/deposit/repository.go`

**修改内容**：
```go
func (r *DBRepository) MoveToKYTPending(...) error {
    res, err := ...ExecContext(ctx, ...)
    if err != nil { return ... }
    n, _ := res.RowsAffected()
    if n == 0 {
        return ErrDepositNotPending  // 新 sentinel error
    }
    return nil
}
```

调用方（`service.go` T-alpha 末尾）捕获 `ErrDepositNotPending` → 跳过 T-beta KYT API 调用，直接标 event DONE。

**DoD**：
- [ ] `MoveToKYTPending` 加 `RowsAffected` 检查
- [ ] 定义 `ErrDepositNotPending` sentinel error
- [ ] service 层 T-alpha 捕获后跳过 KYT 调用
- [ ] 新增测试：deposit 状态为 KYT_PENDING → 调用 MoveToKYTPending → 返回 ErrDepositNotPending

**验证命令**：
```bash
go test ./internal/wallet/deposit/... -run TestMoveToKYTPending -race -v
```

---

### T11.8 — LookupAddressOwner 加 network_family（D-48）

**问题**：查询 `WHERE address = $1` 不带 network_family，理论上跨 network_family 地址碰撞可导致用户错配。

**修改文件**：`internal/wallet/deposit/repository.go`

**修改内容**：
1. `LookupAddressOwner` 签名加 `networkFamily string` 参数
2. SQL 改为 `WHERE address = $1 AND network_family = $2`
3. 调用方从 deposit 的 chain → registry 查 `chains.network_family` 传入

**锁与性能影响**：无锁。`address_pool` 的唯一索引是 `(network_family, address)`，加上 network_family 后查询从扫描所有 network_family 行变为精确命中索引，**性能反而更好**。

**DoD**：
- [ ] `LookupAddressOwner` 加 `networkFamily` 参数
- [ ] SQL 加 `AND network_family = $2`
- [ ] 所有调用方传入正确的 networkFamily
- [ ] 现有测试适配新签名

**验证命令**：
```bash
go test ./internal/wallet/deposit/... -race -count=1
```

---

### T11.9 — .env 优先于 shell env（D-51）

**问题**：shell profile 里如果已经导出 `DATABASE_URL`（指向别的项目，如 `trader_flow`），viper 的 `AutomaticEnv()` 优先级高于 `ReadInConfig()`，会**静默覆盖** `.env` 中的同名变量，导致后端连错数据库。每次启动 Go server 都必须显式 `DATABASE_URL=... ./monera-digital`（memory `local_env_setup_pitfalls`）——这是反复踩过的坑。

**根因**：viper 的优先级是固定的 `Set > AutomaticEnv > ReadInConfig > SetDefault`，无法颠倒。

**解决方案**：在 viper 读取之前，用 `godotenv.Overload(".env")` 把 `.env` 内容**覆盖**到 `os.Environ()`，让进程内所有读 env 的代码（含 viper 和直接 `os.Getenv` 的 cmd 工具）都拿到 `.env` 值。`godotenv v1.5.1` 已在 `go.mod`，无新增依赖。

**修改文件**：

1. **`internal/config/config.go`**（主后端，必改）：
   ```go
   import (
       "os"
       "github.com/joho/godotenv"
       "github.com/spf13/viper"
   )

   func Load() *Config {
       // D-51: 本地/测试环境让 .env 覆盖 shell env，避免其他项目的同名变量串扰
       // 生产环境（Vercel / Cloud Run）通过 APP_ENV=production 守护，保持原 env 注入逻辑
       if os.Getenv("APP_ENV") != "production" {
           _ = godotenv.Overload(".env")
       }

       viper.SetConfigFile(".env")
       viper.ReadInConfig()
       // ... 其余完全不变
       viper.AutomaticEnv()
       // ...
   }
   ```

2. **所有 cmd 工具入口**（8 个文件，除 `db_check` / `wealth_test` 硬编码连接串外）：
   - `cmd/migrate/main.go`
   - `cmd/migrate-drop/main.go`
   - `cmd/pool_init/main.go`
   - `cmd/scheduler/main.go`
   - `cmd/scheduler/run_once.go`
   - `cmd/check-2fa/main.go`
   - `cmd/check-wallet/main.go`
   - `cmd/simulation_test/main.go`

   每个 cmd 的 `main()` 函数**第一行**加：
   ```go
   import "github.com/joho/godotenv"

   func main() {
       // D-51: .env 优先于 shell env（仅本地/测试）
       if os.Getenv("APP_ENV") != "production" {
           _ = godotenv.Overload(".env")
       }
       // ... 原有逻辑
   }
   ```

**关键事项**：
- `Overload` 找不到 `.env` 时返回 error，被 `_` 忽略，等价于现状（生产环境无 `.env` 不会出错）
- 必须在所有读 env 的代码**之前**调用（含 viper / `os.Getenv` 直接读）
- `db_check` / `wealth_test` 是硬编码连接串的工具（memory `neondb_password_rotation_todo`），不读 env，无需改

**锁与性能影响**：无锁。`godotenv.Overload` 是文件 I/O + 字符串解析 + `os.Setenv`，只在进程启动时执行一次，纳秒级影响。

**生产保护**：
- 部署前确认 Dockerfile / `.vercelignore` / `.dockerignore` 排除 `.env` 文件，避免误打包到生产镜像
- 即使 `.env` 被误打包，`APP_ENV=production` 守护下仍不会被读取

**DoD**：
- [ ] `internal/config/config.go` `Load()` 入口加 godotenv.Overload + APP_ENV 守护
- [ ] 8 个 cmd 工具入口全部加同样代码（**漏一个等于没改**）
- [ ] `db_check` / `wealth_test` **不改**（已硬编码连接串，无意义）
- [ ] 新增测试 `internal/config/config_test.go`：
  - shell env 有 `DATABASE_URL=postgres://wrong`，`.env` 有 `DATABASE_URL=postgres://right` → `Load()` 返回 right（覆盖生效）
  - `APP_ENV=production` 时 shell env 优先（生产保护生效）
  - `.env` 不存在时不报错（等价现状）
- [ ] 验证 `.dockerignore` 含 `.env`（避免误打包）
- [ ] 启动一遍后端：`./monera-digital`（shell 不显式传 `DATABASE_URL`）→ 启动日志显示连接 `monera_local`，不是 shell 里的 `trader_flow`

**验证命令**：
```bash
# 单元测试
go test ./internal/config/... -race -v

# 手工验证（关键）：临时把 shell env 设错，看启动是否还连对
export DATABASE_URL="postgres://wrong-host/wrong-db?sslmode=disable"
./monera-digital   # 预期：连接到 .env 里的 monera_local，不是 shell 的 wrong-host
unset DATABASE_URL

# 验证 cmd 工具同样受益
export DATABASE_URL="postgres://wrong-host/wrong-db?sslmode=disable"
go run cmd/migrate/main.go   # 预期：操作 monera_local
unset DATABASE_URL
```

---

### T11.10 — 安全加固验收基线

**功能验收**：
- [ ] **S-HARDEN-1**：构造 CREDITED deposit → 调用 MarkDepositFailed → deposit 状态仍为 CREDITED
- [ ] **S-HARDEN-2**：构造 CREDITED deposit → 调用 MarkDepositManualReview → deposit 状态仍为 CREDITED
- [ ] **S-HARDEN-3**：`SAFEHERON_WEBHOOK_ALLOWED_IPS=1.2.3.4` 时，非白名单 IP 请求 webhook → 403
- [ ] **S-HARDEN-4**：白名单为空时，任何 IP 正常进入验签流程
- [ ] **S-HARDEN-5**：`UPDATE account SET balance = -1` → CHECK violation 报错
- [ ] **S-HARDEN-6**：`APP_ENV=production + KYT_ENABLED=false + SetKYTDeps` → panic
- [ ] **S-HARDEN-7**：已为 KYT_PENDING 的 deposit → MoveToKYTPending 返回 error，不触发 KYT API
- [ ] **S-HARDEN-8**：LookupAddressOwner 传入正确 networkFamily → 精确匹配

**测试覆盖**：
```bash
go test ./internal/wallet/deposit/... -race -cover  # ≥ 80%
go vet ./internal/...                                # 无 warning
```

---

## T12. Sandbox 端到端 + 灰度上线

> **注**：原 T10，2026-05-12 T10 KYT 合规筛查插入后顺延为 T11；2026-05-13 T11 安全加固插入后再次顺延为 T12。子任务 T10.X → T11.X → T12.X。

**依赖**：T1-T11（T11 安全加固必须先完成，否则状态覆写/IP 暴露等风险点会带到生产）
**估时**：1d
**输出**：测试报告 + 上线 checklist

### T12.1 — Sandbox E2E 矩阵（充值主路径）

按 SPEC §11.1 必须各成功 1 笔：

| 链 | 币 | 转账金额（最小） | 验证步骤 |
|----|----|------------------|---------|
| Sepolia | ETH | 0.0001 | 前端拿地址 → testnet 钱包发 → webhook → CREDITED |
| Sepolia | USDC | 0.1（USDC `0x1c7D...7238`） | 同上 |
| Shasta | TRX | 0.1 | 同上 |

每次转账后核对：
- [ ] `safeheron_webhook_events` 至少一条 `event_type=TRANSACTION_*`
- [ ] `deposits.safeheron_tx_key` 与 Safeheron 控制台一致
- [ ] `deposits.status='CREDITED'` 且 `aml_screening_state` 非空（KYT_ENABLED=true）/ 或 `KYT_ENABLED=false` 走绕过分支
- [ ] `account.balance` 增加值等于 `txAmount`
- [ ] `journal.biz_type=10`、`ref_id=deposits.id`、`amount=txAmount`

### T12.2 — KYT 真实告警路径实测（v1.5 新增）

**前提**：在公司电脑配置好 Safeheron 密钥 + Console AML 已开启 + Webhook 通知已启用。

- [ ] **KYT-E2E-1**：sandbox 正常充值（地址干净）→ Safeheron 自动 KYT 评估 → `amlScreeningTriggeredState=TRIGGERED + LOW` → CREDITED + `deposits.aml_list` JSONB 含 MistTrack 评估结果
  - [ ] 同步打印收到的 `AML_KYT_ALERT` 原始 payload（`SELECT raw_payload FROM safeheron_webhook_events WHERE event_type='AML_KYT_ALERT' ORDER BY id DESC LIMIT 1`），对照 T10.6 改造点 1 的 `AMLKYTAlertDetail` struct 字段**完整对齐**（字段名 + 类型 + 大小写）；如字段名不一致或缺字段，**以实测 payload 为准回头更新 struct**，并把对齐结果记入 review log（N-S2 修正）
- [ ] **KYT-E2E-2**：构造或选择一个 Safeheron 自带的"测试用风险地址"（咨询客服可获得 sandbox 风险地址样本）→ 充值后收到 `AML_KYT_ALERT` webhook → deposit 进 MANUAL_REVIEW + 飞书 ERROR 告警
- [ ] **KYT-E2E-3**：观察 Console 中的 KYT 报告查询次数，确认实际调用 ≈ 1 次/笔（初查），无超时兜底触发
- [ ] **KYT-E2E-4**：制造一笔需要等待 KYT 评估的充值（如果 sandbox 不能模拟可跳过，标 N/A），观察 KYT_PENDING 状态 → 收到 AML_KYT_ALERT 后推进
- [ ] **KYT-E2E-5**：手动 SQL 把一笔 deposit 设为 `status='KYT_PENDING' AND updated_at = NOW() - INTERVAL '21 min'`，下一个 ticker（≤1min）触发兜底 API 调用，日志可见

### T12.3 — 异常路径覆盖（人工构造）

- [ ] **AC-1**：转账到未分配地址 → 用 SDK 手动建一个 hidden 钱包，往里打 testnet ETH → webhook 进 MANUAL_REVIEW + 飞书消息
- [ ] **AC-2**：金额低于 min_deposit → 发 0.00001 Sepolia ETH（< 0.0001）→ MANUAL_REVIEW
- [ ] **AC-3**：webhook 验签失败 → curl 伪造 payload → 401 + 无 DB 写入
- [ ] **AC-4**：webhook 重发 → Safeheron 控制台触发 `/v1/webhook/resend` → DB 无重复 deposits 行
- [ ] **AC-5**：worker 中途崩溃 → kill -9 进程 → 重启后未完成事件仍能处理
- [ ] **AC-6**（v1.5 新增）：手动给 worker 注入 KYT API 失败（mock 或临时改错 base url），事件回 PENDING 自动重试，process_attempts 增长正确

### T12.4 — 非功能验证

- [ ] `wrk -t4 -c10 -d30s` 打 webhook handler，P99 latency < 2s
- [ ] 用日志直方图统计 webhook 落库 → CREDITED 延迟 P99 < 30s（KYT_ENABLED=true 下，含 KYT API 调用时间，可适当放宽到 P99 < 60s）
- [ ] `go test ./internal/... -cover` 覆盖率 ≥ 80%
- [ ] `go vet ./...` 无 warning
- [ ] `npm run build` + `npm run test` 通过

### T12.5 — 上线 Checklist（部署前 ops 确认）

- [ ] 生产 Safeheron API Key 已申请，权限含「读取 + 钱包账户管理 + 合规筛查 (Compliance)」（**不**含「发起/取消交易」）
- [ ] 生产 RSA 密钥对已生成，私钥已注入生产 env `SAFEHERON_PRIVATE_KEY_PEM`
- [ ] Safeheron 平台公钥、Webhook 公钥已注入对应 env
- [ ] 生产出口 IP 固定且已加白名单（至少 2 个 IP）
- [ ] Webhook 接收 URL 配置到 Safeheron 控制台（生产）
- [ ] `APP_ENV=production` 确认；`KYT_ENABLED` 未设置（默认 true）或显式 true，**不能为 false**（启动会 panic）
- [ ] **Safeheron Console KYT 配置已确认**（v1.5 新增）：
  - [ ] 管理 → API → AML 功能已开启
  - [ ] 风险等级映射已配置（MistTrack 评分 → LOW/MEDIUM/HIGH/SEVERE）
  - [ ] 风险等级配置中**Webhook 通知已启用**（否则不推 AML_KYT_ALERT）
- [ ] `ALERT_WEBHOOK_URL` 指向生产飞书机器人，`ALERT_EMAIL_RECIPIENTS` 配置正确
- [ ] 数据库迁移 015 已在生产执行（phase1 仅 1 个 migration 文件，内部 step 含 chains/coins/coin_chains/address_pool/webhook_events 表 + deposits 扩展 + T10 KYT 字段 + **T11 account 余额非负 CHECK 约束**）
- [ ] `cmd/pool_init --evm-count=100 --tron-count=100` 已在生产跑过，`address_pool` 各 100 个 AVAILABLE
- [ ] 健康检查接口（如有）确认 Safeheron 连通性 + KytReport API 连通性
- [ ] **T11 安全加固上线项**（v1.6 新增）：
  - [ ] `SAFEHERON_WEBHOOK_ALLOWED_IPS` 已填入 Safeheron 官方 webhook 源 IP（逗号分隔，不能留空）
  - [ ] 015 执行前先确认 `SELECT count(*) FROM account WHERE balance < 0 OR frozen_balance < 0` 返回 0（避免 `AddAccountBalanceConstraints` step 因历史违反数据而失败）
  - [ ] 015 执行后 `psql -c "\d account"` 确认含 `ck_balance_non_negative` + `ck_frozen_non_negative` 两条 CHECK 约束
  - [ ] `journal.biz_type=10` 入账记录数 = `deposits WHERE status='CREDITED'` 记录数（手工一致性校验，对账系统未建前的兜底）

### T12.6 — 灰度上线策略

- 阶段 1：先上前端 + 后端代码，**不**改 Safeheron 控制台 webhook URL（webhook 仍指向 staging）
- 阶段 2：staging 用生产 Safeheron team 真小额做 5 个 mainnet 币种最终验证（SPEC §13 dev/test 覆盖差距）+ KYT 路径验证（KYT-E2E-1/2 至少各一次）
- 阶段 3：切换生产 webhook URL，观察 1 小时：
  - 无 ERROR 日志、无非预期 MANUAL_REVIEW
  - 监控 `failed_reason LIKE 'KYT_%'` 占比 < 5%（v1.5 新增）
  - 监控 `status='KYT_PENDING'` 最长停留时间 < 5min（v1.5 新增）
- 阶段 4：通知业务可对外宣传 deposit 功能

---

## 完成标志

全部任务 DoD 勾选完毕后：

1. PR 描述按 `plan.md` §8 列出全部交付物
2. 在 SPEC §16 添加 v1.5 KYT 实施落地记录
3. 关闭 todo task
4. 团队公告 Phase 1 上线，二期排期开始
