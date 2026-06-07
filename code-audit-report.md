# 更新代码审计报告

审计对象：当前工作区更新后的基金/AUM 首页展示相关代码。

审计结论：**不建议直接合并**。

本次更新整体符合项目核心架构：前端通过 `/api/fund/stats` 调用统一 Vercel API Router，Go 后端负责业务逻辑和数据库访问。但当前存在会影响生产可靠性、安全边界和公开金融数据一致性的缺陷，需先修复高优先级问题。

---

## 1. 高优先级问题

### 1.1 基金数据不存在时真实后端会返回 500，而不是预期 404

**严重级别：HIGH**

**证据：**

- `internal/repository/postgres/fund_report.go:13` 定义了 `postgres.ErrFundNotFound`。
- `internal/repository/postgres/fund_report.go:38-40` 在 `sql.ErrNoRows` 时返回 repository 层的 `ErrFundNotFound`。
- `internal/services/fund_service.go:14` 又定义了另一个 `services.ErrFundNotFound`。
- `internal/services/fund_service.go:33-36` 直接返回 repository 错误，没有做错误归一化。
- `internal/handlers/fund_handler.go:23-34` 只识别 `services.ErrFundNotFound` 并映射为 404。

**影响：**

当真实数据库中的 `fund_reports` 为空、seed 失败或数据被清理时，`GET /api/fund/stats` 会返回 `500 Failed to load fund statistics`，而不是预期的 `404 No fund report available yet`。

当前测试没有覆盖真实错误路径，因为测试 mock 直接返回的是 `services.ErrFundNotFound`。

**建议修复：**

- 统一 not-found sentinel；或
- 在 `FundService.GetStats` 中将 repository 层 not-found 错误映射为 `services.ErrFundNotFound`；
- 增加 handler/service 测试，覆盖真实 repository not-found 错误传播路径。

---

### 1.2 公共接口全局限流实际窗口为 60 纳秒，基本失效

**严重级别：HIGH**

**证据：**

- `internal/container/container.go:298` 调用：`middleware.NewRateLimiter(5, 60)`。
- `internal/middleware/rate_limit.go:27` 中 `window` 参数类型是 `time.Duration`。
- `internal/middleware/rate_limit.go:56` 使用 `now.Sub(ts) < rl.window` 判断窗口。

**影响：**

`60` 会被 Go 解释为 `60ns`，不是 60 秒。实际流量中请求时间戳几乎立刻过期，导致全局限流基本失效。

由于 `/api/fund/stats` 是公开接口，这会增加被刷请求直接打到 Go 后端和数据库的风险。

**建议修复：**

将调用改为明确的 duration：

```go
middleware.NewRateLimiter(5, 60*time.Second)
```

或者修改 `NewRateLimiter` 签名，让调用方传秒数并在内部转换。应补充单测验证窗口内第 6 次请求被拒绝。

---

## 2. 中优先级问题

### 2.1 首页同一数据源会被请求两次

**严重级别：MEDIUM**

**证据：**

- `src/components/HeroAumCard.tsx:31` 调用 `useFundStats()`。
- `src/components/FundPerformance.tsx:35` 也调用 `useFundStats()`。
- `src/hooks/use-fund-stats.ts:19-36` 每个 hook 实例都会独立执行 fetch。
- `src/pages/Index.tsx:16-17` 同页渲染 `HeroAumCard` 和 `FundPerformance`。

**影响：**

首页每次访问会至少发起两次 `GET /api/fund/stats`。这会增加公开接口负载，并可能出现一个组件成功、另一个组件失败的视觉不一致。

**建议修复：**

在页面级统一拉取基金数据并通过 props 下传，或使用 React Query / shared provider 实现请求去重和缓存。

---

### 2.2 资产配置百分比与金额不完全一致

**严重级别：MEDIUM**

**证据：**

`internal/migration/migrations/016_create_fund_reports.go:154-157` 中 seed 的配置百分比为：

```text
0.26028 + 0.66647 + 0.06750 + 0.00563 = 0.99988
```

但资产金额总和精确等于 May 2026 AUM：

```text
3857328.43 + 9879372.87 + 1000000.00 + 83424.64 = 14820125.94
```

按金额 / AUM 计算，`Proactive Trading` 的比例约为 `0.6666186853`，而不是当前 seed 的 `0.66647`。

**影响：**

公开图表展示的百分比与金额不一致，合计不到 100%。金融产品页面中，这类数字不一致会降低可信度。

**建议修复：**

- 不要手写 `pct`，在 seed/query 中用 `amount / total_aum` 计算；或
- 修正 seed 的百分比；
- 增加测试验证 allocation amount 与 pct 在明确容差内和 `fund_reports.total_aum` 一致。

---

### 2.3 中文页面存在英文日期和美元格式混用

**严重级别：MEDIUM**

**证据：**

- `src/components/HeroAumCard.tsx:22-26` 硬编码英文月份。
- `src/components/HeroAumCard.tsx:94` 中文模式会显示类似 `截至 May 2026`。
- `src/components/FundPerformance.tsx:29-30` 固定使用 `en-US` 和 USD 格式。
- `src/components/FundPerformance.tsx:101` 直接显示原始 `2026-05`。

**影响：**

中文页面会出现中英文混排和不一致的数字/日期格式。

**建议修复：**

使用 `Intl.DateTimeFormat` 和 `Intl.NumberFormat`，基于 `i18n.language` 选择 locale，并在两个组件间复用统一 formatter。

---

### 2.4 图表信息对屏幕阅读器不完整

**严重级别：MEDIUM**

**证据：**

