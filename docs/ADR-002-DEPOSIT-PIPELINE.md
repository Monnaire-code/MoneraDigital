# ADR-002: Safeheron 充值流水线架构

## Status

ACCEPTED

## Date

2026-05-10

## Context

MoneraDigital 需要接入 Safeheron MPC 托管钱包，实现用户充值的自动化处理。核心需求：

- 接收 Safeheron webhook 通知（链上交易确认）
- 将充值金额入账到用户内部账户
- 满足 KYT（Know Your Transaction）合规筛查要求
- 处理异常情况（未知地址、金额过小、KYT 高风险）
- 保证幂等性和数据一致性

关键约束：
- Safeheron webhook 有 5s 超时，超时会重试（最多 10 次）
- KYT API 评估可能需要数秒到数分钟
- 生产环境 KYT 不可跳过（合规硬性要求）
- 单笔充值必须恰好入账一次（不多不少）

## Decision

采用 **webhook 同步入库 + worker 异步处理** 的两阶段架构，KYT 筛查作为入账前的必经门控。

### 架构总览

```
Safeheron Console
    ↓ POST /api/webhooks/safeheron
    
Phase 1: 同步入库（SafeheronWebhookHandler）
    IP 白名单校验 → RSA 验签 → 解密 → 写入 safeheron_webhook_events（processed=false）
    → 立即返回 {"code":"200","message":"SUCCESS"}（满足 5s 超时）

Phase 2: 异步处理（Deposit Worker，1s 轮询）
    取 processed=false 事件 → ProcessOne()
    → 匹配 address_pool（查找地址归属）
    → 校验 coin_chain 配置 + 最小充值金额
    → KYT 合规筛查（Safeheron KytReport API）
    → 根据 KYT 结果决策：入账 / 人工审核 / 失败
    → 标记事件 processed=true
```

### KYT 筛查三阶段

```
链上 COMPLETED
    ↓
初查（KytReport API）
    ├─ TRIGGERED + LOW      → 直接 CREDITED（入账）
    ├─ TRIGGERED + MEDIUM+  → MANUAL_REVIEW（飞书告警）
    ├─ UNTRIGGERED          → KYT_PENDING（等待异步评估）
    └─ API 失败             → 事件回 PENDING，下次重试

KYT_PENDING 状态
    ↓ 收到 AML_KYT_ALERT webhook
    ├─ LOW                  → CREDITED
    └─ MEDIUM+              → MANUAL_REVIEW

超时兜底（20min）
    ↓ Worker 定时扫描 KYT_PENDING + updated_at > 20min
    → 再次调用 KytReport API
    ├─ 有结果              → 按结果决策
    └─ 仍无结果            → MANUAL_REVIEW + 告警
```

### 入账事务（ProcessOne 的 creditDeposit）

单次入账使用数据库事务保证原子性：

```
BEGIN
  1. UPDATE deposits SET status='CREDITED'（WHERE status NOT IN 终态）
  2. UPDATE account SET balance = balance + amount（CHECK 约束保证非负）
  3. INSERT journal（biz_type=10, ref_id=deposit.id）
COMMIT
```

终态保护：CREDITED / FAILED 状态的 deposit 不可被覆写（ErrDepositTerminalState），MANUAL_REVIEW 不可被 MarkDepositFailed 覆写。

## Alternatives Considered

### 方案 A：webhook 同步处理（一步到位）

在 webhook handler 中直接完成地址匹配 + KYT + 入账。

- **优点**：架构简单，无 worker 进程
- **缺点**：
  - KYT API 调用可能超过 Safeheron 5s 超时 → webhook 重试 → 重复处理风险
  - webhook handler 长时间占用连接
  - 失败重试逻辑与 webhook 接收耦合
- **否决原因**：KYT 延迟 + Safeheron 超时约束使同步处理不可行

### 方案 B：消息队列（RabbitMQ / SQS）

webhook 入库后发消息到队列，consumer 处理。

- **优点**：松耦合，天然支持重试和死信
- **缺点**：
  - 引入额外基础设施（当前团队规模不需要）
  - 运维复杂度增加
  - 当前充值量（日均 <100 笔）不需要消息队列的吞吐能力
- **否决原因**：过度设计。DB 轮询在当前规模下完全足够，且无需额外依赖

### 方案 C：KYT 后置（先入账再筛查）

先入账，收到 KYT 高风险结果后再冻结。

- **优点**：用户体验好（即时到账）
- **缺点**：
  - 用户已入账的资金可能需要冻结/回滚，法律和产品层面极其复杂
  - 高风险资金进入热钱包归集后，合规层面压力巨大
  - 不符合先审后放的合规最佳实践
- **否决原因**：合规风险不可接受——"黑钱归集进平台热钱包的话，合规层面会面临非常大的压力"

## Consequences

### Positive

- **5s 内响应**：webhook 同步阶段只做入库，确保不超时
- **幂等**：safeheron_tx_key UNIQUE 约束 + 终态保护 → 重复 webhook 不会重复入账
- **合规前置**：KYT 筛查在入账之前，高风险资金不会进入用户账户
- **可观察**：safeheron_webhook_events 保留原始 payload，便于排查和审计
- **容错**：worker 崩溃重启后，未处理事件自动恢复（processed=false）
- **简单运维**：无外部消息队列依赖，单进程内嵌 worker goroutine

### Negative

- **轮询开销**：Worker 1s 轮询 DB，空闲时有少量 SELECT 开销（当前规模可忽略）
- **延迟**：充值到账有 1-2s 延迟（worker 轮询间隔），KYT 筛查额外增加数秒
- **单点**：Worker 运行在单个 Go 进程中，进程挂掉期间不处理事件（systemd 自动重启兜底）
- **KYT 配额消耗**：每笔充值消耗 1 次 KYT 查询配额（超时兜底可能 +1 次）

## Key Design Decisions

### D-1: Deposit 状态机设计

7 个状态，清晰的状态转换路径：
- `PENDING` → `CHAIN_VERIFYING` → `CHAIN_VERIFIED` → `KYT_PENDING` → `CREDITED`
- 任何非终态 → `MANUAL_REVIEW` / `FAILED`
- `CREDITED` / `FAILED` 为终态，不可覆写

**原因**：状态机是充值流水线的核心不变量，清晰的状态定义避免边界条件混乱。

### D-2: KYT_ENABLED 生产环境强制

`APP_ENV=production && KYT_ENABLED=false` 时启动 panic。

**原因**：防止运维误配导致合规筛查被绕过。测试环境可关闭以便开发调试。

### D-3: 余额非负 CHECK 约束

`account` 表添加 `ck_balance_non_negative` + `ck_frozen_non_negative` 数据库级约束。

**原因**：即使代码有 bug，数据库层面也不允许余额为负，作为最后一道防线。

### D-4: Webhook IP 白名单

在 body 读取和 RSA 验签之前校验来源 IP。

**原因**：CPU 资源保护——RSA 验签是计算密集操作，先做轻量级 IP 检查可以廉价地拒绝非法请求。

## Related

- **Spec**: `docs/spec/safeheron-phase1-spec.md`（§7 充值流水线、§8 KYT 合规筛查）
- **Plan**: `docs/plans/safeheron-phase1-plan.md`
- **ADR-001**: 2FA Architecture（同项目，不同模块）
- **T11 安全加固**: 终态保护 + IP 白名单 + KYT 环境校验（commit `6d33af3`）
