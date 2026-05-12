# Safeheron Phase 1 任务清单

> 配套文档: `docs/plans/safeheron-phase1-plan.md`（含决策记录、依赖图、验收基线）
> SPEC: `docs/spec/safeheron-phase1-spec.md` v1.4
> Last updated: 2026-05-11

任务格式约定：
- **依赖**：必须先完成的 task ID
- **DoD（Definition of Done）**：每条都是可观察的、可被外部人 5 分钟验证的
- **验证命令**：能直接 copy-paste 跑的命令

---

## T1. 数据库迁移 + Seed

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

## T2. Safeheron SDK Adapter

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

## T3. Registry

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

## T4. `cmd/pool_init/main.go` 预生成脚本

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

## T5. Pool Manager + Replenisher

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

## T6. 用户侧 API + Vercel 路由

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

## T7. Webhook 处理（同步 + 异步 worker）

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

## T8. 前端切换

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

## T9. 充值页面 UX 重构（选币 → 选链 → 展示地址）

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

## T10. Sandbox 端到端 + 灰度上线

> **注**：原 T9，2026-05-12 T9 充值页面 UX 重构插入后顺延为 T10。子任务 T9.X → T10.X。

**依赖**：T1-T9
**估时**：1d
**输出**：测试报告 + 上线 checklist

### T10.1 — Sandbox E2E 矩阵

按 SPEC §11.1 必须各成功 1 笔：

| 链 | 币 | 转账金额（最小） | 验证步骤 |
|----|----|------------------|---------|
| Sepolia | ETH | 0.0001 | 前端拿地址 → testnet 钱包发 → webhook → CREDITED |
| Sepolia | USDC | 0.1（USDC `0x1c7D...7238`） | 同上 |
| Shasta | TRX | 0.1 | 同上 |

每次转账后核对：
- [ ] `safeheron_webhook_events` 至少一条 `event_type=TRANSACTION_*`
- [ ] `deposits.safeheron_tx_key` 与 Safeheron 控制台一致
- [ ] `deposits.status='CREDITED'`
- [ ] `account.balance` 增加值等于 `txAmount`
- [ ] `journal.biz_type=10`、`ref_id=deposits.id`、`amount=txAmount`

### T10.2 — 异常路径覆盖（人工构造）

- [ ] **AC-1**：转账到未分配地址 → 用 SDK 手动建一个 hidden 钱包，往里打 testnet ETH → webhook 进 MANUAL_REVIEW + 飞书消息
- [ ] **AC-2**：金额低于 min_deposit → 发 0.00001 Sepolia ETH（< 0.0001）→ MANUAL_REVIEW
- [ ] **AC-3**：webhook 验签失败 → curl 伪造 payload → 401 + 无 DB 写入
- [ ] **AC-4**：webhook 重发 → Safeheron 控制台触发 `/v1/webhook/resend` → DB 无重复 deposits 行
- [ ] **AC-5**：worker 中途崩溃 → kill -9 进程 → 重启后未完成事件仍能处理

### T10.3 — 非功能验证

- [ ] `wrk -t4 -c10 -d30s` 打 webhook handler，P99 latency < 2s
- [ ] 用日志直方图统计 webhook 落库 → CREDITED 延迟 P99 < 30s
- [ ] `go test ./internal/... -cover` 覆盖率 ≥ 80%
- [ ] `go vet ./...` 无 warning
- [ ] `npm run build` + `npm run test` 通过

### T10.4 — 上线 Checklist（部署前 ops 确认）

- [ ] 生产 Safeheron API Key 已申请，权限含「读取 + 钱包账户管理」（**不**含「发起/取消交易」）
- [ ] 生产 RSA 密钥对已生成，私钥已注入生产 env `SAFEHERON_PRIVATE_KEY_PEM`
- [ ] Safeheron 平台公钥、Webhook 公钥已注入对应 env
- [ ] 生产出口 IP 固定且已加白名单（至少 2 个 IP）
- [ ] Webhook 接收 URL 配置到 Safeheron 控制台（生产）
- [ ] `APP_ENV=production` 确认
- [ ] `ALERT_WEBHOOK_URL` 指向生产飞书机器人，`ALERT_EMAIL_RECIPIENTS` 配置正确
- [ ] 数据库迁移 015-021 已在生产执行
- [ ] `cmd/pool_init --evm-count=100 --tron-count=100` 已在生产跑过，`address_pool` 各 100 个 AVAILABLE
- [ ] 健康检查接口（如有）确认 Safeheron 连通性

### T10.5 — 灰度上线策略

- 阶段 1：先上前端 + 后端代码，**不**改 Safeheron 控制台 webhook URL（webhook 仍指向 staging）
- 阶段 2：staging 用生产 Safeheron team 真小额做 5 个 mainnet 币种最终验证（SPEC §13 dev/test 覆盖差距）
- 阶段 3：切换生产 webhook URL，观察 1 小时无 ERROR 日志、无 MANUAL_REVIEW
- 阶段 4：通知业务可对外宣传 deposit 功能

---

## 完成标志

全部任务 DoD 勾选完毕后：

1. PR 描述按 `plan.md` §8 列出全部交付物
2. 在 SPEC §16 添加 v1.5 实施落地记录
3. 关闭 todo task #15
4. 团队公告 Phase 1 上线，二期排期开始
