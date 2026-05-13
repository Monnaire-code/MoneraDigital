# Safeheron Phase 1 实施计划

> Status: **Draft for review**
> Last updated: 2026-05-12（v1.5 KYT 合规筛查补充）
> 对应 SPEC: `docs/spec/safeheron-phase1-spec.md` v1.5
> 任务清单: `docs/plans/safeheron-phase1-todo.md`

---

## 0. 元信息

| 项 | 值 |
|---|---|
| SPEC 版本 | v1.5（KYT 合规筛查，2026-05-12 并入 Phase 1）|
| 验收基线 | SPEC §11 全部勾选 |
| Sandbox 实测基线 | V2/V3/V4/V5/V6/V7 全通（2026-05-11） |
| 当前已完成 | D1（spec、sandbox 实测、coinKey 锁定） |
| 待启动 | D2 - D7 |
| 总工期 | 一周 |

**本计划的承诺**：所有"看起来可能讨论"的细节已在本文档 §4 决策记录中锁死。施工时只剩**真正需要看代码上下文**才能决定的事（清单见 §5）。

---

## 1. 依赖图（垂直切片视角）

```
                            ┌────────────────────────────┐
                            │ T1 数据库迁移 + Seed       │
                            │ (chains/coins/coin_chains  │
                            │  /address_pool/webhook_evt │
                            │  /deposits 扩展)           │
                            └─────────────┬──────────────┘
                                          │
                  ┌───────────────────────┼───────────────────────┐
                  ▼                       ▼                       ▼
       ┌────────────────────┐   ┌────────────────────┐  ┌────────────────────┐
       │ T2 Safeheron       │   │ T3 Registry        │  │ (T1 完成即可)     │
       │    SDK adapter     │   │    内存索引        │  │                    │
       │  (无业务, 纯封装)  │   │  + 后台刷新        │  │                    │
       └─────────┬──────────┘   └─────────┬──────────┘  │                    │
                 │                        │             │                    │
                 └────────────┬───────────┘             │                    │
                              ▼                         │                    │
                ┌─────────────────────────┐             │                    │
                │ T4 pool_init 命令       │             │                    │
                │ (sandbox 真创 1 个钱包) │             │                    │
                └─────────────┬───────────┘             │                    │
                              ▼                         │                    │
            ┌─────────────────────────────────┐         │                    │
            │ T5 pool/manager 分配 +          │         │                    │
            │    pool/replenisher 补水        │         │                    │
            └─────────────┬───────────────────┘         │                    │
                          ▼                             │                    │
            ┌─────────────────────────────────┐         │                    │
            │ T6 /api/wallet/deposit-address  │         │                    │
            │    /api/wallet/supported-chains │         │                    │
            │    Vercel ROUTE_CONFIG 更新     │         │                    │
            └─────────────┬───────────────────┘         │                    │
                          ▼                             ▼                    │
            ┌─────────────────────────────────────────────────────┐          │
            │ T7 Webhook handler (同步: 验签 + 落库 + ack)        │          │
            │    + worker (异步: UPSERT + status_rank + 入账)     │◀─────────┘
            │    + 飞书/邮件告警                                  │
            │    + Vercel /api/webhooks/safeheron 路由            │
            └─────────────┬───────────────────────────────────────┘
                          ▼
            ┌─────────────────────────────────┐
            │ T8 前端切换                     │
            │ (wallet-service 调新端点 +      │
            │  Deposit 页面适配 supportedCoins│
            │  + 移除老 wallet/create 调用)   │
            └─────────────┬───────────────────┘
                          ▼
            ┌─────────────────────────────────┐
            │ T9 充值页面 UX 重构              │
            │ (选币→选链→展示地址 +           │
            │  deposit-coins 端点 +           │
            │  DB 展示字段 migration 022)     │
            └─────────────┬───────────────────┘
                          ▼
            ┌──────────────────────────────────────────────┐
            │ T10 KYT 合规筛查（v1.5 spec 新增）            │
            │ (015 加 4 AML 字段 + KYT_PENDING 状态 +       │
            │  ProcessOne 拆两阶段 + KYT Report API +      │
            │  AML_KYT_ALERT webhook + 超时兜底扫描 +      │
            │  KYT_ENABLED 启动校验 + 前端文案)            │
            └─────────────┬────────────────────────────────┘
                          ▼
            ┌──────────────────────────────────────────────┐
            │ T11 充值流水线安全加固（v1.6 审计补充）       │
            │ (状态覆写保护 + Webhook IP 白名单 + .env 优先 +│
            │  余额非负 CHECK + KYT 二次校验 +             │
            │  fallback FOR UPDATE + RowsAffected 检查)    │
            └─────────────┬────────────────────────────────┘
                          ▼
            ┌─────────────────────────────────┐
            │ T12 Sandbox 端到端 + 灰度上线   │
            │ (3 链 × 2-3 币 + KYT 路径 +     │
            │  异常路径覆盖)                  │
            └─────────────────────────────────┘
```

**关键依赖说明**：

- T2 / T3 / T1 是并行根，T1 是其他所有任务的硬依赖（schema 不就绪就不能跑）
- T4 是首个能在 sandbox **跑通**的节点 → 首个 demo 检查点
- T5+T6 共同完成"用户能拿地址"垂直切片 → 第二个 demo 检查点
- T7 是 webhook 入账闭环 → 第三个 demo 检查点（核心交付）
- T8 是前端切换 → 第四个 demo 检查点（用户可见）
- T9 是 T8 落地后的 UX 二次迭代（对齐币安/欧易），独立交付
- **T10 是 v1.5 spec 新增的 KYT 合规筛查**，依赖 T7 的入账状态机；ProcessOne 被改造为两阶段（KYT API 调用脱离 DB 事务），整笔 deposit 必须经 KYT 检查才能 CREDITED
- **T11 是 v1.6 安全审计补充**，依赖 T10 的状态机和 webhook handler；必须在 T12 端到端验收前完成，否则状态覆写/IP 暴露等风险点会带到生产
- T12 是灰度上线前的最终验收（原 T10 → T11 → T12 两次顺延）

---

## 2. 阶段划分（10 个检查点）

每个阶段对应**一个可独立 demo 的能力**，跨阶段必须通过检查点才能继续。

| 阶段 | 包含任务 | 检查点（demo 目标） | 期望工期 |
|------|---------|-------------------|---------|
| **P1 基础设施** | T1, T2, T3 | `go test ./internal/safeheron/... ./internal/wallet/config/...` 全过；数据库新表/字段全部就位 | D2 |
| **P2 地址池贯通** | T4, T5 | sandbox 真创 100 个 EVM + 100 个 TRON 钱包，`SELECT count(*) FROM address_pool` 各返回 100 | D3 |
| **P3 用户拿地址** | T6 | 用户两次 curl `/api/wallet/deposit-address?network_family=EVM` 返回同一地址；并发 10 个用户拿到 10 个不同地址 | D4 |
| **P4 充值入账闭环** | T7 | sandbox Sepolia ETH 真转账 → webhook 触发 → `deposits.status=CREDITED` + `account.balance` 增加 + `journal` 写入；webhook 重发不重复入账 | D5 |
| **P5 前端切换** | T8 | 前端 Dashboard 显示 EVM/TRON 地址 + supportedCoins 列表 | D6 |
| **P6 充值 UX 重构** | T9 | 选币→选链→展示地址三步流程；deposit-coins 端点；DB 加 short_name/token_standard/estimated_arrival_minutes 列 | D6 |
| **P7 KYT 合规筛查** | T10 | `KYT_ENABLED=true` 下，构造 webhook payload 跑通 9 个分支：UNTRIGGERED→MR / TRIGGERED+LOW→CREDITED / MEDIUM→MR / HIGH→MR(ERROR) / SEVERE→MR(ERROR) / FAILED→MR / SKIPPED→MR / IN_PROGRESS→KYT_PENDING 后 AML_KYT_ALERT 推进 / 20min 超时兜底；`KYT_ENABLED=false` 下走原流程 | D7 |
| **P9 安全加固** | T11 | 状态覆写保护 + IP 白名单 + 余额 CHECK + KYT 二次校验 + fallback FOR UPDATE + MoveToKYTPending 幂等 + LookupAddressOwner 精确匹配 + **.env 优先 shell env** | D8 |
| **P10 端到端验收 + 灰度** | T12 | 3 链 × 2-3 币种端到端各成功 1 次 + KYT 真实告警路径（明天 sandbox 实测） + 异常路径覆盖 + 灰度上线 | D9 |

**检查点强制要求**：每阶段结束**必须** demo 给团队看（或录屏），未通过不进入下一阶段。

---

## 3. 决策记录（已锁，施工时不再讨论）

> 以下决策已经在 SPEC v1.4 / sandbox 实测 / 项目约定中固化。施工阶段如发现矛盾，**先停**并提出，不要默默改方向。

### 3.1 数据库 / 迁移