- `src/components/FundPerformance.tsx:120-149` AUM 趋势主要通过 BarChart 展示。
- `src/components/FundPerformance.tsx:156-176` 资产配置主要通过 PieChart 展示。
- `src/components/FundPerformance.tsx:177-193` 文本列表只展示配置百分比，没有完整金额和趋势值。

**影响：**

屏幕阅读器用户无法完整获得趋势和资产配置金额信息，图表内容主要依赖视觉和 tooltip。

**建议修复：**

为趋势和配置增加 visually-hidden table 或文本摘要，并为图表区域添加 `aria-labelledby` / `aria-describedby`。

---

### 2.5 前端 runtime validation 缺少金融数据领域约束

**严重级别：MEDIUM**

**证据：**

- `src/lib/fund-service.ts:40-45` 仅校验 current metrics 是 number。
- `src/lib/fund-service.ts:52` 仅校验 trend AUM 是 number。
- `src/lib/fund-service.ts:59-61` 仅校验 allocation amount/pct 是 number。

**影响：**

类型正确但领域错误的数据会通过校验，例如负 AUM、非法月份、`pct` 超出 0..1、空 trend、未排序 trend、NaN/Infinity 等。这会导致公开页面展示误导性金融指标。

**建议修复：**

在 `parseFundStats` 中增加领域校验：

- 金额必须 finite 且非负；
- `pct` 必须在 `0..1`；
- month/reportDate 必须符合有效 `YYYY-MM`；
- trend/allocation 至少非空；
- allocation 百分比合计应在明确容差范围内；
- 禁止 NaN/Infinity。

---

## 3. 低优先级问题

### 3.1 公开 DTO 直接暴露自由文本 note

**严重级别：LOW**

**证据：**

- `internal/dto/fund.go:15` 暴露 `note`。
- `internal/services/fund_service.go:82` 直接返回数据库中的 `latest.Note`。
- `internal/migration/migrations/016_create_fund_reports.go:67` 将 note 定义为自由 `TEXT`。

**影响：**

当前 seed 内容是公开月报文案，但后续运营人员可能将内部评论、策略细节、对手方信息或草稿写入 note，随后未经审核直接公开。

**建议修复：**

将字段重命名为 `publicNote` / `publicCommentary`，并明确它是可公开内容；或从公开 API 中移除 note。

---

### 3.2 About 区域硬编码 AUM，与动态数据源重复

**严重级别：LOW**

**证据：**

- `src/components/About.tsx:26` 硬编码 `$14.82M`。
- `src/components/HeroAumCard.tsx:54-55` 使用 `/api/fund/stats` 返回的动态 `totalAum`。

**影响：**

当基金报告更新后，Hero 和 FundPerformance 会展示新 AUM，但 About 区域仍显示旧值，造成首页指标不一致。

**建议修复：**

About 区域也使用同一个 fund stats 数据源，或删除该重复 AUM 指标。

---

## 4. 验证结果

### 4.1 TypeScript / API Router 相关测试

执行命令：

```bash
npm test -- src/lib/fund-service.test.ts api/__route__.test.ts
```

结果：**失败**。

通过项：

- `src/lib/fund-service.test.ts`：11 tests 通过。

失败项：

- `api/__route__.test.ts` 中 `should reject invalid address route patterns` 失败。
- 期望非法地址路由返回 404，实际返回 200。
- 位置：`api/__route__.test.ts:752-768`。

该失败不属于本次基金功能主线，但说明当前统一 API Router 的动态地址路由匹配过宽，会影响当前 targeted test suite 的可信度。

---

### 4.2 Go 基金服务测试

执行命令：

```bash
go test ./internal/services -run 'TestFundService_GetStats'
```

结果：**通过**。

---

### 4.3 Go handler/routes 编译验证

执行命令：

```bash
go test ./internal/handlers ./internal/routes -run '^$'
```

结果：**通过**。

---

### 4.4 Go migration package 编译验证

执行命令：

```bash
go test ./internal/migration/migrations -run '^$'
```

结果：**通过**。

---

### 4.5 前端生产构建

执行命令：

```bash
npm run build
```

结果：**通过**。

构建警告：存在大 chunk：

- `vendor-core-CfFDNKn7.js`：713.66 kB
- `vendor-charts-YALyPN-V.js`：275.61 kB

建议后续评估 Recharts 是否应延迟加载或拆分 chunk，避免首页首屏 JS 体积继续增长。

---

## 5. 通过项

- 新 endpoint 没有新增 Vercel Serverless Function，仍使用统一 `api/[...route].ts`。
- 前端服务层只调用本地 API，没有直接访问数据库或 Core API。
- Go 后端负责数据库读取和业务组合，符合项目架构要求。
- API JSON 字段使用 camelCase，符合项目约定。
- SQL 查询为固定 SQL / 参数化参数，未发现 SQL 注入风险。
- `GET /api/fund/stats` 公开暴露本身符合首页 AUM widget 的产品目标。
- 新增 TypeScript parser/service 测试覆盖了成功、404、500、非 JSON 响应和 malformed payload 基础路径。

---

## 6. 建议修复顺序

1. 修复 not-found sentinel 不一致，确保真实空数据返回 404。
2. 修复 `NewRateLimiter(5, 60)` 的 duration bug。
3. 修复 API Router 动态地址路由过宽导致的既有测试失败。
4. 统一首页基金数据请求，避免同页双 fetch。
5. 修正 allocation pct，与金额/AUM 保持一致。
6. 补充前端领域数据校验与组件测试。
7. 完善中文日期/货币格式和图表可访问性。
8. 处理公开 note 字段的命名和发布边界。

---

## 7. 合并建议

当前状态：**BLOCK**。

至少完成以下三项后再考虑合并：

- 真实后端空基金数据返回 404；
- 全局限流窗口修复为秒级 duration；
- `api/__route__.test.ts` 当前失败项恢复通过。
