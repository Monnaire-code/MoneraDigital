<!--
  SECURITY NOTICE — Redaction applied 2026-06-05 per audit C-1.
  This document previously embedded the production Neon database
  connection string (owner role + password + host). Those values have
  been redacted because the password was rotated in response to
  historical exposure in git history and source commits.

  If you need the live values, retrieve them from your local .env
  (DATABASE_URL) or the deployment secret store. Do NOT re-introduce
  the literal values into this or any other tracked file — they will
  re-leak the new password on the next commit.
  See docs/security/ROTATION_RUNBOOK.md for the full rotation procedure.
-->

# 🚨 Neon 数据库问题诊断与修复计划

**诊断日期**: 2026-01-09
**问题**: `users` 表实际不存在于 Neon 数据库
**状态**: ⚠️ 关键问题，需立即修复

---

## 一、问题确认

### 错误日志
```
Failed query: SELECT * FROM users WHERE email = 'gyc567@gmail.com'
Query can't be composed effectively.
The table "users" does not exist in the provided database schema.
```

### 根本原因
✅ **Drizzle Schema 已定义** (src/db/schema.ts)
❌ **Drizzle 迁移未执行** (drizzle/ 目录为空)
❌ **users 表未创建到 Neon 数据库** (实际数据库中不存在)

---

## 二、数据库现状分析

### 2.1 Drizzle 层面

| 组件 | 状态 | 说明 |
|------|------|------|
| **Schema 定义** | ✅ 完成 | `src/db/schema.ts` 中定义了 5 个表 |
| **配置文件** | ✅ 完成 | `drizzle.config.ts` 已配置 |
| **迁移生成** | ❌ 未执行 | `npx drizzle-kit generate:pg` 未运行 |
| **迁移推送** | ❌ 未执行 | `npx drizzle-kit push:pg` 未运行 |
| **drizzle/ 目录** | ❌ 空或缺失 | 没有生成的迁移文件 |

### 2.2 Neon 数据库层面

| 内容 | 状态 | 说明 |
|------|------|------|
| **连接配置** | ✅ 完成 | DATABASE_URL 在 .env 中 |
| **连接测试** | ❓ 未知 | 需要验证 SSL 连接是否正常 |
| **users 表** | ❌ 不存在 | 错误信息明确：table does not exist |
| **其他表** | ❌ 未确认 | 可能也未创建 |
| **迁移历史** | ❌ 无 | 没有任何迁移记录 |

### 2.3 项目结构现状

```
MoneraDigital/
├── src/
│   ├── db/
│   │   └── schema.ts          ✅ Schema 定义完成
│   ├── lib/
│   │   ├── db.ts              ✅ 连接配置完成
│   │   ├── auth-service.ts    ✅ 代码完成，但依赖 users 表
│   │   └── ...
│   └── ...
├── api/
│   ├── auth/
│   │   ├── register.ts        ✅ 代码完成，但依赖 users 表
│   │   ├── login.ts           ✅ 代码完成，但依赖 users 表
│   │   └── ...
├── drizzle/                   ❌ 空目录或不存在（迁移文件缺失）
├── drizzle.config.ts          ✅ 配置完成
├── cmd/
│   ├── db_migration/
│   │   └── main.go            ✅ Go 迁移工具完成
│   └── server/
│       └── main.go            ⏳ 框架完成，实现不完整
├── docs/
│   └── 静态理财/
│       └── 需求文档MD/
│           └── 数据库建表脚本.sql  ✅ 22 个完整表定义
└── .env                       ✅ DATABASE_URL 配置完成
```

---

## 三、修复方案

### 方案 A：使用 Drizzle ORM 迁移（推荐，快速）

**优点**：
- 使用官方工具，安全可靠
- 自动生成迁移文件
- 易于版本控制

**步骤**：

#### 第 1 步：生成迁移
```bash
cd /Users/eric/dreame/code/MoneraDigital

# 生成 Drizzle 迁移文件
npx drizzle-kit generate:pg

# 预期输出：
# Creating table `users`
# Creating table `lendingPositions`
# Creating table `withdrawalAddresses`
# Creating table `addressVerifications`
# Creating table `withdrawals`
#
# ✓ Drizzle migration generated successfully! (/drizzle/...)
```

**检查生成的迁移文件**：
```bash
ls -la drizzle/
# 应该看到：
# - drizzle/meta/ (元数据)
# - drizzle/0001_initial_schema.sql (或类似名称的迁移文件)
```

#### 第 2 步：推送迁移到 Neon
```bash
# 推送迁移到 Neon 数据库
npx drizzle-kit push:pg

# 预期输出：
# 🚀 Applying migrations...
# ✓ Successfully applied migrations
#
# Tables created in neondb:
# - users
# - lendingPositions
# - withdrawalAddresses
# - addressVerifications
# - withdrawals
```