| 决策 | 取值 | 来源 |
|------|------|------|
| 迁移文件编号 | **phase1 阶段只有 1 个 migration 文件 `015_safeheron_phase1.go`**，所有改动以 step 形式并入（含 T9 充值展示字段、T10 KYT AML 字段、T11 余额 CHECK 约束）；step struct 保留独立 Up/Down 方法供测试 | 项目现状 + memory `feedback_migration_consolidation` |
| Seed 实现方式 | Go `Up` 函数内嵌 SQL，与 014 风格一致 | `internal/migration/migrations/014_*.go` |
| `coin_chains.safeheron_coin_key` seed 来源 | 读 `os.Getenv("APP_ENV")`，`production` 走 mainnet 8 行，其余走 testnet 3 行 | SPEC §4.7 |
| `coin_chains.min_deposit_amount` 类型 | `VARCHAR(64)` 存字符串，业务代码用 `shopspring/decimal` 解析 | SPEC §4.3，金额不用 float |
| `deposits` UNIQUE 索引 | `(safeheron_tx_key) WHERE safeheron_tx_key IS NOT NULL` 部分索引 | SPEC §4.6（v1.3 修正） |
| `deposits.status_rank` 取值表 | SUBMITTED=10, SIGNING=20, BROADCASTING=30, CONFIRMING=50, FAILED/CANCELLED/REJECTED=90, COMPLETED=100 | SPEC §4.6 |
| `account` 唯一索引 | `idx_account_user_currency ON (user_id, currency)` | SPEC §4.6 |
| 迁移可回滚 | 每个迁移必须实现 Down 方法 | SPEC §12.1 |
| `internal/coreapi/` 包 | 不删，service 层停止调用即可（避免破坏 test 编译） | SPEC §3.2 |
| `user_wallets` / `wallet_creation_requests` 表 | 保留不动，Phase 1 后停止写入；老地址展示兼容由二期处理 | SPEC §3.2 |

### 3.2 Safeheron SDK / Adapter

| 决策 | 取值 | 来源 |
|------|------|------|
| 包路径 | `internal/safeheron/` | SPEC §3.1 |
| Go SDK | `github.com/Safeheron/safeheron-api-sdk-go`（最新版 `go get` 时确定） | SPEC §9 |
| 私钥来源 | env `SAFEHERON_PRIVATE_KEY_PEM` 直接放 PEM 字符串（不再用文件路径，避免容器挂载麻烦） | SPEC §9.6，但**简化为单字符串**：sandbox 已验证 SDK 支持字符串输入（实测 demo 已通） |
| 平台 / Webhook 公钥来源 | env `SAFEHERON_PLATFORM_PUBLIC_KEY_PEM` + `SAFEHERON_WEBHOOK_PUBLIC_KEY_PEM` | SPEC §9.6 |
| SDK adapter 必须封装的方法 | `CreateAssetWallet` / `AddCoin` / `ListAccountCoin` / `GetAccountByAddress` / `WebhookConvert`（其余按需添加） | SPEC §6.1 / §6.3 / §6.4 |
| 重试策略 | 5s / 30s / 120s 指数退避，3 次失败后落 ERROR | SPEC §6.1 |
| Webhook 验签 | 完全交给 SDK `webhook.WebhookConverter.Convert(env)`，**不自己拼签名串** | SPEC §10 |
| **SDK 私钥/公钥输入格式（重要修正）** | SDK 读 **PEM 文件路径**（实测 `client.go`：`ApiConfig.RsaPrivateKey` 字段是文件路径字符串，**不是 PEM 内容**）。Phase 1 实现：env 注入 PEM 字符串 → 启动时写 `/tmp/safeheron-{name}.pem` 0600 权限 → 把路径传给 SDK。退出时清理。 | `~/scratch/safeheron-sandbox-test/client.go:137-170` 实测 |
| SDK import 路径 | `github.com/Safeheron/safeheron-api-sdk-go/safeheron`, `.../safeheron/api`, `.../safeheron/webhook` 三个子包 | `client.go:10-13` 实测 |
| SDK client 构造 | `safeheron.Client{Config: safeheron.ApiConfig{BaseUrl, ApiKey, RsaPrivateKey, SafeheronRsaPublicKey, RequestTimeout}}`；各 API 用 `api.AccountApi{Client: cl}` / `api.CoinApi{Client: cl}` / `api.TransactionApi{Client: cl}` 包装 | `client.go:137-157` 实测 |
| WebhookConverter 构造 | `webhook.WebhookConverter{Config: webhook.WebHookConfig{SafeheronWebHookRsaPublicKey, WebHookRsaPrivateKey}}`，两字段也是**文件路径** | `client.go:159-170` 实测 |
| 启动校验 | `APP_ENV=production` 时缺失任一 Safeheron env → panic；启动前先把 3 个 PEM 写入临时文件，**任一写入失败也 panic** | SPEC §9.5 + O-1 锁定 |

### 3.3 Registry

| 决策 | 取值 | 来源 |
|------|------|------|
| 加载时机 | `container.NewContainer()` 内同步加载，失败 panic | SPEC §5.2 |
| 刷新频率 | 60s 后台 goroutine，失败保留旧值 + 告警 | SPEC §5.2 |
| 内存结构 | `chains` / `coins` / `coinsByID` / `coinChains` / `bySHKey` / `byChain` 6 个 map | SPEC §5.1 |
| 并发 | `sync.RWMutex` + 整体替换新 map（不做 copy-on-write） | SPEC §5.2 |
| 缓存项扩展（如运营改了 min_deposit） | 下次 60s 刷新自动生效，**不暴露主动 invalidate 接口** | KISS：Phase 1 无运营后台 |

### 3.4 地址池

| 决策 | 取值 | 来源 |
|------|------|------|
| 表设计 | EVM + TRON 合表 `address_pool`，按 `network_family` 区分 | SPEC §4.4 |
| 预生成数量 | EVM 100、TRON 100 | SPEC §6.1 / §9.6 |
| 补水水位 | 低于 50 时补到 100，10 分钟检查一次 | SPEC §6.2 / §9.6 |
| 补水 scheduler 实现 | **不复用** `InterestScheduler`（那是每日 UTC 00:00:05 阻塞 `time.Sleep`，不适合周期任务）。新建 `internal/wallet/pool/replenisher.go`：`time.NewTicker(POOL_REPLENISH_INTERVAL)` + 独立 goroutine + `ctx.Done()` 优雅退出 | `internal/scheduler/interest.go:35-113` 实测 |
| 钱包参数（创建时固定） | `accountTag="DEPOSIT" / hiddenOnUI=true / autoFuel=false / customerRefId=uuid` | SPEC §6.1 / §9.4 |
| AddCoin coinKey 集合来源 | `SELECT safeheron_coin_key FROM coin_chains WHERE chain_code IN (SELECT code FROM chains WHERE network_family=?) AND deposit_enabled=true` | SPEC §6.1 |
| AddCoin 失败处理 | 任一 coinKey 失败 → 该地址进 `ERROR` 状态 + 告警，**不**回退已成功的 coinKey | SPEC §13 |
| 分配并发保护 | `BEGIN; SELECT FOR UPDATE SKIP LOCKED; UPDATE; COMMIT;` | SPEC §6.3 |
| 已分配地址 | **永不**回收到 AVAILABLE | SPEC §12.3 |

### 3.5 Webhook 处理

| 决策 | 取值 | 来源 |
|------|------|------|
| 同步路径 | **IP 白名单校验 (T11 D-42)** + 验签 + INSERT `safeheron_webhook_events` ON CONFLICT DO NOTHING + 返回 ack | SPEC §6.4 / D-42 |
| IP 白名单 | env `SAFEHERON_WEBHOOK_ALLOWED_IPS`（逗号分隔），空值 = 不校验（本地兼容）；放在 RSA 验签**之前**避免攻击者用任意 IP 触发 CPU 密集型验签 | T11 D-42 |
| Ack body | **字面量** `{"code":"200","message":"SUCCESS"}`，Content-Type `application/json`，HTTP 200 | SPEC §6.4 / §10（V6 实测踩过） |
| 同步 SLA | P99 < 2s，handler 内不做业务（worker 来做） | SPEC §11.2 |
| 异步 worker 启动方式 | 单 goroutine，1s polling loop `SELECT FOR UPDATE SKIP LOCKED ... LIMIT 1` | KISS（不引入消息队列） |
| 幂等键 | `event_id = txKey + ':' + transactionStatus`，UNIQUE 约束 | SPEC §6.4 |
| eventType 过滤 | 只处理 `TRANSACTION_CREATED` / `TRANSACTION_STATUS_CHANGED`，其余 12 种标 DONE 不入账 | SPEC §6.4 |
| 入账唯一条件 | `transactionStatus='COMPLETED' AND transactionSubStatus='CONFIRMED' AND transactionDirection='INFLOW'` | SPEC §6.4 |
| UPSERT 守卫 | `WHERE deposits.status_rank <= EXCLUDED.status_rank` | SPEC §4.6 / §6.4 |
| 整个 worker 流程 | 单事务（webhook_events + deposits + account + journal），崩溃 ROLLBACK | SPEC §6.4 |
| 异常事件 | `MANUAL_REVIEW` + 告警，`webhook_events.process_status=DONE` 不再重试 | SPEC §6.5 |
| 失败终态（FAILED/CANCELLED/REJECTED） | `deposits.status=FAILED` + `failed_reason=transactionSubStatus` + 告警，不写 journal | SPEC §6.4 |

