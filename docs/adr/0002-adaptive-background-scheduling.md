# ADR 0002: MD 自适应后台调度（进程内信号 + 清空队列 + 空闲退避）

## Status

Accepted — 2026-07-22

## Context

测试环境启用公司资金流水、路由、估值和告警后，Monera Digital 多个后台任务使用固定秒级或分钟级轮询持续访问 PostgreSQL。空闲窗口内仍产生约 10.2 次数据库事务/秒，并保持多个连接，使 Neon Compute 难以挂起，抬高 stage/production 的 CU-hours。

约束：

- 不新增 MGT → MD 内部唤醒 HTTP 接口
- 不引入 SQS / Redis / Kafka 等外部消息队列
- 不改变公司资金 schema、幂等、租约、重试、风险与告警语义
- PostgreSQL 仍是任务与处理状态的唯一事实来源
- stage 与 production 使用同一运行模型

## Decision

引入统一的进程内后台协调能力（`internal/adaptiveschedule`），作为各后台任务的可测试调度缝：

1. **启动立即扫描**：进程启动后立刻执行一次恢复扫描。
2. **进程内唤醒（可合并）**：Webhook 持久化成功或上游事务提交并产生下游工作后，对本进程发出 coalescible wake。唤醒只优化时效，不参与正确性。
3. **有工作则连续排空**：单次唤醒/扫描在配置的 drain limit 内连续处理，不等待固定周期。
4. **空闲逐级退避**：队列为空后空闲间隔从 `MinIdle` 倍增，直至 `MaxIdle`（默认 **10 分钟**，环境可配）。
5. **到期调度可打断退避**：业务 `next-attempt` / 超时截止时间可通过 `NextDue` 把下一次扫描拉到业务到期点之前。
6. **错误隔离**：单次 cycle 的错误或 panic 不终止其他任务运行时。

Provider Event 处理链是第一条 tracer bullet：验证「持久化后唤醒 → 排空 → 退避 → 启动/最大间隔恢复」闭环，再把充值、路由、告警、估值、地址池等迁移到同一模型。

## Alternatives considered

### 1. MGT/外部服务回调 MD 唤醒接口

拒绝。扩大跨服务攻击面与运维面；与「不新增内部唤醒 API」约束冲突。MGT 直写 PostgreSQL 的任务依赖启动扫描与最大空闲扫描发现。

### 2. SQS / Redis Stream / 外部队列

拒绝。增加基础设施与一致性模型；本阶段目标是降低 Neon 空闲占用，而非引入第二套队列真相源。PostgreSQL 租约与 next-attempt 已足够。

### 3. 保持固定高频轮询（秒级 / 分钟级）

拒绝。这是当前 CU-hours 问题的直接原因。即使只改最明显的 1s worker，其它分钟级 DB 任务仍会阻止 Neon 挂起。

### 4. 仅拉长固定间隔（例如一律 10 分钟）

拒绝。会把 Webhook 与 MD 内部下游任务的正常时效一并拉到分钟级，违反「正常流水约 2 秒内开始处理」目标。需要 **事件驱动 + 空闲退避**，而不是单一慢轮询。

## Consequences

- Webhook 与 MD 内部产生的任务：持久化提交后本地 wake，目标约 2 秒内开始处理。
- MGT 在 MD 深度空闲时写入的任务：最坏约 `MaxIdle`（默认 10 分钟）被发现。
- 丢失或合并进程内信号不会丢任务；启动扫描与最大空闲扫描是耐久回退。
- 日志仅允许任务类型、结果、延迟、安全计数；禁止 Provider Payload、密钥、数据库连接串。
- 第一阶段默认 10 分钟；至少 24 小时 stage 观测后再决定是否配置到 30 分钟（不在本 ADR 内直接改默认值）。

## Related

- Spec: GitHub issue #46
- Implementation tracer: issue #47
- Follow-ons: #48–#53（各链路迁移与防回归守卫）
