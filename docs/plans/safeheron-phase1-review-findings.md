# Safeheron Phase 1 Code Review 发现记录

> Status: **第二轮 review 修复完成** — 第一轮 23 项 + 第二轮 8 项（R2-C-1 + R2-I-1..4 + R2-S-2/3/4）。R2-S-1 评估保留（reflect 已被测试覆盖，长期可改具体类型）。
> Last updated: 2026-05-12
> 对应 PR / commits: `acd0652` → `8f34499`（T1–T8）+ 未 commit 修复（工作树）
> 对照 plan: `docs/plans/safeheron-phase1-plan.md` §4（12 条 D-X）+ §3.6 + §6 验收基线

## 修复完成快照（2026-05-12）

### ✅ 第一轮：24 项

**第一波 plan 锁定违背（6/6）**：T6-I-2 / T7-I-1 / T7-I-3 / T8-I-1 / T8-I-2 / T8-I-4
**第二波 安全 & 健壮性（6/6）**：T6-I-3 / T6-I-1 / T7-I-4 / T7-I-5 / T7-I-6 / T8-I-3
**第三波 Suggestion（12/12）**：T6-S-1 / T6-S-2 / T6-S-3 / T6-S-4 / T7-S-1 / T7-S-2 / T7-S-3 / T7-S-4 / T7-S-5 / T7-S-6 / T8-S-1 / T8-S-2

### ✅ 第二轮 review 后修复：7 项 + 1 项保留评估

**Critical（1）**：R2-C-1 — 前后端 query 命名对齐，后端改 `c.Query("networkFamily")`
**Important（4）**：R2-I-1（`safeheron_coin_key` 写入恢复，含 T7-I-2 历史遗漏） / R2-I-2（alert 邮件 HTML XSS 防护抽到 `alertrender` 子包） / R2-I-3（删除 `RESEND_API_KEY` 明文 stdout 日志） / R2-I-4（deposit_handler 路由 wiring 测试改用 gin engine ServeHTTP）
**Suggestion（3 修 + 1 保留）**：R2-S-2 文档计数对齐 / R2-S-3 vitest mock 改 URL parse / R2-S-4 提示拆 commit / **R2-S-1 保留**（reflect 检测已有测试覆盖，长期建议改具体类型，但本轮不改以避免重构面扩大）

### ⏸️ 已知遗留（不在本批）

- **T8-S-3** — 错误码 i18n 映射表（依赖 T6-I-3 + T8-I-3 已完成，但前端映射表实施留下个 release）
- **T8-S-4** — fetch wrapper 处理 401（设计变更，下个 release）
- **T8-S-6** — `deposit.comingSoon.*` i18n 残留（Phase 2 cleanup）

### Phase B Safeheron 模块覆盖率（80% 目标全部达成）

Go：
- `internal/wallet/deposit`: **84.1%**
- `internal/wallet/config`: **98.0%**
- `internal/alert`: **90.5%**
- `internal/safeheron`: **88.7%**
- `internal/container`: 9.3%（wiring 代码，主要是生产依赖实例化路径，非 Safeheron 引入的历史现状）

前端：
- `src/lib/wallet-service.ts`: **89.47% stmts / 94.11% lines**
- `src/pages/dashboard/Deposit.tsx`: **85.71% stmts / 92.1% lines**

### Phase C 验收

- `go test -race ./internal/wallet/... ./internal/safeheron/... ./internal/alert/... ./internal/container/...` — **全 ok**
- `go test -race ./internal/handlers/` Safeheron 相关测试（`TestWebhook*` / `TestHandleDepositWebhook` / `TestGetDepositAddress` / `TestGetSupportedChains` / `TestSetSafeheronDeps`）— **全 ok**
- `go vet ./internal/wallet/... ./internal/safeheron/... ./internal/handlers/ ./internal/alert/... ./internal/container/...` — **clean**
- 前端 `npx vitest run src/lib/wallet-service.test.ts src/pages/dashboard/Deposit.test.tsx` — **12/12 pass**
- `npm run build` — **3083 modules transformed, dist/ 产出正常**

**注**：`go test ./...` 全跑会看到一些 FAIL — 那些是 dev 分支预存在的历史 bug（TwoFA / Wealth / scheduler / wallet.go:435 panic 等），与本次 Safeheron review 无关。详见 memory `monera-test-baseline-failures`。

### 工作树状态（待 commit）

修改：
- 新增：`docs/plans/safeheron-phase1-review-findings.md`、`internal/handlers/setsafeheron_deps_test.go`、`internal/handlers/deposit_handler_test.go`
- 改：`internal/{container,handlers,wallet/deposit,wallet/config,alert,services/email_service}/*`、`src/{App.tsx,pages/dashboard/Deposit.{tsx,test.tsx},i18n/locales/{en,zh}.json,lib/wallet-service.{ts,test.ts}}`、`api/[...route].ts`、`docs/plans/safeheron-phase1-plan.md`
- 删：`src/pages/dashboard/AccountOpening.{tsx,test.tsx}`

---


---

## 严重度约定

| 级别 | 含义 | 处理 |
|---|---|---|
| 🔴 Critical | 安全漏洞 / 数据丢失 / 阻塞上线 | 上线前必修 |
| 🟠 Important | Bug 或显著质量问题 | 灰度前应修 |
| 🟡 Suggestion | 可维护性 / 风格 | 可选 |

---

## T6 — commit `31ef64b`（用户 API + Vercel 路由）

### 决策对齐
- §3.6 用户端 API 路径 / Webhook 路径 / ROUTE_CONFIG 新增 3 行 / 老端点 410 / camelCase / 金额字符串 — 全部 ✅
- §6 S-3 webhook 不挂 JWT / S-4 老 POST 端点 410 — ✅
- §6 F-5 同用户同址 / F-6 10 用户并发 10 不同址 — 测试覆盖 ✅
- D-11 webhook 路由不挂 auth — ✅
- D-12 webhook 1MB body 限制 — ⏸️ T7 路径核

**注**：`GET /api/wallet/info` 保留未改 410，commit message 明示"Core API 流程仍在用"，与 SPEC §3.2 "老地址展示由二期处理" 兼容，**不算偏离**。

---

### 🟠 I-1. `SetSafeheronDeps` 对 typed-nil 接口失防御

**文件**：`internal/handlers/handlers.go:71-78`

```go
func (h *Handler) SetSafeheronDeps(pm DepositPoolManager, reg ChainsRegistry) {
    if pm != nil { h.PoolManager = pm }      // ← typed-nil 仍 != nil
    if reg != nil { h.WalletRegistry = reg }
}
```

如果调用方传入 `*pool.Manager(nil)` 包装为接口，`pm != nil` 仍 true，赋值后 handler `if h.PoolManager == nil` 也通不过 → `GetDepositAddress` 内 `m.repo.GetUserAddress` 走 nil pointer panic。

当前 `routes.go:41-43` 用具体类型事前检查兜住，但**这条防御是隐式契约**，注释只说了"避免接口持有 nil 指针绕过 503-fallback"，没强制写"调用方必须先 typed-nil 检查"。