### 3.6 API / 路由

| 决策 | 取值 | 来源 |
|------|------|------|
| 用户端 API 路径 | `GET /api/wallet/deposit-address?network_family=EVM\|TRON`, `GET /api/wallet/supported-chains` | SPEC §8.1 |
| Webhook 路径 | `POST /api/webhooks/safeheron` | SPEC §8.2 |
| Vercel ROUTE_CONFIG 改动 | 新增 3 行（见 SPEC §8.3），**保留**老 `POST /api/wallet/create` 等行（标记 DEPRECATED 但不删） | SPEC §8.3 / §3.2 |
| 老 `POST /api/wallet/create` / `POST /api/wallet/addresses` | Go 端 handler 直接返回 410 Gone + "DEPRECATED, use /api/wallet/deposit-address" | 新决策（避免前端误用） |
| 响应 JSON | camelCase（CLAUDE.md 强制） | CLAUDE.md |
| 金额传输 | 字符串（避免 float 精度） | SPEC §12.1 |

### 3.7 前端

| 决策 | 取值 | 来源 |
|------|------|------|
| **改动文件（实测修正）** | (1) `src/lib/wallet-service.ts` — 新增 2 方法 + 删除 `createWallet`/`getWalletInfo`/`addAddress`；(2) **`src/pages/dashboard/Deposit.tsx` — 从 94 行"Coming Soon"占位页改写为充值地址页**；(3) **`src/pages/dashboard/Addresses.tsx` 不动**（它是 507 行提现地址白名单，属二期提现） | 实测当前 `Deposit.tsx` 没接任何 API |
| 新方法签名 | `getDepositAddress(networkFamily: 'EVM' \| 'TRON'): Promise<{address, networkFamily, supportedCoins}>`; `getSupportedChains(): Promise<...>` | SPEC §8.1 |
| 移除调用 | `createWallet` / `getWalletInfo` / `addAddress` 在前端引用全部删除；这些旧端点 Go 端返回 410 兜底 | SPEC §3.2 |
| Deposit 页面 UI | 上方 Tabs 切 EVM/TRON，下方 Card 显示地址（含复制按钮 + 二维码）+ supportedCoins 列表（链/币/最小金额三列） | T8 设计决策 |
| i18n key 命名 | 新增 `deposit.addressCard.{label,hint,copy,copied,copyFailed,errorTitle,qrAlt}` / `deposit.tabs.{evm,tron}` / `deposit.supportedCoins.{title,chain,coin,minDeposit,empty}`；保留旧 `deposit.comingSoon.*` 直到下个 release 清理 | T8 实施修正（原 plan 写 `deposit.evm.*` / `deposit.tron.*`，实际为避免 EVM/TRON 重复定义，改用 `addressCard` 共享 namespace + `tabs` 分组——结构更合理） |
| 缓存策略 | React Query `staleTime: 5 * 60_000`，地址不会变 | 业务直觉（地址永不变） |

### 3.8 告警

| 决策 | 取值 | 来源 |
|------|------|------|
| 告警通道 | 飞书机器人 webhook（`ALERT_WEBHOOK_URL`）+ 邮件（`ALERT_EMAIL_RECIPIENTS`，复用 `internal/services/email_service.go`） | SPEC §9.6 / §11.2 |
| 飞书消息体 | 简单文本卡片（标题 + 5 行字段：user_id/address/amount/reason/event_id） | KISS |
| 告警时机 | (a) Registry 后台刷新失败; (b) MANUAL_REVIEW 写入时; (c) FAILED/CANCELLED/REJECTED 终态; (d) AddCoin 失败导致 pool ERROR | SPEC §6.4 / §6.5 / §13 |
| 告警 SLA | 5 分钟内推送 | SPEC §11.2 |
| 告警失败 | 日志 ERROR，不阻塞主流程事务 | SPEC §6.5 |

### 3.9 测试

| 决策 | 取值 | 来源 |
|------|------|------|
| 单测目标覆盖率 | safeheron adapter / Registry / pool manager / deposit service ≥ 80% | SPEC §11.2 |
| 单测 mock | 复用现有 `mock_repository_test.go` / `mock_handler_test.go` 风格 | 项目惯例 |
| 关键测试用例（必须有） | (a) webhook 验签失败 → 401; (b) 同 (txKey, status) 重发 → 不重复入账; (c) COMPLETED 后 CONFIRMING 乱序 → 状态不回退; (d) FOR UPDATE SKIP LOCKED 并发分配 → 10 用户拿 10 个不同地址; (e) Registry 刷新失败 → 保留旧值 | SPEC §6.4 / §11.1 |
| Sandbox E2E | T10 阶段手动跑，按 `~/scratch/safeheron-sandbox-test/` 工具验证 | SPEC §11.1 |

### 3.10 环境变量（新增）

按 SPEC §9.6 落定到 `.env.example`。生产部署前 ops 必须确认全套已注入：

```bash
APP_ENV=production
SAFEHERON_API_BASE_URL=
SAFEHERON_API_KEY=
SAFEHERON_PRIVATE_KEY_PEM=
SAFEHERON_PLATFORM_PUBLIC_KEY_PEM=
SAFEHERON_WEBHOOK_PUBLIC_KEY_PEM=
WALLET_CONFIG_REFRESH_INTERVAL=60s
POOL_REPLENISH_INTERVAL=10m
POOL_LOW_WATERMARK_EVM=50
POOL_TARGET_CAPACITY_EVM=100
POOL_LOW_WATERMARK_TRON=50
POOL_TARGET_CAPACITY_TRON=100
ALERT_WEBHOOK_URL=
ALERT_EMAIL_RECIPIENTS=ops@moneradigital.com

# ============ T10 KYT 合规筛查 (v1.5 新增) ============
KYT_ENABLED=true                                   # 仅 APP_ENV != production 允许设为 false
KYT_TIMEOUT=20m
KYT_SCAN_INTERVAL=1m
KYT_ORPHAN_ALERT_MAX_RETRY=100

# ============ T11 安全加固 (v1.6 新增) ============
# Safeheron webhook 源 IP 白名单（逗号分隔，留空 = 不校验，仅本地开发用）
# 生产部署前 ops 必须填入 Safeheron 官方提供的 webhook 源 IP 列表
SAFEHERON_WEBHOOK_ALLOWED_IPS=
```

### 3.11 KYT 合规筛查（v1.5 spec 新增）

| 决策 | 取值 | 来源 / 锁定理由 |
|------|------|----------------|
| KYT 服务商 | **仅 MistTrack 一家**（K-4）。三家服务商（MistTrack/Chainalysis/Elliptic）的 `amlList` 数组结构统一保留，未来加新服务商无需改 schema | SPEC §6.5；用户决策 |
| 主路径 | `AML_KYT_ALERT` webhook（被动接收，90%+ 场景覆盖） | K-11；Safeheron API 有次数限制 |
| 辅路径 | 初始 1 次 `/v1/compliance/kyt/report`（COMPLETED 时）+ 超时 1 次（20min 兜底），共 ≈ 1 次/笔 | K-12 |
| Console 配置（运维） | **AML 功能已开启 + 风险等级 Webhook 通知已启用** | K-2，用户已确认 |
| 处置矩阵（已锁） | LOW→CREDITED；MEDIUM/HIGH/SEVERE/UNKNOWN/FAILED/SKIPPED/UNTRIGGERED→MANUAL_REVIEW | K-1/K-5/K-6/K-7/K-8；SPEC §6.5.1 |
| 告警分级 | HIGH/SEVERE/超时类→ERROR；其余→WARN | K-17 |
| 20min 超时仍 IN_PROGRESS | 直接 MANUAL_REVIEW(`KYT_TIMEOUT_STILL_PENDING`)，**不延长** | K-19 |
| webhook 乱序保护 | AML_KYT_ALERT 找不到 deposit → 事件保留 PENDING 待下次 worker 轮询关联；超过 100 次（`KYT_ORPHAN_ALERT_MAX_RETRY`）转 MANUAL_REVIEW | K-13 |
| 历史数据 | 不回填（线上 Phase 1 之前无真实 deposit） | K-14 |
| 前端展示 | `KYT_PENDING` 状态显示「Under compliance review」 | K-15 |
| 测试开关 | `KYT_ENABLED` env；prod 启动校验：`APP_ENV=production && !KYT_ENABLED` → panic | K-16 |
| 运维放行接口 | Phase 1 **不做**（admin 接口蔓延风险）；列入 Phase 2 TODO | K-18 |
| DB 迁移合并 | **15 个 migration 文件不再增加**，KYT 4 个 AML 字段 + KYT_PENDING 状态 + `idx_deposits_kyt_pending` 部分索引**全部并入 `015_safeheron_phase1.go`**；本地 monera_local 数据库由开发者手动 ALTER 或 DROP 重建（用户授权） | 用户决策 + memory `feedback_migration_consolidation` |
| 单事务 vs 两阶段 | **拆两阶段**：(1) 事务 1 落 webhook event + 解析 + 锁定；(2) 调 KYT API（事务外）；(3) 事务 2 写 KYT 结果 + 入账/MANUAL_REVIEW + 标 DONE。原因：KYT API 是外部 HTTP（100ms ~ 5s），放在事务里会长时间占 `FOR UPDATE` 行锁 | 用户决策（Q2 方案 A） |
| `summarizeRiskLevel` 算法 | 取 `amlList` 中所有 provider `riskLevel` 的最高严重度：SEVERE > HIGH > MEDIUM > UNKNOWN > LOW；任一 `status=PENDING` 视为整体未完成；任一 `status=FAILED`/`SKIPPED` 优先返回（不与 riskLevel 同时存在） | SPEC §6.5.1 |
| 超时扫描实现 | 复用 `DepositWorker` 主 ticker，加 `case <-kytScanTicker.C` 分支（间隔 1m），不另起 goroutine | Phase 1 KISS |
| KYT 客户端接口 | `safeheron.SafeheronClient` 接口扩展 `KytReport(ctx, txKey) (*KytReportResponse, error)`，adapter 内调 SDK `api.ComplianceApi.KytReport()`；项目内 mirror 一份 `KytReportResponse` / `AmlReport` 类型（不直接暴露 SDK 类型） | 与现有 adapter 风格一致（T2.3 决策） |
| `aml_list` 字段写入 | Go side 用 `json.Marshal(amlList)` 转 `[]byte` 存 JSONB；读取时 `json.Unmarshal` 回 `[]AmlReport` | 标准 PG JSONB 用法 |

