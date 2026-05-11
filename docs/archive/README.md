# Docs Archive

历史 fix/test/proposal/report 类文档归档目录。

**归档时间**：2026-05-11
**归档原因**：根目录原本堆积 90+ 一次性历史报告，淹没了真正重要的入口文档（README/CLAUDE/AGENTS 等）。在进入 Safeheron Phase 1 实施前做最小归档动作，把"过去式"文档统一收纳，不再污染根目录。

**追溯历史**：所有文件均使用 `git mv` 移动，原始改动历史保留：

```bash
git log --follow docs/archive/<subdir>/<file>.md
```

---

## 目录索引

| 子目录 | 数量 | 内容主题 |
|--------|------|---------|
| [`2fa/`](2fa/) | 24 | 2FA 功能历次修复、QR 码、OTPAuth、Skip 流程、测试报告 |
| [`auth/`](auth/) | 8 | 登录 401/405/500 错误修复、地址 401 修复 |
| [`deployment/`](deployment/) | 6 | Vercel/Replit 部署历次修复与指南 |
| [`tests/`](tests/) | 22 | 各模块 E2E/集成测试报告（Core Account/Deposit/Lending/Withdrawal/Wealth/I18n 等） |
| [`fixes/`](fixes/) | 6 | 单点 bug 修复（500/404/Invalid Date/currency-format 等） |
| [`encryption/`](encryption/) | 2 | 加密改动验证 |
| [`i18n/`](i18n/) | 3 | JSON 命名约定统一（snake_case → camelCase）相关修复 |
| [`architecture/`](architecture/) | 8 | 架构统一（Unified API Router/Serverless Function）、代码优化、schema 更新 |
| [`usdc/`](usdc/) | 2 | USDC_BEP20 Core API 长格式相关排查 |
| [`agent-browser/`](agent-browser/) | 3 | Agent Browser 接入与测试 |
| [`misc/`](misc/) | 7 | 杂项（documentation/findings/progress/task_plan/前端安全审计/数据库修复 README） |

**合计**：91 个文件

---

## 保留在根目录的活文档

| 文件 | 用途 |
|------|------|
| `README.md` | 项目入口 |
| `CLAUDE.md` | Claude Code 工作规范 |
| `AGENTS.md` | 多 Agent 协作约定 |
| `GEMINI.md` | Gemini 工作规范 |
| `replit.md` | Replit 部署说明 |

---

## 新文档去向（非归档）

| 类型 | 路径 |
|------|------|
| 当前 Safeheron Phase 1 SPEC | `docs/spec/safeheron-phase1-spec.md` |
| 当前实施计划 | `tasks/plan.md` |
| 当前任务清单 | `tasks/todo.md` |
| API 文档 | `docs/static-finance-api*.md`, `docs/openapi.yaml`, `docs/safeheron-wallet-openapi.yaml` |
| PRD | `docs/static-finance-prd*.md`, `tasks/prd-*.md` |
| 架构审计 | `docs/ARCHITECT-AUDIT-REPORT*.md`, `docs/audit/` |
