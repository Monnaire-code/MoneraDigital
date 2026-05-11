# Scripts Archive

历史一次性脚本归档目录。

**归档时间**：2026-05-11
**归档原因**：接手项目后梳理 `scripts/`，发现混杂着活的启动/构建/部署脚本和一堆一次性 SQL 修复 / 测试驱动 / 验证脚本。在进入 Safeheron Phase 1 实施前做最小归档动作，把"过去式"脚本统一收纳，让根目录只剩活脚本。

**追溯历史**：所有文件均使用 `git mv` 移动，原始改动历史保留：

```bash
git log --follow scripts/archive/<subdir>/<file>
```

---

## 目录索引

| 子目录 | 数量 | 内容 |
|---|---|---|
| [`sql/`](sql/) | 3 | 一次性 DB 修复 SQL / 包装 Shell |
| [`tests/`](tests/) | 25 | 手写驱动测试脚本（2FA / auth / agent-browser / i18n / wealth / login / regression 等） |
| [`verify/`](verify/) | 7 | 一次性诊断 / 验证 / mock server / TOTP 生成等工具 |
| **合计** | **35** | |

### `sql/`

- `drop_wrong_wallet_table.sql` — 删除错建的 wallet 表
- `update_database.sh` — 一次性 DB 更新包装脚本
- `update_neon_database.sql` — Neon DB 一次性数据修正

### `tests/`

- `test-2fa-qrcode-browser.html` — 手动浏览器验证 2FA 二维码
- `test-auth-api.sh` — Auth API curl 驱动脚本（18KB）
- `test-auth-live.js` — Live 环境 Auth 测试驱动
- `test-backend-api.js` — 后端 API 手动测试驱动
- `test-integration.js` — 集成测试驱动（8.8KB）
- `test-vercel-proxy.js` — Vercel proxy 测试驱动

> 真测试在 `tests/`（vitest）+ `tests/*.spec.ts`（playwright），这些是开发期手写的临时驱动。

### `verify/`

- `verify-2fa-qrcode-fix.mjs` — 2FA QR 修复后验证（9.3KB）
- `verify-security-i18n.cjs` — Security i18n 验证
- `clear-cache-snippet.js` — 浏览器缓存清理片段
- `diagnose-integration.js` — Replit 集成诊断工具（6.9KB）

---

## 保留在 scripts/ 根的活脚本

| 文件 | 引用方 / 用途 |
|------|------|
| `start.sh` | `.replit` + `replit.md` 启动入口 |
| `start-replit.sh` | `.replit` Replit 启动 |
| `start-dev.sh` | `tests/dev-environment.test.ts` 断言存在；开发启动 |
| `start-frontend.sh` | `tests/dev-environment.test.ts` 断言存在；前端启动 |
| `start-backend.sh` | 开发后端启动（连 dev DB） |
| `start-prod.sh` | 生产启动（GIN_MODE=release + ./server） |
| `build.sh` | 完整 build（npm install 等） |
| `build-backend-only.sh` | `.replit` 部署构建钩子 |
| `deploy.sh` | `src/__tests__/ci-cd/docker-config.test.ts` 引用 |
| `deploy-vercel.sh` | Vercel 部署 |
| `generate-favicon.js` | `package.json` `npm run favicon` |

---

## 未归档但需关注

- `scripts/cleanup.go` —— 含**明文 Neon 生产 DB 连接串**（已 commit 到 git 历史）。等用户跟团队对齐 + 轮换 Neon 密码后再删除。详见 memory `neondb_password_rotation_todo.md`。