> 之前 v1 草稿留了 7 个开放点（O-1~O-7），用户质疑后实测一遍代码 / SDK / 前端现状，**全部锁死**。施工时如发现下表与代码冲突，**先停**并提出，不要默默改方向。

| # | 议题 | 锁定决策 | 依据 |
|---|------|---------|------|
| **D-1** | Safeheron Go SDK 路径 | `github.com/Safeheron/safeheron-api-sdk-go/safeheron` + `/safeheron/api` + `/safeheron/webhook` 三个子包 | `~/scratch/safeheron-sandbox-test/client.go:10-13` |
| **D-2** | SDK client 初始化 | `safeheron.Client{Config: safeheron.ApiConfig{BaseUrl, ApiKey, RsaPrivateKey, SafeheronRsaPublicKey, RequestTimeout}}` 后用 `api.AccountApi{Client: cl}` 等包装 | `client.go:137-157` |
| **D-3** | SDK 私钥/公钥输入格式（**重要纠正**） | SDK 接受 **PEM 文件路径**字符串（实测 `ApiConfig.RsaPrivateKey` 字段是路径不是 PEM 内容）。Phase 1 实现：env 传 PEM 字符串 → 启动写 `/tmp/safeheron-{private,platform,webhook}.pem` 0600 权限 → SDK 读路径。退出 defer 删除 | `client.go:137-170` 实测 |
| **D-4** | WebhookConverter 构造 | `webhook.WebhookConverter{Config: webhook.WebHookConfig{SafeheronWebHookRsaPublicKey, WebHookRsaPrivateKey}}`，两字段同样是**文件路径** | `client.go:159-170` |
| **D-5** | 飞书告警消息格式 | 简单文本 `{"msg_type":"text","content":{"text":"【Phase1告警】level=X title=Y\nuser_id=...\naddress=...\namount=...\nreason=...\nevent_id=..."}}` POST 到 `ALERT_WEBHOOK_URL` | 飞书自定义机器人公开协议 |
| **D-6** | Pool Replenisher 实现 | **不复用** `InterestScheduler`（它是每日 UTC 00:00:05 + `time.Sleep` 阻塞）。新建 `internal/wallet/pool/replenisher.go`：`time.NewTicker(POOL_REPLENISH_INTERVAL)` + 独立 goroutine + `ctx.Done()` 优雅退出 + `recover` 防 panic | `internal/scheduler/interest.go:35-113` 实测 |
| **D-7** | 前端改造点 | `Deposit.tsx`（当前 94 行 "Coming Soon" 占位，**不接 API**）改写为正式充值地址页；`Addresses.tsx`（507 行提现白名单，二期范围）**不动** | 实测 `src/pages/dashboard/Deposit.tsx:1-94` |
| **D-8** | i18n key | 新增 `deposit.addressCard.{label,hint,copy,copied,copyFailed,errorTitle,qrAlt}` / `deposit.tabs.{evm,tron}` / `deposit.supportedCoins.{title,chain,coin,minDeposit,empty}`；保留旧 `deposit.comingSoon.*` 直到下个 release | T8 实施时为避免 EVM/TRON 重复定义两套 title/hint/copy，改用 `addressCard` 共享 namespace + `tabs` 分组（结构上更合理） |
| **D-9** | Migration 幂等 | `migrator.Migrate()` 按 version 跳过 applied，但**中途失败**会重跑同一 SQL → **每个 Up SQL 必须自带** `CREATE TABLE IF NOT EXISTS` / `CREATE INDEX IF NOT EXISTS` / `ADD COLUMN IF NOT EXISTS` / `INSERT ... ON CONFLICT DO NOTHING`。`014` 已是这个风格 | `internal/migration/migrator.go:111-151` 实测 |
| **D-10** | decimal 库 | `github.com/shopspring/decimal`（go.mod 已存在 v1.4.0 间接依赖，升为直接依赖） | `go.mod` 实测 |
| **D-11** | Webhook handler 路由 | `r.POST("/api/webhooks/safeheron", webhookHandler.Receive)` 直接挂，**不走任何 auth middleware**。验签由 handler 内部用 SDK 完成 | SPEC §6.4 |
| **D-12** | Webhook body 大小限制 | 用 `http.MaxBytesReader(w, r.Body, 1<<20)` 限制 1MB 防 DoS。Gin 等价：handler 入口 `c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)` | 防御性编码 |

#### T9 充值页面 UX 重构（D-13 ~ D-30）

| # | 议题 | 锁定决策 | 依据 |
|---|------|---------|------|
| **D-13** | UX 流程 | 三步流程「选币 → 选链 → 展示地址」，对齐币安/欧易。不做搜索、FAQ、多地址管理、钱包切换 | 用户 2026-05-12 提供币安截图对齐 |
| **D-14** | 视觉风格 | 保持当前浅色 Tailwind + shadcn/ui，无主题层面变更 | 与项目其它 dashboard 页一致 |
| **D-15** | 地址复用 | 同 `networkFamily` 下不同 coin 复用同一地址（Safeheron 设计：一个 EVM 钱包同时接所有 EVM coinKey） | SPEC §4.4 AddCoin 规则 |
| **D-16** | 地址分配时机 | 不变，保持 lazy assign per (user, networkFamily)；激活预分配作为独立 ticket 后续做 | SPEC §6.3 |
| **D-17** | 展示元数据存储 | DB 加列（不用代码 map 硬编码）：`chains.short_name`、`coin_chains.{token_standard, estimated_arrival_minutes}` | 用户确认；migration 022 |
| **D-18** | Migration 编号 | 原计划 `022_add_deposit_display_fields.go` 独立文件；T9 施工时按"phase1 单 migration 文件"约定**并入 015 内部 step**，不新增独立文件 | T1 D-9 幂等约束 + memory `feedback_migration_consolidation` |
| **D-19** | 新增端点 | `GET /api/wallet/deposit-coins`，按 coin 分组返回 networks；旧 `/supported-chains` 保留向后兼容不删 | SPEC §8.1 / §8.4 |
| **D-20** | 端点响应 `tokenContract` | 原生币序列化为 JSON `null`（用 `*string`，空字符串 → nil）；非原生币填合约地址字符串 | UI 区分原生/非原生显示合约链接 |
| **D-21** | 静态字段语义 | `estimated_arrival_minutes` 是 UI 展示静态值（如 ETH=2/BSC=1/TRON=1），不进入业务逻辑判断；新增链/币时在新 migration 补值 | UI 展示用，无 SLA 含义 |
| **D-22** | 步骤指示器实现 | 手写 diamond + 序号 tailwind 样式，不引入新 stepper 组件（3 步复用度低） | 避免引入新依赖 |
| **D-23** | 自动推进 | 选币后若该币只有 1 个 network → useEffect 自动选中并推进到步骤 ③；多 network 时停在步骤 ② 等用户点 | Binance 同行为 |
| **D-24** | 切币行为 | 切换币种 → 清空 `selectedNetwork`、地址区回到 placeholder；React Query 按 `networkFamily` 缓存复用 | UX 一致性 |
| **D-25** | 币种图标 | 复用现有 `src/components/ui/crypto-icon.tsx`（已含 BTC/ETH/USDT/USDC/SOL SVG）；BNB/TRX 走 default 彩色圆 + 首字母分支 | 不引入图标包 |
| **D-26** | RecentDeposits 数据源 | 调用现有 `GET /api/deposits?limit=5`；空态显示文案；不做分页 | 已有接口 |
| **D-27** | 浏览器链接构造 | 前端构造 `${explorerUrl}/token/${tokenContract}` 和 `${explorerUrl}/tx/${txHash}`；后端只暴露 `explorerUrl` | 前端拼接更灵活 |
| **D-28** | 文件布局 | 单文件 `src/pages/dashboard/Deposit.tsx`（< 400 行），内联 5 个子组件；不拆 5 个文件 | 简单页面避免过度拆分 |
| **D-29** | i18n 处理 | 整重写 `deposit.*` 子树；删除 `deposit.tabs.*` / `deposit.supportedCoins.*` / `deposit.comingSoon.*`；保留 `deposit.status.*` 和 `deposit.activate*`（其它位置仍用） | D-8 i18n 命名更新 |
| **D-30** | Deposit.test.tsx | 因组件结构变更，整文件重写（不试图保留旧用例形态）；12 个新用例覆盖三步流程 + 边界 | 旧测试基于 Tabs 已不适用 |