**修复建议**（二选一）：
1. `SetSafeheronDeps` 收具体类型 `*pool.Manager` + `*walletconfig.Registry`，handler 字段仍接口（仅 set 时做类型 nil 比较）。
2. 在 `SetSafeheronDeps` 内用 `reflect.ValueOf(pm).IsNil()` 双重把关。

---

### 🟠 I-2. `Container.Close()` 未清理 Safeheron 临时 PEM 文件，**注释与实现不一致**

**文件**：`internal/container/container.go:59`（注释）vs `:322-330`（实现）

注释：
> The SDK client's temp PEM files are cleaned up via Container.Close().

实际 `Close()`：
```go
func (c *Container) Close() error {
    if c.TokenBlacklist != nil { c.TokenBlacklist.Close() }
    if c.DB != nil { return c.DB.Close() }
    return nil
}
```

没有调用 `c.SafeheronClient` 的任何清理。Plan **D-3 明确要求"退出时 defer 删除"PEM 文件** → 注释撒谎，实现违背 plan 锁定。

后果：服务热重启时 `/tmp/safeheron-*.pem` 残留磁盘（0600 私钥）。

**修复建议**：
1. `safeheron.Client` 暴露 `Close() error` 删除 3 个临时 PEM。
2. `Container.Close()` 顺序清理 `SafeheronClient.Close()` + `DB.Close()`（注意当前 `return c.DB.Close()` 提前 return 也会跳过后续资源，需要重构）。
3. 本属 T2 责任，因 T6 添加错误注释归到 T6。

---

### 🟠 I-3. `GetDepositAddress` 把 service 内部错误细节泄露给前端

**文件**：`internal/handlers/deposit_address_handler.go:100-104`

```go
c.JSON(http.StatusInternalServerError, gin.H{
    "error":   "ASSIGN_FAILED",
    "message": err.Error(),  // 可能含 "pq: connection refused" / "registry load: ..."
})
```

`pool.Manager.GetOrAssign` 把 DB 错误 `fmt.Errorf("get user address: %w", err)` 一路上抛，handler 直接拼回 message。违反 `common/security.md`「Error messages don't leak sensitive data」。

**修复建议**：
```go
logger.Error("assign deposit address", "userID", userID, "family", family, "err", err)
c.JSON(http.StatusInternalServerError, gin.H{
    "error":   "ASSIGN_FAILED",
    "message": "Failed to assign deposit address, please try again",
})
```
并加 test 断言 body 不含 `pq:` 等内部串。

---

### 🟡 S-1. `Registry.AllChains()` 不过滤 `Chain.Enabled`

**文件**：`internal/wallet/config/registry.go:185-193` + `models.go:11`

`Chain` 有 `Enabled bool`，`AllChains()` 全量返回不过滤。Phase 1 数据 3 条全 enabled 不出问题，但 API 语义 `supported-chains` = 用户可用，应过滤。

**建议**：新增 `ListEnabledChains()` 或 `AllChains` 改名为 `ListAllChainsIncludingDisabled`。

---

### 🟡 S-2. `[...route].ts` 留 pre-existing `console.log` debug 行

**文件**：`api/[...route].ts:190-198`

```typescript
if (path === '/api/wallet/create') {
  console.log(`[DEBUG-ACCOUNT-OPENING] ...`);
}
```

pre-existing，但 `/api/wallet/create` 现恒返 410，DEBUG 已无意义且违 `web/coding-style.md`「No console.log in production」。

---

### 🟡 S-3. `GetSupportedChains` 测试缺 empty chains 边界用例

**文件**：`internal/handlers/deposit_address_handler_test.go`

补一个 `chains=[]` 用例验证 200 + `{chains:[]}`。

---

### 🟡 S-4. `Handler.PoolManager` / `Handler.WalletRegistry` 是导出字段

**文件**：`internal/handlers/handlers.go:48-49`

公开字段允许任何人绕过 `SetSafeheronDeps` 防御直接写 nil。建议改非导出 + 仅 setter 写入。改动面稍大，作为 nit。

---

## T7 — commit `02b3a4f`（Webhook + worker）

### 决策对齐

| 决策 | 期望 | T7 实现 | 状态 |
|---|---|---|---|
| §3.5 ack body 字面量 | `{"code":"200","message":"SUCCESS"}` + HTTP 200 + Content-Type json | `safeheron_webhook_handler.go:19,117-118` 字面常量 + 显式 header | ✅ |
| §3.5 验签交 SDK | 不自拼签名串 | handler 调 `Verifier.WebhookConvert(body)` → SDK 实现 | ✅ |
| §3.5 同步路径 | 验签 → INSERT ON CONFLICT DO NOTHING → ack | handler 流程顺序对齐；`repository.go:91-107` `INSERT ... ON CONFLICT (event_id) DO NOTHING` | ✅ |
| §3.5 幂等键 | `txKey + ':' + transactionStatus` UNIQUE | `safeheron_webhook_handler.go:83` 拼接；UNIQUE 约束在 migration 019 | ✅ |
| §3.5 eventType 过滤 | 只处理 `TRANSACTION_CREATED` / `TRANSACTION_STATUS_CHANGED` | `service.go:46-49` allowedTypes 包含两者 | ✅ |
| §3.5 入账唯一条件 | `COMPLETED + CONFIRMED + INFLOW` | `service.go:139, 205-207` | ✅ |
| §3.5 UPSERT 守卫 | `WHERE deposits.status_rank <= EXCLUDED.status_rank` | `repository.go:160` | ✅ |
| §3.5 单事务 | webhook_events + deposits + account + journal 同 tx | `service.go:64-110` 一个 tx 全包 | ✅ |
| §3.5 Worker 1s polling | `FOR UPDATE SKIP LOCKED LIMIT 1` | `worker.go:41` ticker 1s; `repository.go:118` SKIP LOCKED | ✅ |
| §3.5 失败终态 | `status=FAILED + failed_reason=transactionSubStatus + 告警` | `service.go:235-252` | ✅ |
| §3.1 status_rank 取值 | SUBMITTED=10, SIGNING=20, BROADCASTING=30, CONFIRMING=50, FAIL=90, COMPLETED=100 | `models.go:81-98` BROADCASTING=**20** + 缺 SIGNING + 多 CREATED=5 | ❌ **偏离** |
| D-12 webhook 1MB body 限制 | `http.MaxBytesReader` | `safeheron_webhook_handler.go:65` 用 `io.LimitReader`（静默截断） | ❌ **偏离** |
| F-7 sandbox 正常入账 | webhook → CREDITED + balance + journal | 代码路径覆盖；sandbox 实测留 T9 | ✅（待 T9 验） |
| F-8 重发不重复入账 | event_id UNIQUE + status_rank guard | InsertEventOrSkip 幂等 + UPSERT guard ✓ | ✅ |
| F-9 COMPLETED 后 CONFIRMING 不回退 | status_rank guard | guard + `service.go:178-181` ErrNoRows 回退取现状 | ✅ |
| F-10 验签失败 → 401 + 不入库 | handler 401 早返 | `safeheron_webhook_handler.go:78-81` | ✅ |
| F-11 地址无主 → MANUAL_REVIEW + 告警 | LookupAddressOwner not found → flagManualReview | `service.go:149-151` | ✅ |