#### 第 3 步：验证表创建成功
```bash
# 方式 1：使用 Drizzle 检查
npx drizzle-kit introspect:pg

# 方式 2：直接连接 Neon 数据库检查
psql "postgresql://[REDACTED-DB-USER]:[REDACTED-DB-PASSWORD]@[REDACTED-NEON-HOST]/neondb?sslmode=require"

# 在 psql 中执行：
\dt              # 列出所有表
\d users         # 查看 users 表结构
SELECT COUNT(*) FROM pg_tables WHERE schemaname = 'public';  # 统计表数量
```

#### 第 4 步：测试认证系统
```bash
# 运行认证测试
npm test -- src/lib/auth-service.test.ts

# 预期结果：所有测试通过
```

---

### 方案 B：使用 Go 迁移工具（完整业务表）

**优点**：
- 一次性创建所有 22 个业务表
- 包含完整的财富管理系统表定义
- 执行 SQL 脚本确保完整性

**步骤**：

#### 第 1 步：编译 Go 迁移工具
```bash
cd /Users/eric/dreame/code/MoneraDigital

# 检查 Go 迁移工具
cat cmd/db_migration/main.go

# 编译为二进制（可选）
cd cmd/db_migration && go build -o db_migration
```

#### 第 2 步：执行迁移
```bash
# 直接运行 Go 迁移工具
go run cmd/db_migration/main.go

# 预期输出：
# Connecting to Neon PostgreSQL...
# Reading SQL script from: docs/静态理财/需求文档MD/数据库建表脚本.sql
# Executing migrations...
# ✓ Successfully created all tables
```

#### 第 3 步：验证所有 22 个表
```bash
# 连接 Neon 检查
psql "postgresql://[REDACTED-DB-USER]:[REDACTED-DB-PASSWORD]@[REDACTED-NEON-HOST]/neondb?sslmode=require"

# 在 psql 中执行：
SELECT COUNT(*) FROM pg_tables WHERE schemaname = 'public';
# 应该返回：27 (22 个表 + 5 个其他系统表)

\dt
# 列出所有表
```

---

## 四、推荐修复步骤（组合方案）

### ✅ 推荐：先用 Drizzle，后用 Go（最安全）

这样既能快速修复认证系统，又能完整部署业务表。

#### 第 1 天：快速修复认证系统（1 小时）
```bash
cd /Users/eric/dreame/code/MoneraDigital

# Step 1: 生成 Drizzle 迁移
npx drizzle-kit generate:pg

# Step 2: 推送到 Neon
npx drizzle-kit push:pg

# Step 3: 验证
npm test -- src/lib/auth-service.test.ts

# Step 4: 测试注册和登陆
curl -X POST http://localhost:3000/api/auth/register \
  -H "Content-Type: application/json" \
  -d '{"email":"test@example.com","password":"password123"}'
```

**预期结果**：
- ✅ users 表已创建
- ✅ 注册成功，返回 user id
- ✅ 登陆成功，返回 JWT token

#### 第 2 天：部署完整业务系统（1 小时）
```bash
# 执行 Go 迁移（创建剩余 22 个表）
go run cmd/db_migration/main.go

# 验证
psql "postgresql://[REDACTED-DB-USER]:[REDACTED-DB-PASSWORD]@[REDACTED-NEON-HOST]/neondb?sslmode=require"

# 在 psql：
\dt  # 应该看到 27 个表
SELECT COUNT(*) FROM pg_tables WHERE schemaname = 'public';
# 返回：27
```

**预期结果**：
- ✅ 所有 22 个业务表已创建
- ✅ 完整的财富管理系统就位

---

## 五、故障排查

### 问题 1：执行 `npx drizzle-kit generate:pg` 时失败

**错误信息**：
```
Error: DATABASE_URL is not set
```

**解决**：
```bash
# 检查 .env 文件
cat .env | grep DATABASE_URL

# 或手动设置
export DATABASE_URL="postgresql://[REDACTED-DB-USER]:[REDACTED-DB-PASSWORD]@[REDACTED-NEON-HOST]/neondb?sslmode=require"

# 再试一次
npx drizzle-kit generate:pg
```

### 问题 2：Neon 连接失败

**错误信息**：
```
Error: connect ECONNREFUSED or SSL error
```

**解决**：
```bash
# 测试连接
psql "postgresql://[REDACTED-DB-USER]:[REDACTED-DB-PASSWORD]@[REDACTED-NEON-HOST]/neondb?sslmode=require"

# 如果 psql 不可用，使用 Node.js 测试
node -e "
const postgres = require('postgres');
const sql = postgres(process.env.DATABASE_URL || '', { ssl: 'require' });
sql\`SELECT 1\`.then(() => console.log('✓ Connected')).catch(e => console.error('✗ Error:', e.message));
"
```