#### T11 充值流水线安全加固（D-41 ~ D-51，v1.6 安全审计补充，2026-05-13 编号确认）

| # | 议题 | 锁定决策 | 依据 |
|---|------|---------|------|
| **D-41** | 状态覆写保护 | `MarkDepositFailed` 和 `MarkDepositManualReview` 加前置条件 `WHERE status NOT IN ('CREDITED')`，并检查 `RowsAffected`。已 CREDITED 的 deposit 永远不可被回退。这是未来对账系统的基石。 | 安全审计 #1 HIGH |
| **D-42** | Webhook IP 白名单 | 环境变量 `SAFEHERON_WEBHOOK_ALLOWED_IPS`（逗号分隔），webhook handler 入口用 `c.ClientIP()` 校验。空值 = 不校验（本地开发兼容）。当前无配置表，跟现有 env var 架构一致。 | 安全审计 #2 HIGH |
| **D-43** | FindOrCreateAccountForUpdate 注释 | 方法名保留不改，加注释说明 `ON CONFLICT DO UPDATE` 等效 `FOR UPDATE` 锁语义（PostgreSQL 对 `DO UPDATE` 的行持有排他锁直到事务提交）。 | 安全审计 #3 命名误导 |
| **D-44** | 余额非负 CHECK 约束 | `ALTER TABLE account ADD CONSTRAINT ck_balance_non_negative CHECK (balance >= 0)` + `ck_frozen_non_negative CHECK (frozen_balance >= 0)`。数据库级兜底，任何 SQL 路径都绕不过去。**作为新 step `AddAccountBalanceConstraints` 追加到 `015_safeheron_phase1.go` 内部 step list 末尾**，不新增独立 migration 文件（phase1 单文件约定 + memory `feedback_migration_consolidation`）；本地 015 已 applied 需手动 ALTER。 | 安全审计 #4 MEDIUM + 用户决策 2026-05-13 |
| **D-45** | KYT 环境二次校验 | `SetKYTDeps` 方法内加 defensive check：如果 `kytEnabled=false` 且 `APP_ENV=production`，`panic`。与 `container.go:142` 构成双保险——container 层防启动遗漏，service 层防运行时注入错误。 | 安全审计 #5 MEDIUM |
| **D-46** | UpsertDeposit fallback 加 FOR UPDATE | `fetchDepositByTxKey`（当 status_rank 拦截 upsert 后的回退 SELECT）加 `FOR UPDATE`。解释：当一笔 deposit 已经存在、新来的 webhook 状态更低（如 CONFIRMING 在 COMPLETED 之后到达），upsert 被 rank 拦截后会走 fallback SELECT 读取现有记录。加 FOR UPDATE 确保后续基于这条记录的决策不被并发修改干扰。**不会导致死锁**——此处是在 T-alpha 事务内对 deposits 行加锁，且 T-alpha 很短（解析 + upsert/fallback + MoveToKYTPending），不持有其他表的锁。 | 安全审计 #6 MEDIUM |
| **D-47** | MoveToKYTPending 检查 RowsAffected | 返回 `(bool, error)` 或检查 `RowsAffected == 0` 时返回特定 error，让 T-alpha 调用方跳过后续 KYT API 调用。节省 KYT 配额（每次无效调用 ≈ 1 次/500次，累积可观）。 | 安全审计 #9 |
| **D-48** | LookupAddressOwner 加 network_family | 查询加 `WHERE network_family = $2`。参数从 deposit 的 chain → chains.network_family 映射。成本极低（WHERE 多一个已索引列），防止极端情况下跨 network_family 地址碰撞导致错配用户。 | 安全审计 #10 |
| **D-49** | 敏感日志 | **本轮不修改**，保持 txKey/金额 INFO 级别输出，方便排查问题。后续有 admin 后台时统一加日志脱敏中间件。 | 用户决策：当前先不动 |
| **D-50** | Journal serial_no 碰撞 | **本轮不修改**。当前单 worker 串行处理，纳秒级碰撞概率极低。后续多 worker 扩容时再加 UUID 或随机后缀。 | 用户决策：暂不处理 |
| **D-51** | `.env` 优先于 shell env | 在 `internal/config/config.go` 和所有 `cmd/*/main.go` 的入口（除 `db_check` / `wealth_test` 因硬编码连接串外）加 `godotenv.Overload(".env")`，仅当 `os.Getenv("APP_ENV") != "production"` 时执行。这样 `.env` 内容会覆盖 shell 中的同名变量，避免开发者 shell profile 里其他项目的同名 env（如 `DATABASE_URL=...trader_flow`）静默覆盖项目配置。生产环境（Vercel / Cloud Run）通过 `APP_ENV=production` 守护，保持原 env 注入逻辑不变。`godotenv v1.5.1` 已在 `go.mod`，无新增依赖。 | 用户反馈 2026-05-13；memory `local_env_setup_pitfalls` 锁定的反复出过的问题 |

#### T10 KYT 合规筛查（D-31 ~ D-40，v1.5 spec 新增）

| # | 议题 | 锁定决策 | 依据 |
|---|------|---------|------|
| **D-31** | DB 迁移落地位置 | **并入现有 `015_safeheron_phase1.go`**，不新增 016+ 文件。本地 monera_local 已经跑过 015，开发者手动 `ALTER TABLE deposits ADD COLUMN ...` 补齐字段（**不强制 IF NOT EXISTS**——本地 step 测试已通过，生产首次执行时 015 一次到位）。但 SQL 中**仍用 `ADD COLUMN IF NOT EXISTS`** 保持幂等（migrator 中途失败可重跑） | 用户决策 + memory `feedback_migration_consolidation` |
| **D-32** | 015 改造范围（仅 AML 部分） | (a) `deposits` 加 4 字段：`aml_screening_state VARCHAR(16)` / `aml_risk_level VARCHAR(8)` / `aml_evaluated_at TIMESTAMP` / `aml_list JSONB`；(b) 状态枚举 `ck_deposits_status` 加 `KYT_PENDING`（先 DROP CONSTRAINT 再 ADD）；(c) 部分索引 `idx_deposits_kyt_pending ON (updated_at) WHERE status='KYT_PENDING'`；(d) 不改 `safeheron_webhook_events` 表（AML_KYT_ALERT 复用现有 raw_payload + event_id 机制） | SPEC §4.6 / §6.5 |
| **D-33** | 单事务 → 两阶段 拆分边界 | `ProcessOne` 改为三事务结构：(T-α) 拉 webhook event + parse + 锁定，COMMIT；(T-β 不在事务内) 调 `KytReport` API；(T-γ) 写 KYT 结果 + 入账或 MANUAL_REVIEW + 标 DONE。中间态用 `webhook_events.process_status='KYT_QUERYING'` 标识（**仅内存态够用，不需新加 enum 值**——直接在 T-α 末尾 SELECT 锁的同时设置 `process_attempts++`，T-β 失败回滚为 PENDING） | 用户决策 Q2 方案 A |
| **D-34** | KYT API 失败兜底 | T-β 中 `KytReport` 返回 error（网络抖动、超时） → **不**标 DONE，事件回 PENDING（`process_attempts++`），下次 worker 拉取重试；超过 `KYT_ORPHAN_ALERT_MAX_RETRY=100` 次后 MANUAL_REVIEW(`KYT_API_FAILED`) | 与现有 webhook_events 重试机制对齐 |
| **D-35** | KYT_ENABLED=false 走原入账逻辑 | 当 `KYT_ENABLED=false`（仅 local/test 可设）：`processEvent` 内**跳过整个 KYT 分支**，COMPLETED+CONFIRMED 直接走原入账事务（单事务，复用现有逻辑）。生产环境启动校验阻止此分支被启用 | K-16；保证本地端到端不依赖真实 Safeheron AML 配置 |
| **D-36** | summarizeRiskLevel 函数位置 | 新建 `internal/wallet/deposit/kyt.go`，导出 `SummarizeRiskLevel(amlList []AmlReport) string`（返回值：`LOW`/`MEDIUM`/`HIGH`/`SEVERE`/`UNKNOWN`/`FAILED`/`SKIPPED`/`PENDING`）。**不另起 kyt 包**——KYT 决策语义紧绑充值入账，与 deposit 包共生 | KISS；防包间循环依赖 |
| **D-37** | Safeheron Client KytReport 方法签名 | 在 `internal/safeheron/iface.go`（接口）+ `client.go`（adapter）扩展：`KytReport(ctx context.Context, txKey string) (*KytReportResponse, error)`；项目内 mirror 类型 `KytReportResponse{TxKey, CustomerRefID, AmlScreeningTriggeredState, AmlList []AmlReport}` 和 `AmlReport{Provider, Timestamp, Status, RiskLevel, LastUpdateTime, Payload json.RawMessage}`（实际文件清单见 todo.md T10.2） | 与现有 adapter 风格一致（T2.3 D-1） |
| **D-38** | 超时扫描 goroutine 实现 | **复用** `internal/wallet/deposit/worker.go`：在 `Worker.Run` 主循环加一个独立 ticker `kytScanTicker := time.NewTicker(KYT_SCAN_INTERVAL)`（默认 1m）；新增 `Service.ScanKYTTimeouts(ctx)` 方法扫描 `status=KYT_PENDING AND updated_at < NOW() - INTERVAL '20 minutes'`，逐行调 `KytReport` 兜底。复用 `FOR UPDATE SKIP LOCKED` | Phase 1 KISS，避免起新 goroutine |
| **D-39** | AML_KYT_ALERT 在 deposit 创建前到达 | webhook handler 同步路径**不做特殊处理**，照常 INSERT `safeheron_webhook_events`；worker 异步处理时若 `eventType=AML_KYT_ALERT` 且 `SELECT * FROM deposits WHERE safeheron_tx_key=?` 返回空，则 ROLLBACK 事务（`process_status` 保留 PENDING，`process_attempts++`），下次轮询自然重试；超过 `KYT_ORPHAN_ALERT_MAX_RETRY=100` 次后强转 MANUAL_REVIEW(`KYT_ORPHAN_ALERT`) | SPEC §6.5.2 + K-13 |
| **D-40** | 前端 KYT_PENDING 文案 | `src/i18n/locales/en.json` / `zh.json` 加 `deposit.status.KYT_PENDING = "Under compliance review"` / 「合规审核中」；前端 `Recent deposits` Badge 颜色按 status 着色（KYT_PENDING 用现有"蓝色待处理"色，与 PENDING 同色）。**不**暴露 KYT 报告细节给用户 | K-15；防泄露合规策略 |