---

### 🟠 T7-I-1. `status_rank` 数值与 plan §3.1 锁定值偏离

**文件**：`internal/wallet/deposit/models.go:81-98`

```go
case "CREATED":      return 5    // ← plan 未定义
case "SUBMITTED":    return 10
                                  // ← plan 写 SIGNING=20，代码缺
case "BROADCASTING": return 20    // ← plan 写 30
case "CONFIRMING":   return 50
case "FAILED", "CANCELLED", "REJECTED": return 90
case "COMPLETED":    return 100
```

plan §3.1 锁定：`SUBMITTED=10, SIGNING=20, BROADCASTING=30, CONFIRMING=50, FAILED/CANCELLED/REJECTED=90, COMPLETED=100`

**影响**：
- 单调性还是对的（5 < 10 < 20 < 50 < 100），F-9 验收通过。
- **SIGNING 缺失** 会让 Safeheron 推送的 SIGNING 事件 status_rank 返回 0 → UPSERT guard `0 <= EXCLUDED` 几乎永远成立 → SIGNING 反而能"更新"较新的状态 row → 数据脏。
- BROADCASTING=20 占用了 plan 锁定给 SIGNING 的位次，未来运维/对账如果按 plan 数值断言 BROADCASTING=30 会出错。

**修复**：
```go
case "CREATED":      return 5
case "SUBMITTED":    return 10
case "SIGNING":      return 20
case "BROADCASTING": return 30
case "CONFIRMING":   return 50
case "FAILED", "CANCELLED", "REJECTED": return 90
case "COMPLETED":    return 100
```

---

### 🟠 T7-I-2. `deposits.safeheron_coin_key` 列永远写空串

**文件**：`internal/wallet/deposit/repository.go:166-167`

```go
d.SafeheronTxKey, "", d.ChainCode, d.CoinChainID,
//                ^^ ← safeheron_coin_key 位置硬编码 ""
```

字段绑定（line 138-139）：
```
safeheron_tx_key, safeheron_coin_key, chain_code, coin_chain_id
```

并且 `DepositRow` 结构体（repository.go:39-56）根本没有 `SafeheronCoinKey` 字段，service.go 构造 DepositRow 时也没传。

**影响**：`deposits.safeheron_coin_key` 列永远为空 → 无法对账回 `coin_chains.safeheron_coin_key`，dashboard / 运维查询失能。

**修复**：
1. `DepositRow` 加 `SafeheronCoinKey string`。
2. `service.go` 构造 DepositRow 时传 `d.CoinKey`。
3. `repository.go` 占位符 7 改用 `d.SafeheronCoinKey`。

---

### 🟠 T7-I-3. webhook body 限制用 `io.LimitReader` 而非 plan D-12 锁定的 `http.MaxBytesReader`

**文件**：`internal/handlers/safeheron_webhook_handler.go:65`

```go
body, err := io.ReadAll(io.LimitReader(c.Request.Body, MaxWebhookBodyBytes))
```

plan D-12 明确锁定：
> 用 `http.MaxBytesReader(w, r.Body, 1<<20)` 限制 1MB 防 DoS。Gin 等价：handler 入口 `c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)`

差异：

| 行为 | `io.LimitReader` | `http.MaxBytesReader` |
|---|---|---|
| 超限读取 | 静默截断到 N 字节 | 主动报错 `*http.MaxBytesError` |
| HTTP 响应 | 截断 body 进入 SDK verify → 401 | 413 Request Entity Too Large |
| Safeheron 看见 | 401 → 自然重试 6 次 | 413 → 仍重试（但语义明确） |

`TestWebhook_BodyTooLargeTruncatedNotRejected` 自己注释了静默截断行为。

**修复**：
```go
c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxWebhookBodyBytes)
body, err := io.ReadAll(c.Request.Body)
if err != nil {
    var maxErr *http.MaxBytesError
    if errors.As(err, &maxErr) {
        c.AbortWithStatus(http.StatusRequestEntityTooLarge)
        return
    }
    // ...
}
```

---

### 🟠 T7-I-4. `raw_payload` re-marshal 用 schema 子集，丢字段，与文档矛盾

**文件**：`internal/handlers/safeheron_webhook_handler.go:91-92` vs `internal/wallet/deposit/models.go:58-59`

handler 注释：
> Re-marshal the decrypted envelope so the worker has a stable shape; we don't blindly store the raw outer JSON because that's still encrypted.

models.go 注释：
> Additional Safeheron fields (replaceTxHash, destinationAddressList, ...) are preserved in the raw_payload column for forensic replay.

实际：
- handler 把 SDK 返回的 `*safeheron.WebhookEvent` re-marshal → raw_payload
- `safeheron.EventDetail` (types.go:44-57) 字段集 = 13 个核心字段，**不含** `replaceTxHash` / `destinationAddressList`
- 这些字段在 raw_payload 里**根本没有**，不能 forensic replay

**修复**（二选一）：
1. 把 SDK `WebhookConvert` 返回的 plaintext JSON bytes 原样存（需要 SDK 暴露 plaintext bytes — 看 SDK 接口）。
2. 接受 schema 子集的事实，删 models.go:59 误导注释，写明"raw_payload 只保留 Phase 1 使用字段"。

倾向 #1，因为 plan §6.4 audit 链条依赖 raw_payload。需查 SDK `webhook.Convert` 是否暴露 plaintext bytes。

---

### 🟠 T7-I-5. `MarkEventError` 失败时事件保持 PENDING → hot-loop 风险

**文件**：`internal/wallet/deposit/service.go:85-98`

```go
if procErr := s.processEvent(...); procErr != nil {
    if markErr := s.repo.MarkEventError(ctx, tx, evt.ID, procErr.Error()); markErr != nil {
        return true, fmt.Errorf("...: %w", procErr)  // committed=false → tx 自动 rollback
    }
}
```

当 `MarkEventError` 自己也失败时：
- `committed=false` → defer rollback
- 事件状态保持 `PENDING`
- worker 下一次 `LockNextPendingEvent` `ORDER BY received_at` 拿同一行（FOR UPDATE SKIP LOCKED 只跳过别人锁的）
- 同一条事件不停重试，每次都 panic / DB error → CPU + 日志暴涨

**修复**：
1. `LockNextPendingEvent` 加 `AND process_attempts < N` 过滤。
2. 或 worker.go `drainSafely` 检测连续相同 eventID 错误，触发 backoff。

---

### 🟠 T7-I-6. Alert email 借用 `SendActivationEmail` → 用户收到错误模板

**文件**：`internal/alert/alert.go:81-94`

```go
func (a *AlertService) sendEmail(title, body string) {
    for _, addr := range a.recipients {
        if err := a.emailSvc.SendActivationEmail(context.Background(), addr, body); err != nil {
            // ...
        }
    }
    _ = title // reserved for future subject formatting
}
```