### 问题 3：drizzle/ 目录为空

**原因**：迁移文件未生成或被删除

**解决**：
```bash
# 强制重新生成
rm -rf drizzle/
npx drizzle-kit generate:pg

# 确保文件存在
ls -la drizzle/
```

### 问题 4：迁移推送失败（冲突）

**错误信息**：
```
Error: Migration already applied
```

**解决**：
```bash
# 检查 drizzle 元数据
cat drizzle/meta/_journal.json

# 如果需要重置（危险！），清空元数据
rm -rf drizzle/meta/
rm drizzle/*.sql

# 重新生成
npx drizzle-kit generate:pg
npx drizzle-kit push:pg
```

---

## 六、验证清单

### ✅ 认证系统验证

- [ ] 运行 `npm test -- src/lib/auth-service.test.ts`
- [ ] 所有测试通过
- [ ] 注册端点可用：`POST /api/auth/register`
- [ ] 登陆端点可用：`POST /api/auth/login`
- [ ] JWT token 可正常验证

### ✅ 数据库验证

- [ ] `npx drizzle-kit introspect:pg` 能成功执行
- [ ] users 表存在
- [ ] lendingPositions 表存在
- [ ] withdrawalAddresses 表存在
- [ ] 表字段与 schema.ts 完全匹配

### ✅ Neon 数据库验证

- [ ] 连接成功：`psql` 登陆正常
- [ ] 所有表可查询：`\dt` 显示表列表
- [ ] 可执行 SQL：`SELECT * FROM users` 成功（即使无数据）
- [ ] 索引和约束正确应用

---

## 七、恢复计划

如果迁移出现问题，恢复步骤：

### 方案 1：Drizzle 回滚
```bash
# Drizzle 暂不支持回滚，但可以手动重置
# （仅用于开发环境！）

# 1. 删除所有表（危险）
psql "..." -c "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"

# 2. 清除 drizzle 迁移记录
rm -rf drizzle/meta/

# 3. 重新生成迁移
npx drizzle-kit generate:pg

# 4. 重新推送
npx drizzle-kit push:pg
```

### 方案 2：使用 Neon 控制台回滚
```
1. 访问 Neon 控制台 (https://console.neon.tech)
2. 找到对应的数据库
3. 查看 "Branches" 或 "Reset" 功能
4. 恢复到上一个已知好的状态
```

---

## 八、后续改进

### 短期（本周）
- [x] 修复 users 表创建
- [ ] 验证所有 5 个 Drizzle 表
- [ ] 运行认证系统完整测试
- [ ] 部署 22 个业务表

### 中期（本月）
- [ ] 实现理财账户系统
- [ ] 实现提现功能（冻结机制）
- [ ] 实现对账和监控系统
- [ ] 编写集成测试

### 长期（本季）
- [ ] 完成 Go 后端实现
- [ ] 迁移所有 API 到 Go 后端
- [ ] 性能优化和压力测试
- [ ] 生产环境部署

---

## 九、命令速查表

### 快速修复（复制粘贴）

```bash
#!/bin/bash
cd /Users/eric/dreame/code/MoneraDigital

# 1. 生成迁移
npx drizzle-kit generate:pg

# 2. 推送迁移
npx drizzle-kit push:pg

# 3. 验证
npx drizzle-kit introspect:pg

# 4. 测试认证
npm test -- src/lib/auth-service.test.ts

# 5. 部署业务表
go run cmd/db_migration/main.go

# 6. 最终验证
psql "postgresql://[REDACTED-DB-USER]:[REDACTED-DB-PASSWORD]@[REDACTED-NEON-HOST]/neondb?sslmode=require" -c "\dt"
```

---

## 总结

| 步骤 | 操作 | 时间 | 预期结果 |
|------|------|------|---------|
| **1** | `npx drizzle-kit generate:pg` | 30秒 | 生成迁移文件 |
| **2** | `npx drizzle-kit push:pg` | 1分钟 | 创建 5 个表 |
| **3** | `npm test` | 1分钟 | 认证系统测试通过 |
| **4** | `go run cmd/db_migration/main.go` | 1分钟 | 创建 22 个业务表 |
| **5** | 验证表结构 | 1分钟 | 所有表可用 |
| **总计** | | **5 分钟** | **完整数据库就位** |

---

**问题严重度**: 🔴 **关键（无法登陆）**
**修复难度**: 🟢 **简单（5 分钟）**
**风险等级**: 🟢 **低（只是创建表）**

**建议**: 立即执行方案 A（Drizzle 迁移），然后执行方案 B（Go 完整表）。