**剩下唯一真正的部署期未知数**：

- ✅ 已规划，不属于代码决策：
  - 生产出口 IP 列表（部署 ops 提供）→ 已在 §9.4 / §7 风险表中追踪
  - 生产 Safeheron team API Key（需在控制台生成）→ T12.5 上线 checklist
  - 飞书机器人 webhook URL（运营创建后给到）→ T12.5
  - **Safeheron Console KYT 配置**：AML 功能开启 + 风险等级映射 + Webhook 通知启用（K-2 用户已确认；T10.7 中再核对一次） → T12.5

这些**不是代码决策开放点**，是部署运维的输入参数。代码侧已经准备好读 env，部署时填即可。

---

## 5. 任务列表（详见 todo.md）

任务编号、依赖、验收摘要详见 `docs/plans/safeheron-phase1-todo.md`。本文档只汇总：

| Task | 名称 | 依赖 | 估时 |
|------|------|------|------|
| T1   | DB migration + Seed (chains/coins/coin_chains/address_pool/webhook_events/deposits 扩展/account UNIQUE) | — | 1d |
| T2   | Safeheron SDK adapter + 签名验签单测 | — | 0.5d |
| T3   | Registry 加载 + 后台刷新 + 单测 | T1 | 0.5d |
| T4   | `cmd/pool_init/main.go` 预生成脚本 + sandbox 实测 1 钱包 | T1, T2, T3 | 0.5d |
| T5   | pool/manager（分配）+ pool/replenisher（补水） | T1, T2, T3, T4 | 0.5d |
| T6   | `/api/wallet/deposit-address` + `/api/wallet/supported-chains` handler + Vercel 路由 + 老端点 410 | T5 | 0.5d |
| T7   | Webhook handler（同步） + worker（异步） + 告警 + Vercel 路由 | T1, T2, T3, T6 | 1.5d |
| T8   | 前端切换（wallet-service + Deposit 页面） | T6, T7 | 0.5d |
| T9   | **充值页面 UX 重构**（选币→选链→展示地址 + 后端 deposit-coins 端点 + DB 展示字段） | T6, T8 | 1d |
| T10  | **KYT 合规筛查**（015 补 AML 字段 + KYT_PENDING 状态 + ProcessOne 两阶段 + KytReport adapter + AML_KYT_ALERT webhook + 超时兜底扫描 + 前端文案 + 现有测试回归）（v1.5 spec 新增） | T7 | 1.5d |
| T11  | **充值流水线安全加固**（状态覆写保护 + Webhook IP 白名单 + 余额非负约束 + KYT 环境二次校验 + UpsertDeposit fallback 加锁 + MoveToKYTPending RowsAffected 检查 + LookupAddressOwner network_family 过滤 + FindOrCreateAccountForUpdate 注释澄清 + **.env 优先于 shell env (D-51)**）| T7, T10 | 0.5d |
| T12  | Sandbox 端到端（3 链 × 2-3 币 + KYT 真实告警路径 + 异常路径） + 灰度上线 | T1-T11 | 1d |

总估时 9.5 人日（T11 在 T10 完成后插入，T12 依赖 T11），含缓冲两周内可完成。

---

## 6. 验收基线（与 SPEC §11 对齐）

### 6.1 功能验收（每条必须可观察）

- [ ] **F-1** `SELECT * FROM chains` 返回 3 行；`SELECT * FROM coins` 返回 5 行；`SELECT count(*) FROM coin_chains WHERE deposit_enabled=true` 在 prod env 返回 8，在 local/test env 返回 3
- [ ] **F-2** 启动日志含 `Registry loaded: chains=3 coins=5 coin_chains=8 (or 3)`；60s 后看到第二次刷新日志
- [ ] **F-3** `cmd/pool_init/main.go` 跑完后 `SELECT count(*) FROM address_pool WHERE network_family='EVM' AND status='AVAILABLE'` 返回 100；TRON 同
- [ ] **F-4** Replenisher 启动后日志含 `pool replenish check: EVM=100 TRON=100`，10 分钟周期可见
- [ ] **F-5** 同一用户 curl `/api/wallet/deposit-address?network_family=EVM` 两次返回完全相同的 `address`
- [ ] **F-6** 10 个用户并发请求 EVM 地址，`SELECT address FROM address_pool WHERE assigned_user_id IS NOT NULL` 返回 10 个**不同**地址
- [ ] **F-7** Sandbox webhook 推送一次正常事件，DB 看到 `safeheron_webhook_events` 一行 + `deposits.status='CREDITED'` + `account.balance` 增加 + `journal` 一行 `biz_type=10`
- [ ] **F-8** 同一事件 (txKey, status) 重推 6 次，`SELECT count(*) FROM deposits WHERE safeheron_tx_key=?` 仍为 1，`account.balance` 不重复增加
- [ ] **F-9** 构造测试事件：先发 COMPLETED 再发 CONFIRMING，最终 `deposits.status_rank=100`，**不**回退到 50
- [ ] **F-10** Webhook 验签失败 → handler 返回 401，`SELECT count(*) FROM safeheron_webhook_events` 不增加
- [ ] **F-11** 地址无主事件 → `deposits.status='MANUAL_REVIEW'` + `failed_reason='ADDRESS_UNASSIGNED'` + 飞书消息可见
- [ ] **F-12** Sandbox 端到端 3 链各成功 1 笔：Sepolia ETH / Sepolia USDC / Shasta TRX
- [ ] **F-13** 前端 Dashboard 点击 Deposit 看到 EVM 地址 + 6 个币种列表（生产）/ 2 个币种（testnet）

**T10 KYT 合规筛查追加项**（v1.5 spec 新增，对齐 SPEC §11.1 F-KYT-1 ~ F-KYT-15）：