注释：
> EmailService.SendActivationEmail repurposed as a generic single-arg sender for Phase 1 (Resend templates aren't wired yet). The "code" argument carries the alert body.

实际后果：
- `SendActivationEmail` 走 Resend 激活码模板渲染 → 邮件主题"您的激活码" + 模板正文 `{{code}}` 位置塞着 alert 文本（甚至可能因 code 过长被 Resend 拒收）
- `title` 参数完全丢弃

**影响**：plan §3.8「告警时机：MANUAL_REVIEW / FAILED / 终态」**实际等于邮件渠道失效**，只剩飞书。

**修复**：
1. `EmailService` 加 `SendPlainEmail(ctx, to, subject, body)` 走 Resend simple email 不走模板。
2. AlertService 改用新方法。
3. 或 Phase 1 暂时 disable 邮件渠道（仅日志 + 飞书），等模板就绪后开。

---

### 🟡 T7-S-1. MANUAL_REVIEW 路径 `chain_code` 写 `d.CoinKey`，语义错位

**文件**：`internal/wallet/deposit/service.go:277`

```go
ChainCode: d.CoinKey, // best-effort when registry miss
```

`chain_code` 应是项目枚举（"ETHEREUM"/"SEPOLIA"），`coinKey` 是 Safeheron 内部 ID（"ETH_TEST7"）。注释自己写了 best-effort。

**修复**：写空串 + 把 coinKey 放到 `failed_reason` 详情或新增 audit 字段。

---

### 🟡 T7-S-2. `MarkDepositFailed` 不防 `MANUAL_REVIEW` 状态被覆盖

**文件**：`internal/wallet/deposit/service.go:235`

```go
if isFailedStatus(d.TransactionStatus) && dep.Status != DepositStatusCredited && dep.Status != DepositStatusFailed {
```

漏检 `dep.Status != DepositStatusManualReview`。后续 FAILED 事件会把 MANUAL_REVIEW 行改为 FAILED，丢失运维语义。

**修复**：加 `&& dep.Status != DepositStatusManualReview` 防覆盖。

---

### 🟡 T7-S-3. `alert.sendFeishu` 没传 ctx

**文件**：`internal/alert/alert.go:64`

用 `http.NewRequest` 而非 `WithContext`，进程退出时要等 httpClient timeout (5s) 才结束。

**修复**：`Send(ctx, level, title, fields)` + `http.NewRequestWithContext(ctx, ...)`。

---

### 🟡 T7-S-4. `minAmount, _ := decimal.NewFromString(coinChain.MinDepositAmount)` 静默吞错误

**文件**：`internal/wallet/deposit/service.go:167`

如果 `MinDepositAmount` 配置脏数据（空 / "abc"），NewFromString 报错，`_` 丢弃，minAmount = 0 → 任何金额通过校验 → 小额"尘灰"也入账。

**修复**：解析失败时走 MANUAL_REVIEW + 飞书告警提示数据脏。

---

### 🟡 T7-S-5. `TestWebhook_BenchAckTime` 不是真实 P99 基准

**文件**：`internal/handlers/safeheron_webhook_handler_test.go:193-210`

测试自己说 "sanity-check that the verify+ack roundtrip is sub-millisecond when the verifier short-circuits"，没有计时断言，也没真实 RSA 验签开销。**NF-1 P99 < 2s 需要 T9 用 wrk/k6 实测**。

---

### 🟡 T7-S-6. `/api/webhooks/core/deposit` 老路由保留

**文件**：`internal/routes/routes.go:91`

Phase 1 已停止 Core API 流程，老 webhook 路由是否还要保留？归 Phase 1 收尾决策。

---

## T8 — commit `8f34499`（前端切换）

### 决策对齐（plan §3.7 + §4 D-7/D-8）

| # | plan 锁定决策 | 实现情况 | 状态 |
|---|---|---|---|
| §3.7-1 | `wallet-service.ts` 新增 2 方法 + 删除老 `createWallet`/`getWalletInfo`/`addAddress` | `WalletService` 类内已无老方法；但 `AccountOpening.tsx` 仍以 `apiRequest` 直接调老端点 | ⚠ 部分 |
| §3.7-2 | `Deposit.tsx` 从 94 行 "Coming Soon" 改写为充值地址页 | 已重写为 238 行 Tabs + 地址 Card + supportedCoins 表 | ✅ |
| §3.7-3 | `Addresses.tsx` 不动 | 8f34499 未 touch | ✅ |
| §3.7-4 | `getDepositAddress(networkFamily): {address, networkFamily, supportedCoins}` 签名 | 实现匹配，supportedCoins 增加 `decimals` 字段 | ✅ |
| §3.7-5 | 删除老调用：`createWallet`/`getWalletInfo`/`addAddress` 前端引用全部删除 | 见 T8-I-1 — AccountOpening.tsx + App.tsx 路由保留 | ❌ |
| §3.7-6 | Deposit 页面 UI：Tabs EVM/TRON + Card（地址 + **复制按钮 + 二维码**）+ supportedCoins 列表 | Tabs + Card + 复制 + supportedCoins ✓；**二维码缺失**（`qrcode` 包已装但未用） | ❌ |
| §3.7-7 | i18n key `deposit.evm.*` / `deposit.tron.*` / `deposit.copyAddress` / `deposit.supportedCoins` | 实际命名 `deposit.addressCard.*` / `deposit.tabs.evm` / `deposit.tabs.tron` / `deposit.supportedCoins.*` — 偏离 plan 命名（结构上更合理） | ⚠ 偏离 |
| §3.7-8 | React Query `staleTime: 5 * 60_000` | `Deposit.tsx:26` 一致 | ✅ |
| §6 F-13 | 前端 Dashboard 显示 EVM 地址 + 币种列表 | 实现可观察，待 T9 demo 验收 | ⏸️ |
| §6 NF-6 | `npm run build` / `npm run test` 通过 | commit 自报通过；未在本次 review 复现 | ⏸️ |

**结论**：8 项决策中，**3 项偏离**（其中 §3.7-5 / §3.7-6 是 D-7/§3.7 锁定违背，§3.7-7 i18n 命名偏离属可补登）；功能主体已就位。

---

### 🟠 T8-I-1. `AccountOpening.tsx` 仍调老 `/api/wallet/create` + `/api/wallet/addresses` — 违背 plan D-7 / §3.7

**文件**：
- `src/pages/dashboard/AccountOpening.tsx:300-310`（`createMutation` → `apiRequest("/api/wallet/create", ...)`)
- `src/pages/dashboard/AccountOpening.tsx:325-335`（`addAddressMutation` → `apiRequest("/api/wallet/addresses", ...)`)
- `src/App.tsx:14, 47`（路由 `<Route path="account-opening" element={<AccountOpening />} />` 仍挂在 DashboardLayout 下）

**plan §3.7 锁定**：
> 移除调用：`createWallet` / `getWalletInfo` / `addAddress` **在前端引用全部删除**；这些旧端点 Go 端返回 410 兜底

