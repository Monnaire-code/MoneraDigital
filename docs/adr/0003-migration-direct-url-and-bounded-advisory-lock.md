# ADR 0003: 迁移使用 Direct 连接并对 Advisory Lock 有界等待

## Status

Accepted — 2026-07-23（grill 共识；对应 GitHub #35）

## Context

2026-07-18 生产执行受控迁移 058 时，迁移进程使用应用侧 Neon **pooled** `DATABASE_URL` 连接，并在抢占 session 级 advisory lock（key `8675309`）时无限阻塞。空闲池连接仍可持有该 session lock，释放路径与连接生命周期不一致，导致迁移无法自愈，最终依赖人工定位并终止 holder。

与此同时，仓库已完成**发布路径**加固（standard 单路径、`-exact-version`、`EXPECTED_MIGRATION_CEILING`、fail-closed 编排）。那解决的是「发哪一版、走哪条流水线」，**不**解决「迁移连哪根库、锁等多久」。

约束：

- 业务服务可继续使用 pooled `DATABASE_URL`（Neon Scale to Zero / 连接复用）。
- 不自动 `pg_terminate_backend` 杀死占锁会话。
- 日志不得输出连接串、密码或可还原凭据的材料。
- 不改变 controlled exact-version / ceiling 发布模型。

## Decision

1. **迁移连接与业务连接分离（配置规则 C）**  
   - 解析顺序：若设置 `MIGRATION_DATABASE_URL` 则仅用它跑 migrate；否则 fallback 到 `DATABASE_URL`。  
   - **Stage / production**：必须提供 `MIGRATION_DATABASE_URL`，且必须是 direct/unpooled；禁止依赖业务 pooler URL。  
   - **local / development / test**：允许仅配置 direct 的 `DATABASE_URL`（无专用变量时 fallback）。

2. **Pooler fail-closed（判定规则 A）**  
   - 对**最终用于 migrate 的 URL**，若 hostname（忽略大小写）包含 `-pooler`，拒绝启动并非 0 退出。  
   - 该检测对迁移连接**全环境**生效（含 local：故意使用 Neon pooler 也应被拦）。

3. **Advisory lock 有界等待（默认 30s，硬失败）**  
   - 默认超时 **30 秒**，可通过环境变量覆盖（建议名 `MIGRATION_ADVISORY_LOCK_TIMEOUT`）。  
   - 超时 → migrate **失败退出**，不静默自动重试；需要重试则由人工或 CI 重新触发整次发布/迁库。  
   - 实现可用 try-lock 轮询或等价有界机制；语义是「有界」而非「无限 `pg_advisory_lock`」。

4. **超时后只读 holder 诊断（不做自动杀连接）**  
   - 超时后尽力查询 `pg_locks` / `pg_stat_activity` 类信息，日志可含：lock key、timeout、holder pid、state、年龄、`application_name`。  
   - **不**打印完整查询文本（避免业务/敏感 SQL）；**不**自动 terminate。  
   - 诊断不可用时标明 unavailable，仍以锁超时为失败原因。

5. **文档与运维**  
   - 发布/迁移说明明确：Stage/Prod 配置 Neon **direct** `MIGRATION_DATABASE_URL`；业务保持 pooler `DATABASE_URL`。  
   - 保留 exact-version / ceiling 既有要求；补充锁超时与 holder 排查指引。

## Alternatives considered

### 1. 不新增变量，仅拒绝 pooler 的 `DATABASE_URL`

拒绝作为 Stage/Prod 唯一方案。服务器上业务与 migrate 常共享环境时，无法同时满足「业务 pooler + 迁移 direct」。

### 2. 全局强制仅有 `MIGRATION_DATABASE_URL`

可行但抬高本地成本。采用「优先专用 URL + Stage/Prod 强制专用 + local 可 fallback direct `DATABASE_URL`」。

### 3. 超时后自动 terminate holder 并重试

拒绝。误杀风险高，超出事故加固范围；占锁处置保留给人。

### 4. 默认数分钟超时或无限等但打日志

拒绝。发布窗口仍不可控，与 058 诉求相反。默认短超时（30s）fail closed，必要时 env 显式加长。

### 5. 白名单「仅允许特定 Neon direct host 形态」

拒绝。Neon/拓扑变更会使合法迁移脆弱；以「禁 `-pooler` + 有界锁」为 pragmatic 护栏。

## Consequences

- Stage/Prod 运维必须新增并保管 **direct** `MIGRATION_DATABASE_URL`（与业务 pooler 分离）。  
- 误用 pooler 跑 migrate 会在启动期失败，而不是卡在锁上。  
- 并发 migrate 或残留 holder 时，发布在约默认 30s 内红灯，并尽可能给出 holder 线索。  
- 业务连接池策略不变；CU / 连接数优化不依赖本 ADR。  
- 实现以 GitHub **#35** 为工单载体；本 ADR 为决策记录。

## Related

- Issue: https://github.com/Monnaire-code/MoneraDigital/issues/35  
- Ops notes: `docs/security/MIGRATION-NOTES.md`  
- Adjacent (already shipped): standard deploy path, exact-version ceiling control  