- [ ] **F-KYT-1** `KYT_ENABLED=true` 下，构造 webhook COMPLETED+CONFIRMED → 调一次 `/v1/compliance/kyt/report`；`deposits.aml_screening_state` / `aml_list` / `aml_evaluated_at` 全部写入
- [ ] **F-KYT-2** Mock KYT API 返回 `UNTRIGGERED` → `deposits.status='MANUAL_REVIEW'` + `failed_reason='KYT_UNTRIGGERED'` + WARN 飞书消息，`account.balance` **不变**
- [ ] **F-KYT-3** Mock 返回 `TRIGGERED+LOW`（amlList 内 MistTrack status=COMPLETED, riskLevel=LOW）→ `deposits.status='CREDITED'` + `account.balance` 增加
- [ ] **F-KYT-4** Mock 返回 `TRIGGERED+HIGH` → MANUAL_REVIEW(`KYT_RISK_HIGH`) + **ERROR 告警**
- [ ] **F-KYT-5** Mock 返回 `TRIGGERED+SEVERE` → MANUAL_REVIEW(`KYT_RISK_SEVERE`) + **ERROR 告警**
- [ ] **F-KYT-6** Mock 返回 `TRIGGERED+MEDIUM` / `UNKNOWN` / `FAILED` / `SKIPPED` 各路径 → MANUAL_REVIEW(`KYT_RISK_*` / `KYT_PROVIDER_FAILED` / `KYT_SKIPPED`) + WARN 告警
- [ ] **F-KYT-7** Mock 返回 `IN_PROGRESS` → `deposits.status='KYT_PENDING'`；前端「Recent deposits」对应行显示「Under compliance review」
- [ ] **F-KYT-8** `KYT_PENDING` 后构造 `AML_KYT_ALERT` webhook（payload 含 amlList riskLevel=LOW）→ deposit 推进到 CREDITED
- [ ] **F-KYT-9** `AML_KYT_ALERT` 在 `TRANSACTION_STATUS_CHANGED` 之前到达 → 第一次 worker 处理时 ROLLBACK + process_attempts++；下次轮询 deposits 已创建后正确关联
- [ ] **F-KYT-10** `KYT_PENDING` 超过 20 分钟未推进 → 超时扫描调一次 KYT Report API；Mock 返回 LOW → CREDITED；Mock 仍 IN_PROGRESS → MANUAL_REVIEW(`KYT_TIMEOUT_STILL_PENDING`) + **ERROR 告警**
- [ ] **F-KYT-11** `APP_ENV=production && KYT_ENABLED=false` 启动 → 进程 panic（启动校验）
- [ ] **F-KYT-12** `APP_ENV=local && KYT_ENABLED=false` → COMPLETED+CONFIRMED 直接 CREDITED 不走 KYT 分支；无 `KytReport` API 调用日志
- [ ] **F-KYT-13** 现有 T7 入账测试用例**全部不退化**：在 KYT_ENABLED=false 或 mock 返回 LOW 的前提下，所有原 T7 测试通过（关键回归保证）
- [ ] **F-KYT-14** `aml_list` 字段写入后再次收到 AML_KYT_ALERT → 字段被新数据**覆盖**（不是 append），但 `MANUAL_REVIEW` 终态不会改回 CREDITED
- [ ] **F-KYT-15** `summarizeRiskLevel` 单测覆盖 8 个分支：单 provider × 5 个 riskLevel + status=PENDING/FAILED/SKIPPED

**T11 安全加固追加项**（v1.6 安全审计，对齐 D-41 ~ D-48 + D-51）：

- [ ] **F-HARDEN-1** 构造 `status='CREDITED'` 的 deposit → MarkDepositFailed 返回 error，状态不变
- [ ] **F-HARDEN-2** 构造 `status='CREDITED'` 的 deposit → MarkDepositManualReview 返回 error，状态不变
- [ ] **F-HARDEN-3** `SAFEHERON_WEBHOOK_ALLOWED_IPS=1.2.3.4` 时非白名单 IP → webhook 403
- [ ] **F-HARDEN-4** `SAFEHERON_WEBHOOK_ALLOWED_IPS` 为空 → 任何 IP 正常进入验签
- [ ] **F-HARDEN-5** `UPDATE account SET balance = -1` → CHECK violation
- [ ] **F-HARDEN-6** `APP_ENV=production + KYT_ENABLED=false + SetKYTDeps` → panic
- [ ] **F-HARDEN-7** deposit 已为 KYT_PENDING → MoveToKYTPending 返回 error，不触发 KYT API
- [ ] **F-HARDEN-8** LookupAddressOwner 传入 networkFamily → 精确匹配地址池
- [ ] **F-HARDEN-9** Shell 环境设 `DATABASE_URL=postgres://wrong-host/wrong-db`，`.env` 设 `DATABASE_URL=postgres://localhost/monera_local` → 启动后实际连接 monera_local（.env 赢）
- [ ] **F-HARDEN-10** `APP_ENV=production` + 工作目录有恶意 `.env` 文件 → 启动**不**加载 `.env`，仍使用容器/平台注入的真实 env（生产保护）
- [ ] **F-HARDEN-11** 所有 cmd 工具（migrate / pool_init / scheduler / check-2fa / check-wallet / migrate-drop / simulation_test）单独运行时，shell 中已有错误 `DATABASE_URL` 也能被 .env 覆盖 → 实际操作的是项目 DB

### 6.2 非功能验收

- [ ] **NF-1** Webhook handler 同步 P99 < 2s（用 `wrk` 或日志直方图验证）
- [ ] **NF-2** 异步入账延迟 P99 < 30s（webhook 落库时间 → CREDITED 时间）
- [ ] **NF-3** 失败告警 5 分钟内飞书可见
- [ ] **NF-4** `go test ./internal/safeheron/... ./internal/wallet/... -cover` 显示 ≥ 80%
- [ ] **NF-5** `go vet ./...` 无 warning
- [ ] **NF-6** `npm run build` 通过；`npm run test` 通过
- [ ] **NF-7** `.env.example` 含全部新增变量；私钥占位符正确（无真实值）
- [ ] **NF-8** Git diff 检查：无 `.env` / 真实私钥 / API key 入仓

### 6.3 安全验收

- [ ] **S-1** `grep -r "PRIVATE_KEY\|API_KEY" --include="*.go"` 只命中 env 读取代码，**无**硬编码值
- [ ] **S-2** 日志样本检查（启动 5 分钟）：不含 `sig=` / `key=` / `bizContent=` 等敏感字段
- [ ] **S-3** Webhook handler 路由在 Gin 中**未挂** JWT middleware（验签独立）
- [ ] **S-4** 老 `/api/wallet/create` 等端点返回 410 Gone + 提示新端点

---

## 7. 风险登记 + 回滚方案

| 风险 | 触发条件 | 回滚方案 |
|------|---------|---------|
| Migration 执行失败 | 015 任一 step 出错 | 015 内部 step 列表逐项 Down + 修复出错 step + 重跑 015 |
| 地址池预生成中途失败（如 Safeheron 限流） | 第 N 个钱包创建失败 | 已生成的进 DB（部分成功）；失败的进 `ERROR` 状态；`cmd/pool_init/main.go` 增加 `--retry-errors` 参数 |
| Webhook ack 格式错 | 部署后立即出现 30s/1m 重试风暴 | 立刻热修复 ack body 字面量；用 sandbox 复测后再放行 |
| 老 `internal/coreapi/` 代码影响测试编译 | `go test` 报红 | 保留代码不删，只在 service 层停止调用（SPEC §3.2 已规定） |
| 切换前端后用户看不到老地址 | Phase 1 已确认无真实用户，业务 OK | 老 `user_wallets` 表不动，二期评估展示策略 |
| 生产 Safeheron 出口 IP 不固定 | 部署后 SDK 调用 401 | 部署前 ops 必须确认 IP；预留 2 个 IP 加白名单（SPEC §9.2） |
| **KYT** 改造影响 T7 入账主路径 | T10 拆 ProcessOne 两阶段后，原有 T7 测试可能挂 | 强制要求 T10.7 跑 `go test ./internal/wallet/deposit/... -race` 全绿；原测试加 `KYT_ENABLED=false` 或 mock KYT 返回 LOW 确保不退化（F-KYT-13）|
| **MistTrack 单点不可用** | 所有充值进 MANUAL_REVIEW，运营负担激增 | T10 上线初期监控 `failed_reason LIKE 'KYT_%'` 占比；超过 30% 启动应急人工放行流程（DB 直改） |
| **KYT API 调用配额不够** | 大流量时被 Safeheron 限流 | 初查 + 兜底共 ≈1 次/笔，已是最小化；监控调用次数，必要时与 Safeheron 申请提额 |
| **AML_KYT_ALERT webhook 永不到达**（Console 配置错） | KYT_PENDING 持续堆积 | T10.7 sandbox 实测验证 Console 配置；监控 `status=KYT_PENDING` 最长停留时间，>30min 告警 |
| **本地数据库 015 已经跑过**，加 AML 字段需要手动 ALTER | 开发者本地不一致 | D-31 已锁定：本地手动 ALTER（用户授权）；CI 部署生产时 015 一次性到位 |
| **T11 状态覆写保护破坏现有 T7/T10 测试** | 加 `WHERE status NOT IN ('CREDITED')` 后某些原测试用例（如尚未 CREDITED 的 deposit 走 FAILED 路径）依然过，但 mock 数据库可能不符合 | T11.1 DoD 强制要求 `service_test.go` / `service_kyt_test.go` 全绿；遇到挂掉的测试**修测试**（用真实状态而非硬塞数据），**不**修代码绕过保护 |
| **T11 IP 白名单导致本地开发收不到 webhook** | 本地 dev 配了 `SAFEHERON_WEBHOOK_ALLOWED_IPS` 但忘了加本地隧道出口 IP | env 留空 = 不校验（D-42 锁定，本地兼容）；本地开发请勿设置该 env，仅生产环境填入 |
| **T11 余额 CHECK 约束阻塞历史负余额数据** | 生产 account 表如有遗留 `balance < 0` 行，015 内 `AddAccountBalanceConstraints` step 会失败，整个 015 ROLLBACK | T11.4 部署前先 `SELECT count(*) FROM account WHERE balance < 0 OR frozen_balance < 0` 确认为 0；非 0 时先人工修复再跑 015 |
| **D-51 godotenv.Overload 误读生产 `.env`** | 生产打包意外把 `.env` 文件带进容器，覆盖 Vercel 注入的真实 env | `Overload` 用 `os.Getenv("APP_ENV") != "production"` 守护，生产环境永不读 `.env`；额外要求 `.dockerignore` / `.vercelignore` 排除 `.env` |
| **D-51 cmd 工具漏改导致部分场景仍连错库** | 改了 config.go 没改 cmd/migrate，本地跑 migrate 仍受 shell env 影响 | T11.9 DoD 强制要求 8 个 cmd 文件**全部**加 `godotenv.Overload`，验证命令逐个 cmd 启动跑一遍 |