**commit message 自承**："AccountOpening.tsx 仍使用 apiRequest 调老端点（已从 sidebar 移除，路由还在，被调用时后端返 410 Gone — Phase 2 清理）" — 但**把"全部删除"延期到 Phase 2 是当前 commit 单方面改方向**，未与 plan 对齐。

**后果**：
- URL 直达 `/dashboard/account-opening` 仍可触发请求 → 后端返 410 → 用户看到 `err.message` toast（"DEPRECATED, use /api/wallet/deposit-address" 之类的原始错误码）
- plan §6 S-4 安全验收"老 `/api/wallet/create` 等端点返回 410 Gone + 提示新端点"达成，但**前端不该有触发点**

**修复建议**（按 plan 严格执行，三选一）：
1. **直接删除** `AccountOpening.tsx` 文件 + `App.tsx:14, 47` 路由声明（最干净，符合 plan "全部删除"）
2. 路由 `account-opening` 改为 `<Navigate to="/dashboard/deposit" replace />`（保留 URL 兼容）
3. `AccountOpening.tsx` 整体改为"功能已迁移"提示页 + 不调任何 API + 引导用户去 Deposit 页

**推荐**：方案 1。文件 ~500 行二期会重做，留着只是技术债。

---

### 🟠 T8-I-2. plan §3.7 锁定的"二维码"未实现 — 违背 D-7

**文件**：`src/pages/dashboard/Deposit.tsx:67-89`

**plan §3.7 锁定**：
> Deposit 页面 UI | 上方 Tabs 切 EVM/TRON，下方 Card 显示地址（**含复制按钮 + 二维码**）+ supportedCoins 列表

**现状**：
- Card 只渲染地址文本 + Copy 按钮（line 75-87），**无二维码 canvas/svg**
- `package.json:73, 100` 已装 `qrcode: ^1.5.4` + `@types/qrcode: ^1.5.6` — 依赖就位但未使用

**后果**：
- 手机扫码到账场景不可用（用户必须复制粘贴）
- plan §6 F-13 "前端 Dashboard 点击 Deposit 看到 EVM 地址 + 6 个币种列表（生产）/ 2 个币种（testnet）" 文本验收能过，但 plan §3.7 锁定的 UI 元素遗漏

**修复建议**：
```tsx
import QRCode from 'qrcode';
// 用 useEffect + canvas ref 生成 QR：
// QRCode.toCanvas(canvasRef.current, data.address, { width: 160 })
```
紧邻地址文本下方加 160×160 QR canvas。

---

### 🟠 T8-I-3. i18n key `common.error` 不存在 → fallback 时显示 key 字符串

**文件**：`src/pages/dashboard/Deposit.tsx:58`

**代码**：
```tsx
<CardDescription>
  {error instanceof Error ? error.message : t("common.error")}
</CardDescription>
```

**实测**：`python3 -c "import json; print(json.load(open('src/i18n/locales/en.json')).get('common', {}).get('error'))"` 返回 `None` — `en.json` / `zh.json` 均**无** `common.error` key。

**后果**：当 React Query queryFn 抛出非 `Error` 类型（罕见但可能：例如 `throw "string"`）时，`t("common.error")` 返回 key 本身字符串 `"common.error"` 直接 render 给用户。

**触发概率**：低（`parseOrThrow` 全部抛 `Error`），但兜底失效是 plan 锁定 "Error 状态" 的隐性 bug。

**修复建议**（二选一）：
1. fallback 改用现有 key：`t("deposit.addressCard.errorTitle")`
2. 在 `en.json` / `zh.json` `common` 段下补 `"error": "Unexpected error"` / `"未知错误"`

---

### 🟠 T8-I-4. i18n key 命名偏离 plan §3.7 / D-8

**文件**：`src/i18n/locales/en.json:776-792` + `zh.json:855-871`

**plan §3.7 / D-8 锁定**：
> 新增 `deposit.evm.title` / `deposit.tron.title` / `deposit.copyAddress` / `deposit.supportedCoins` 等

**实际实现**：
- `deposit.addressCard.{label, hint, copy, copied, copyFailed, errorTitle}`
- `deposit.tabs.{evm, tron}`
- `deposit.supportedCoins.{title, chain, coin, minDeposit, empty}`

**分析**：
- 实际结构 EVM/TRON 共用一套 `addressCard` namespace（避免重复定义两套 title/hint/copy）— **结构上更合理**
- 但与 plan / D-8 锁定的 `deposit.evm.title` / `deposit.tron.title` 显式分两套不符
- plan 里写 "等" 字暗示举例，但 D-8 是 "锁定决策"性质 — 应有正式登记

**后果**：未来 plan reviewer 对比 §3.7 与代码会发现偏离；属"plan 与代码漂移"。

**修复建议**（二选一）：
1. **更新 plan §3.7 + D-8**（推荐），把锁定值改为实际命名 `deposit.addressCard.*` / `deposit.tabs.*` / `deposit.supportedCoins.*`，注明 "T8 实施时为避免 EVM/TRON 重复定义，改用 addressCard 共享 namespace"
2. 按 plan 重命名 i18n key 为 `deposit.evm.title` / `deposit.tron.title` 等（不推荐，结构倒退）

---

### 🟡 T8-S-1. `WalletService.getSupportedChains()` 是死代码

**文件**：`src/lib/wallet-service.ts:71-76`

**现状**：
- 定义了 `getSupportedChains(): Promise<SupportedChainsResponse>` 方法
- 测试文件 `wallet-service.test.ts:92-124` 覆盖了 2 个 case
- **但 Deposit.tsx 全文未调用** — `getDepositAddress` 的响应里已经嵌了 `supportedCoins`，UI 直接用那个

**全项目搜索**：`grep -rn "WalletService\." src/ --include="*.ts" --include="*.tsx" | grep -v ".test."` 只命中 `Deposit.tsx:25` 的 `getDepositAddress`。

**后果**：方法 + 测试 + 类型定义 ~30 行死代码；plan §6 NF-4 测试覆盖率会被无意义路径稀释。

**修复建议**（YAGNI）：删除 `getSupportedChains` + `SupportedChainsResponse` 类型 + 对应 2 个测试用例。若 Phase 2 需要再加回。

---

### 🟡 T8-S-2. Deposit.test.tsx 无 Loading 状态测试覆盖

**文件**：`src/pages/dashboard/Deposit.test.tsx`

**现状**：4 个 case 覆盖默认 EVM tab / 切到 TRON / 复制按钮 / 错误态展示；**Skeleton/isLoading 状态无断言**。

`grep -n "Skeleton\|isLoading\|loading" src/pages/dashboard/Deposit.test.tsx` 无命中。

**后果**：plan §3.7 锁定了 Loading 状态的 UI 行为（`Deposit.tsx:40-47` Skeleton 渲染），未来重构若误删 Skeleton 路径，测试无法捕获。

**修复建议**：补 1 个 case：
```tsx
it('shows skeleton while loading', () => {
  global.fetch = vi.fn(() => new Promise(() => {})); // never resolve
  renderDeposit();
  expect(document.querySelector('[data-slot="skeleton"]')).toBeInTheDocument();
});
```

---

### 🟡 T8-S-3. Deposit 错误展示直接渲染后端错误码（pattern 复用，已有 issue）

**文件**：`src/pages/dashboard/Deposit.tsx:58`

**现状**：`{error instanceof Error ? error.message : ...}` — 后端 `ASSIGN_FAILED` / `POOL_UNAVAILABLE` / `REGISTRY_UNAVAILABLE` 等内部错误码原样展示。

`Deposit.test.tsx:107-119` 实际断言了 `screen.getByText(/POOL_UNAVAILABLE/i)` —— 测试也认可"错误码直接展示"。

**项目现状**：`auth-service` / `lending-service` 等已是此 pattern。**已有 issue**：与 T6-I-3 一致（后端泄露错误细节）—— 后端修了之后，前端这条自然消失。

**后果**：
- 安全：暴露后端实现细节（虽是 410/503 等通用业务码，仍非用户可读）
- UX：用户看不懂

**修复建议**：T6-I-3 修完后，前端再加一层 i18n 错误码映射表 `t(\`errors.${code}\`)`，无映射时降级显示通用 `t("common.error")`（需先补齐 T8-I-3）。

---

### 🟡 T8-S-4. `wallet-service.ts` 无 401 自动登出处理

**文件**：`src/lib/wallet-service.ts:34-46`

**现状**：`parseOrThrow` 统一抛 Error，不区分 401 / 403。

**后果**：token 过期 → 用户看到 "Failed to fetch deposit address" → 不会自动跳登录 / 无法理解原因。

**项目现状**：所有 service 都不处理 — 已有 pattern。

**修复建议**：T8 范围内**不动**；下个 release 加 fetch wrapper 中间件统一处理。**不阻塞 Phase 1 上线**。

---

### 🟡 T8-S-5. `encodeURIComponent(family)` 多余

**文件**：`src/lib/wallet-service.ts:61`

```ts
`/api/wallet/deposit-address?networkFamily=${encodeURIComponent(family)}`
```

`family` 已通过 `z.enum(['EVM', 'TRON'])` 校验，两个值都不含 URL 特殊字符。

**分析**：作为防御性编码无害；如果未来 networkFamily 扩到 `BTC` / `SOLANA` 等，仍然安全。

**建议**：保留现状（防御性 OK）。

---

### 🟡 T8-S-6. 老 `deposit.comingSoon.*` i18n 残留 18 个 key 未清理

**文件**：
- `src/i18n/locales/en.json` — `deposit.comingSoon` 整段（title / description / step1-3 / contactUs / contactDesc / supportedCoins / howToDeposit）
- `src/i18n/locales/zh.json` — 同上

**commit message 自承**："保留旧 deposit.comingSoon.* 直到下个 release 清理"

**plan §3.7 / D-8**："保留旧 `deposit.comingSoon.*` 直到下个 release 清理" — **已对齐**

**全项目搜索**：`grep -rn "deposit\.comingSoon" src/ --include="*.tsx"` 应为空（确认无前端代码引用）。

**修复建议**：与 T8 范围一致，**保持现状**；只在 Phase 2 cleanup commit 清理。**仅作可追踪登记**，不在本轮 batch 修。

---

## 修复 batch 规划

T6/T7/T8 已审完。按以下顺序成 batch：

### 第一波 — plan 锁定决策违背（4 项）
1. **T6-I-2** — `Container.Close()` 注释 + PEM 清理（违背 D-3）
2. **T7-I-1** — `status_rank` 数值表对齐 plan §3.1 锁定值
3. **T7-I-3** — webhook body 限制改用 `http.MaxBytesReader`（违背 D-12）
4. **T8-I-1** — `AccountOpening.tsx` 引用 + 路由清理（违背 D-7 / §3.7）
5. **T8-I-2** — Deposit 页面补二维码（违背 §3.7）
6. **T8-I-4** — plan §3.7 / D-8 与实际 i18n 命名对齐（建议改 plan）

### 第二波 — 安全 & 健壮性（5 项）
7. **T6-I-3** — `GetDepositAddress` 错误细节脱敏
8. **T6-I-1** — `SetSafeheronDeps` typed-nil 防御
9. **T7-I-4** — webhook `raw_payload` 改存原始字节，不做 schema 子集 re-marshal
10. **T7-I-5** — `MarkEventError` 失败时 hot-loop 防御（指数退避 / DLQ）
11. **T7-I-6** — Alert email 独立模板，不借用 `SendActivationEmail`
12. **T8-I-3** — i18n `common.error` 补 key（或 fallback 改用现有 key）

### 第三波 — 可维护性（Suggestion，11 项）
13. **T6-S-1** — `Registry.AllChains()` 过滤 `Chain.Enabled`
14. **T6-S-2** — 清理 `[...route].ts` debug `console.log`
15. **T6-S-3** — `GetSupportedChains` 补 empty chains 测试
16. **T6-S-4** — `Handler.PoolManager` / `Handler.WalletRegistry` 改为非导出 + getter
17. **T7-S-1** — MANUAL_REVIEW 路径 `chain_code` 字段名修正
18. **T7-S-2** — `MarkDepositFailed` 防 `MANUAL_REVIEW` 覆盖
19. **T7-S-3** — `alert.sendFeishu` 传 ctx
20. **T7-S-4** — `decimal.NewFromString` 错误显式处理
21. **T7-S-5** — `TestWebhook_BenchAckTime` 改为真实 P99 直方图
22. **T7-S-6** — `/api/webhooks/core/deposit` 老路由清理决策
23. **T8-S-1** — 删 `getSupportedChains` 死代码（YAGNI）
24. **T8-S-2** — 补 Loading 状态测试
25. **T8-S-3** — 错误码 i18n 映射表（依赖 T6-I-3 + T8-I-3）
26. **T8-S-4** — fetch wrapper 处理 401（下个 release）
27. **T8-S-6** — `deposit.comingSoon.*` Phase 2 清理（不在本批）

每波单 commit，便于回查。

**修复入口顺序建议**：第一波 → 第二波 → 第三波（前两波必须在 T9 灰度上线前完成；第三波可分批合并）。

---

## 第二轮 Review（2026-05-12，修复批审查）

> 范围：未 commit 的工作树（30 个文件）+ 新增的 `deposit_handler_test.go` / `setsafeheron_deps_test.go` / 本 findings 文档
> 方法：5 维度审查（Correctness / Readability / Architecture / Security / Performance）
> 重点：验证"修复完成 23/23"声称与代码实际对齐情况

### 🔴 R2-C-1. Frontend ↔ Backend 查询参数命名不一致 — Deposit 流程在生产环境不工作

**严重度**：🔴 Critical — **上线即 100% 失败**

**文件**：
- 前端：`src/lib/wallet-service.ts:57` → `/api/wallet/deposit-address?networkFamily=${...}` （**camelCase**）
- 前端：`src/pages/dashboard/Deposit.test.tsx:45,58` → mock fetch 用 `networkFamily=EVM` / `networkFamily=TRON`
- 后端：`internal/handlers/deposit_address_handler.go:75` → `family := c.Query("network_family")` （**snake_case**）
- 后端测试：`internal/handlers/deposit_address_handler_test.go:82,91,114,145,170,186,200,245,276` 全用 `network_family=...`