---

## 8. 阶段交付物清单

完工时 PR 描述里必须列出（用于 release notes）：

- 新增 Go 包：`internal/safeheron/`, `internal/wallet/config/`, `internal/wallet/pool/`, `internal/wallet/deposit/`
- 新增 handler：`safeheron_webhook_handler.go`
- 改造 handler：`wallet_handler.go`（新增 2 端点，老端点改 410）
- 改造 service：`wallet.go`（停止调用 `coreapi`）
- 新增 cmd：`cmd/pool_init/main.go`
- Migration：**只有 1 个文件 `015_safeheron_phase1.go`**，内部 step 列表汇总：
  - `CreateChainsTable` / `CreateCoinsTable` / `CreateCoinChainsTable` / `CreateAddressPoolTable` / `CreateSafeheronWebhookEventsTable` / `ExtendDepositsForSafeheron` / `SeedSafeheronPhase1Data`（T1 范围）
  - T9 充值展示字段（short_name / token_standard / estimated_arrival_minutes）已合并进对应 step（`CreateChainsTable` / `CreateCoinChainsTable`）
  - T10 KYT AML 字段（v1.5 决策）已合并进 `ExtendDepositsForSafeheron` step
  - **T11 余额非负 CHECK 约束（v1.6 新增）以新 step `AddAccountBalanceConstraints` 形式追加到 step list 末尾**
- 新增前端调用：`src/lib/wallet-service.ts` 新方法
- Vercel `api/[...route].ts` ROUTE_CONFIG 新增 3 行
- 环境变量：见 §3.10 清单（含 KYT 4 项 + T11 IP 白名单 1 项）

**T10 KYT 追加交付物**：
- 改造 migration：`015_safeheron_phase1.go`（追加 AML 4 字段 + `idx_deposits_kyt_pending` + `KYT_PENDING` 状态，不新增文件）
- 新增 Go 文件：`internal/wallet/deposit/kyt.go`（`SummarizeRiskLevel` + KYT 处置矩阵实现）
- 改造 Go 文件：
  - `internal/safeheron/iface.go` / `client.go`（接口加 `KytReport` 方法 + adapter 实现）
  - `internal/safeheron/types.go`（mirror `KytReportResponse` / `AmlReport` 类型）
  - `internal/wallet/deposit/service.go`（`ProcessOne` 拆三事务结构）
  - `internal/wallet/deposit/worker.go`（加 `kytScanTicker` 超时扫描分支）
  - `internal/wallet/deposit/repository.go`（加 `LockKYTPendingForRetry` / `UpdateAMLFields` 等方法）
  - `internal/wallet/deposit/models.go`（加 `StatusKYTPending` 常量）
  - `internal/container/container.go`（KYT_ENABLED 启动校验 + 注入 KytReport func）
- 改造前端：
  - `src/i18n/locales/en.json` / `zh.json`（加 `deposit.status.KYT_PENDING`）
  - `src/pages/dashboard/Deposit.tsx`（RecentDeposits Badge 着色映射加 KYT_PENDING）
- 新增测试：
  - `internal/wallet/deposit/kyt_test.go`（`SummarizeRiskLevel` 单测覆盖 8 分支）
  - `internal/wallet/deposit/service_kyt_test.go`（KYT 状态机端到端，覆盖 F-KYT-1 ~ F-KYT-14）
  - `internal/safeheron/client_kyt_test.go`（adapter KytReport 单测，mock SDK response）
  - 原 `service_test.go` 回归补丁（加 `KYT_ENABLED=false` 或 mock LOW，F-KYT-13）

**T11 安全加固追加交付物**（v1.6 审计补充）：
- 改造 migration：`internal/migration/migrations/015_safeheron_phase1.go`
  - 新增 step struct `AddAccountBalanceConstraints`（Up: 给 account 表加 `ck_balance_non_negative` + `ck_frozen_non_negative` CHECK 约束；Down: DROP CONSTRAINT IF EXISTS）
  - 在 `SafeheronPhase1.Up()` 的 step list 末尾追加 `{"AddAccountBalanceConstraints", (&AddAccountBalanceConstraints{}).Up}`
  - 在 `SafeheronPhase1.Down()` 的 step list 开头追加对应反向 step
  - **不新增 migration 文件**，不修改 `cmd/migrate/main.go`（约定见 [[feedback_migration_consolidation]]）
- 本地数据库（015 已 applied）需手动执行：`psql monera_local -c "ALTER TABLE account ADD CONSTRAINT ck_balance_non_negative CHECK (balance >= 0); ALTER TABLE account ADD CONSTRAINT ck_frozen_non_negative CHECK (frozen_balance >= 0);"`
- 改造 Go 文件：
  - `internal/wallet/deposit/repository.go`
    - `MarkDepositFailed` 加 `WHERE status NOT IN ('CREDITED')` + `RowsAffected` 检查 + 返回 `ErrDepositAlreadyCredited`
    - `MarkDepositManualReview` 加 `WHERE status NOT IN ('CREDITED', 'FAILED')` + `RowsAffected` 检查
    - `MoveToKYTPending` 加 `RowsAffected` 检查 + 返回 `ErrDepositNotPending`
    - `LookupAddressOwner` 签名加 `networkFamily string` 参数 + WHERE 加 `AND network_family = $2`
    - `fetchDepositByTxKey` 加 `FOR UPDATE`
    - `FindOrCreateAccountForUpdate` 加注释澄清锁语义
    - 新增 sentinel error 定义：`ErrDepositAlreadyCredited` / `ErrDepositNotPending`
  - `internal/wallet/deposit/service.go`
    - `SetKYTDeps` 加 `APP_ENV=production + !enabled` panic 校验
    - T-alpha 捕获 `ErrDepositNotPending` → 跳过 KYT API 调用
    - 调用 `MarkDeposit{Failed,ManualReview}` 处捕获 `ErrDepositAlreadyCredited` → WARN 日志 + 标 DONE 不阻塞
    - 调用 `LookupAddressOwner` 处传入正确的 networkFamily
  - `internal/handlers/safeheron_webhook_handler.go`
    - 构造函数接受 `allowedIPs []string`
    - `Receive` 入口加 `c.ClientIP()` 校验
  - `internal/container/container.go`
    - 读取 `SAFEHERON_WEBHOOK_ALLOWED_IPS` env 并注入 handler
  - **`internal/config/config.go`（D-51 .env 优先）**
    - `Load()` 入口加 `if os.Getenv("APP_ENV") != "production" { godotenv.Overload(".env") }`
    - import `github.com/joho/godotenv`
  - **所有 cmd 工具入口加 `godotenv.Overload(".env")`（D-51，除 `db_check` / `wealth_test` 硬编码连接串外）**：
    - `cmd/migrate/main.go`
    - `cmd/migrate-drop/main.go`
    - `cmd/pool_init/main.go`
    - `cmd/scheduler/main.go`
    - `cmd/scheduler/run_once.go`
    - `cmd/check-2fa/main.go`
    - `cmd/check-wallet/main.go`
    - `cmd/simulation_test/main.go`
- 改造 env：
  - `.env.example` 追加 `SAFEHERON_WEBHOOK_ALLOWED_IPS=`
- 新增测试：
  - `internal/wallet/deposit/repository_security_test.go`（覆盖 F-HARDEN-1/2/5/7/8）
  - `internal/wallet/deposit/service_security_test.go`（覆盖 F-HARDEN-6）
  - `internal/handlers/safeheron_webhook_handler_security_test.go`（覆盖 F-HARDEN-3/4）
  - `internal/config/config_test.go`（覆盖 F-HARDEN-9：.env 优先于 shell env，APP_ENV=production 时跳过 Overload）
  - `internal/migration/migrations/safeheron_migrations_test.go` 追加 `TestAddAccountBalanceConstraints`（验证 CHECK 约束已加，与现有 015 step 测试同文件）
  - 原 `service_test.go` / `service_kyt_test.go` 回归（确认 T11 改造未破坏 T7/T10 已通过用例）