**问题**：
1. **CLAUDE.md 明确规定**："All API request/response fields MUST use camelCase naming" — Go backend 当前违反项目锁定的命名约定。
2. 调用链断裂：前端发 `?networkFamily=EVM` → Vercel 路由透传 → Go `c.Query("network_family")` 返回 `""` → 进入 `!allowedNetworkFamilies[""]` 分支 → 返回 `400 INVALID_NETWORK_FAMILY`。
3. **测试盲区**：
   - Go 单测把 query 写死成 `network_family=`，"绿了" 不代表生产可用
   - 前端 vitest 用 mock fetch 拦截，URL 里只要含 `networkFamily=EVM` 就返回 mock 数据 —— 也绿了
   - 没有端到端集成测试覆盖前后端真实拼接路径

**为什么 Phase C 验收没发现**：
- Phase B 覆盖率 84.1% 的 deposit 业务逻辑测试没覆盖 HTTP query 解析层
- "前端 npx vitest run 12/12 pass" 全部是 mock 拦截，未触达真实后端
- "go test -race" 测的也是直接构造的 gin.Context，绕过了真实 HTTP 路由

**修复方案**（二选一，推荐 1）：

1. **后端改 camelCase**（推荐，符合 CLAUDE.md 约定）：
```go
// internal/handlers/deposit_address_handler.go:75
family := c.Query("networkFamily")  // 修复
// :79
"message": "networkFamily must be EVM or TRON",
```
+ 同步改 `deposit_address_handler_test.go` 全部 9 处 `network_family=` → `networkFamily=`。

2. 前端改 snake_case（不推荐，违反 CLAUDE.md）。

**回归测试要求**：
- 加一个集成测试：用 `httptest.NewServer` 启动真实 gin engine，前端 fetch mock 改为真实 HTTP roundtrip，验证 `?networkFamily=EVM` 走通 200 + 期望 body。
- 或 Playwright E2E 直接打 dev server 的 `/api/wallet/deposit-address` 路径。

---

### 🟠 R2-I-1. T7-I-2（`safeheron_coin_key` 列永远写空串）**未修复但 review 文档声称"23/23 完成"**

**严重度**：🟠 Important — 数据完整性 + 文档诚信

**文件**：`internal/wallet/deposit/repository.go:166-167`

```go
d.SafeheronTxKey, "", d.ChainCode, d.CoinChainID,
//                ^^ ← 第 7 个占位符 safeheron_coin_key 仍硬编码空串
```

**审查发现**：
- 本文档第 244-269 行（T7-I-2 段）原始审查识别为 🟠 Important
- 但"修复完成快照"（第 12-14 行）的 23 项清单**未包含 T7-I-2**
- "23/23 全部完成"的声称与实际工作不符 —— 这一项 D-P0 的数据完整性缺陷仍存在

**与 plan 决策对齐**：T7-I-2 属于"plan §3 / 数据库 schema"范畴 —— 按 `[[plan-vs-code-divergence-handling]]` 准则的第 2 类（API/schema），**应改代码对齐 plan**。

**修复**：
1. `DepositRow` struct 加 `SafeheronCoinKey string` 字段（`models.go`）
2. `service.go:185-201` 构造 row 时传入 `SafeheronCoinKey: coinChain.SafeheronCoinKey`
3. `repository.go:167` 占位符 7 改为 `d.SafeheronCoinKey`
4. UPDATE 路径同步刷新 `safeheron_coin_key`（当前 DO UPDATE SET 没有这个字段，需要加上以便回填历史数据）

**回归测试**：在 `service_test.go` 加用例断言 deposit row 的 `SafeheronCoinKey` 字段非空（mock repo `UpsertDeposit` 捕获写入参数）。

---

### 🟠 R2-I-2. Alert email 模板 XSS — webhook 来源数据未 HTML 转义

**严重度**：🟠 Important — 内部告警系统但接收对象是运营人员

**文件**：`internal/services/email_service.go:44-45`

```go
htmlBody := fmt.Sprintf(`<!DOCTYPE html>...<h2 ...>%s</h2><pre ...>%s</pre>...`,
    subject, body, time.Now().Format("2006-01-02 15:04:05"))
```

**问题**：
- `subject` 由 `alert.go:92` 拼接 `"【Phase1告警】" + title`，title 部分目前是常量（"Deposit failed" / "Deposit manual review"），暂时安全
- `body` 是 `formatAlert` 输出，里面包含 webhook 来源的字段：
  - `destinationAddress`（攻击者可控 — 如果他们能让 Safeheron 推送任意 `destinationAddress` 到我们的回调，但这个走 Safeheron 平台签名校验所以可信度较高）
  - `txKey` / `coinKey` / `amount`（同上）
  - `reason` / `transactionStatus`（内部产生，安全）
- 当前虽然 webhook 内容经 SDK 签名校验，但是 Safeheron 平台账户系统外的人也可能创建带特殊字符的地址 / coinKey
- 把这些字段直接放到 `<pre>%s</pre>` 里，邮件客户端会按 HTML 渲染 `<pre>` 内容 —— `<script>` 一般被 strip 但 `<img src=x onerror=...>` 等仍可触发

**对比**：`SendActivationEmail` 把激活码插入模板时也没有转义，但激活码是后端生成的 6 位数字，可控；alert 模板没有这种内容约束。

**修复**：
```go
import "html"
// ...
htmlBody := fmt.Sprintf(`...<h2 ...>%s</h2><pre ...>%s</pre>...`,
    html.EscapeString(subject), html.EscapeString(body), ...)
```
或改用 `html/template` 库自动转义。

---

### 🟠 R2-I-3. `container.go:309` 把 `RESEND_API_KEY` 完整内容打到 stdout 日志

**严重度**：🟠 Important — 密钥泄露到 stdout / 容器日志收集系统

**文件**：`internal/container/container.go:309-312`

```go
fmt.Printf("[EmailService] Initialized - enabled: %v, apiKey: '%s', fromEmail: '%s'\n", 
    emailService.IsEnabled(), 
    os.Getenv("RESEND_API_KEY"),   // ← 整个 API key 打到 stdout
    os.Getenv("SENDER_EMAIL"))
```

**问题**：
- Resend API key（生产）以明文打印到容器 stdout，被 Vercel / 任何 log aggregator 抓走
- 即使 `enabled` 已经包含了"key 是否存在"的信息，把完整 key 打到 log 是双重错误
- pre-existing（不是本次 Safeheron 修复引入）—— 但**修复 batch 期间触碰了这个文件却没顺手清理**

**修复**：
```go
log.Printf("[EmailService] Initialized - enabled: %v, fromEmail=%q, apiKeyHash=%s",
    emailService.IsEnabled(),
    os.Getenv("SENDER_EMAIL"),
    sha256Prefix(os.Getenv("RESEND_API_KEY")))  // 或直接删除整行 Printf
```
（删除整行最干净；如果保留作启动信号，只打"是否设置"+长度）。

---

### 🟠 R2-I-4. `deposit_handler_test.go` 单元测试无法验证路由层 410 — 测试与实际 wiring 脱节

**严重度**：🟠 Important — 测试盲点

**文件**：`internal/handlers/deposit_handler_test.go:15-30` + `internal/routes/routes.go:91`

**问题**：
- 测试直接构造 `gin.Context` 然后 `h.HandleDepositWebhook(c)` —— 完全绕过了 gin engine 的路由分发层
- 实际上路由 `POST /api/webhooks/core/deposit` 是否真的 wire 到 `HandleDepositWebhook` ？测试不验证
- 如果未来有人误改 `routes.go:91` 把 `/core/deposit` 改成别的路径，**这个测试不会失败**
- T7-S-6 修复声称"老 Core-API webhook 410" —— 但 wiring 没测，只测了 handler 函数本身

**修复**：
- 改用 `gin.New()` 注册路由后 `httptest.NewRequest + engine.ServeHTTP` 走真实路由：
```go
r := gin.New()
r.POST("/api/webhooks/core/deposit", h.HandleDepositWebhook)
req := httptest.NewRequest(http.MethodPost, "/api/webhooks/core/deposit", ...)
r.ServeHTTP(w, req)
```
- 或在 `routes_test.go` 加一组路由 wiring 测试覆盖所有 410 / 404 / 200 关键路径。

---

### 🟡 R2-S-1. `Handler.SetSafeheronDeps` 用 reflect 检测 typed-nil — 隐式契约 + 性能小开销

**严重度**：🟡 Suggestion — 设计取舍

**文件**：`internal/handlers/handlers.go:92-102`

```go
func isNilInterface(i any) bool {
    if i == nil { return true }
    v := reflect.ValueOf(i)
    switch v.Kind() {
    case reflect.Ptr, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func:
        return v.IsNil()
    }
    return false
}
```

**评价**：
- ✅ T6-I-1 防御目标达成，typed-nil pollute 路径被阻断
- ⚠ 反射检测仅在 SetSafeheronDeps 调用一次（启动时），性能不是问题
- ⚠ 但 isNilInterface 是新工具函数，文档建议 `accept interfaces, return structs` —— **更干净的方案是 SetSafeheronDeps 直接收具体类型 `*pool.Manager` / `*walletconfig.Registry`**，编译期就能拦截 typed-nil 包装到接口的可能性
- ⚠ 当前实现允许"接口类型 + reflect 检查"组合，未来调用方可能误用 `var pm DepositPoolManager` 然后 SetSafeheronDeps(pm)，仍然会被 reflect 兜住，但这是隐式契约

**建议**：
- 短期保留（已通过测试覆盖 typed-nil 路径）
- 长期把签名改成具体类型，删除 isNilInterface helper

---

### 🟡 R2-S-2. 修复声称"23/23"但实际可数到 **22 项** — 第三波 Suggestion 计数错误

**严重度**：🟡 Suggestion — 文档清洁度

**文件**：本 findings 第 12-14 行 + 第 692-707 行

**问题**：
- 文档顶部声称"第三波 Suggestion（11/11）"并列出：T6-S-1, T6-S-2, T6-S-3, T6-S-4, T7-S-1, T7-S-2, T7-S-3, T7-S-4, T7-S-5, T7-S-6, T8-S-1, T8-S-2 = **12 项**
- 与"11/11"声称不一致
- 总数：6（第一波）+ 6（第二波）+ 12（第三波）= **24**，但顶部说 23
- 原始 batch 规划（第 676-707 行）共 27 项，按 6+6+12 = 24 + 删除 T8-S-3/4/6 + 跳过 T7-I-2 才能凑出 23

**修复**：
- 把顶部"第三波（11/11）"改成"第三波（12/12）"
- 把总数"23/23"改成"22/23"（如果 T7-I-2 故意跳过，则把跳过决策写到"已知遗留"段；如果一并补做则改回 24/24）
- 加"已知遗留"段说明 T7-I-2 / T8-S-3 / T8-S-4 / T8-S-6 状态

---

### 🟡 R2-S-3. `Deposit.test.tsx` mock 的 URL 用 `networkFamily=` —— 一旦后端改 camelCase 测试需同步

**严重度**：🟡 Suggestion — 测试维护提示

**文件**：`src/pages/dashboard/Deposit.test.tsx:45,58`

如果按 R2-C-1 把后端改成 camelCase，前端 vitest mock 的 URL 匹配字符串无需改动（已是 `networkFamily=`），但 Go 单测的 `network_family=` 需要全替换。

测试目前用的 `u.startsWith('/api/wallet/deposit-address?networkFamily=EVM')` —— 对 URL 前缀做精确匹配是脆弱的，未来如果加 query string 顺序变化或加新参数（如 `?networkFamily=EVM&_=cacheBust`）会让 mock fall through。

**建议**：用 `URL` 解析 + query 字典比对替代字符串前缀匹配。

---

### 🟡 R2-S-4. 修复 batch 未 commit — 30 个文件未入库，存在被覆盖 / 丢失风险

**严重度**：🟡 Suggestion — 流程提醒

**现状**（`git status --short`）：30 个文件 modified / added / deleted（见本文档第 41-43 行清单）。

**建议**：在 R2-C-1 修完之后，按第一波/第二波/第三波分 3-4 个 commit 入库，便于 cherry-pick 和回查。当前一锅端的 batch 如果 review 决定回退某一项，操作面太大。

---

## 第二轮 Review 总结

| 严重度 | 数量 | 编号 |
|---|---|---|
| 🔴 Critical | **1** | R2-C-1（前后端 query 命名不一致，生产 100% 失败） |
| 🟠 Important | **4** | R2-I-1（T7-I-2 未修但声称完成）/ R2-I-2（XSS）/ R2-I-3（API key 泄日志）/ R2-I-4（路由 wiring 未测） |
| 🟡 Suggestion | **4** | R2-S-1（reflect 取舍）/ R2-S-2（计数错）/ R2-S-3（mock URL 匹配脆弱）/ R2-S-4（commit 拆分） |

### 上线前必修（阻塞 T9 灰度）

1. **R2-C-1**：deposit-address API 在生产 100% 返回 400 —— 必须修
2. **R2-I-3**：API key 写 stdout —— 安全合规阻塞
3. **R2-I-1**：T7-I-2 数据完整性 —— 影响对账 / dashboard，可放灰度后第一周补，但需要决策是补做还是显式放弃

### 上线后第一周修

4. **R2-I-2**：alert email XSS（影响面有限，但安全债不要堆积）
5. **R2-I-4**：路由 wiring 测试加固（防回归）
6. **R2-S-2**：文档更新到准确状态

### 流程改进建议

- 把"23/23 全部完成"这种声称的依据**写到 PR description 里**，而不是 review 文档自我宣告
- 修复 batch 拆 commit 后再 review，单 commit 更易追溯
- 加端到端测试覆盖 frontend → API → Go 真实拼接路径（R2-C-1 这种 query 命名不一致的 bug 单元测试永远抓不到）
