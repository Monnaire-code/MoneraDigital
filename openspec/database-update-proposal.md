<!--
  SECURITY NOTICE — Redaction applied 2026-06-05 per audit C-1.
  This document previously embedded a production Neon database
  connection string (owner role + password + host) and a
  leaked JWT_SECRET / ENCRYPTION_KEY. Those values have been
  redacted because the password was rotated in response to
  historical exposure in git history and source commits.
  See docs/security/ROTATION_RUNBOOK.md.
-->

# Neon 数据库更新方案

## 安全警告

⚠️ **永远不要将生产数据库凭据提交到代码仓库或分享给第三方**

您提供的连接字符串包含敏感信息，建议立即：
1. 轮换数据库密码
2. 使用环境变量管理凭据
3. 限制数据库访问IP

---

## 推荐的更新方式

### 方式一：使用迁移工具（推荐）

```bash
# 1. 设置环境变量
export DATABASE_URL='postgresql://[REDACTED-DB-USER]:xxx@ep-xxx.c-2.us-east-1.aws.neon.tech/neondb?sslmode=require'

# 2. 编译迁移工具
go build -o migrate ./cmd/migrate

# 3. 运行迁移（ dry-run 模式先预览）
./migrate -dry-run

# 4. 执行迁移
./migrate
```

### 方式二：手动执行 SQL

```bash
# 使用 psql 连接并执行
psql "$DATABASE_URL" -f drizzle/monera_complete_schema.sql
```

### 方式三：Neon 控制台

1. 登录 [Neon Console](https://console.neon.tech)
2. 选择项目
3. 使用 SQL Editor 执行更新

---

## 需要执行的 SQL

### 检查当前表结构
```sql
-- 检查 users 表当前字段
\d users

-- 检查 wallet_creation_request 表
\d wallet_creation_request
```

### 添加 2FA 字段（如果不存在）
```sql
-- 添加 two_factor_last_used_at 字段
ALTER TABLE users 
ADD COLUMN IF NOT EXISTS two_factor_last_used_at BIGINT DEFAULT 0;

-- 添加 updated_at 字段
ALTER TABLE users 
ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP DEFAULT NOW() NOT NULL;

-- 创建索引
CREATE INDEX IF NOT EXISTS idx_users_two_factor_last_used_at 
ON users(two_factor_last_used_at) 
WHERE two_factor_enabled = TRUE;
```

### 添加 Wallet Creation Request 字段
```sql
-- 添加 product_code 字段
ALTER TABLE wallet_creation_request 
ADD COLUMN IF NOT EXISTS product_code VARCHAR(50) DEFAULT '';

-- 添加 currency 字段
ALTER TABLE wallet_creation_request 
ADD COLUMN IF NOT EXISTS currency VARCHAR(20) DEFAULT '';

-- 创建唯一索引
CREATE UNIQUE INDEX IF NOT EXISTS idx_wallet_requests_user_product_currency 
ON wallet_creation_request(user_id, product_code, currency) 
WHERE status = 'SUCCESS';
```

---

## 验证步骤

```sql
-- 验证 users 表字段
SELECT column_name, data_type, column_default 
FROM information_schema.columns 
WHERE table_name = 'users' 
ORDER BY ordinal_position;

-- 验证索引
SELECT indexname, indexdef 
FROM pg_indexes 
WHERE tablename = 'users';

-- 验证 wallet_creation_request 字段
SELECT column_name, data_type, column_default 
FROM information_schema.columns 
WHERE table_name = 'wallet_creation_request' 
ORDER BY ordinal_position;
```

---

## 回滚方案

如需回滚：

```sql
-- 回滚 users 表变更
DROP INDEX IF EXISTS idx_users_two_factor_last_used_at;
ALTER TABLE users DROP COLUMN IF EXISTS two_factor_last_used_at;

-- 回滚 wallet_creation_request 变更
DROP INDEX IF EXISTS idx_wallet_requests_user_product_currency;
ALTER TABLE wallet_creation_request DROP COLUMN IF EXISTS product_code;
ALTER TABLE wallet_creation_request DROP COLUMN IF EXISTS currency;
```

---

## 建议操作顺序

1. **备份数据库**（ Neon 自动备份，但建议手动创建快照）
2. **在开发环境测试**
3. **连接到生产数据库验证**
4. **执行迁移**
5. **验证结果**
6. **监控应用日志**

---

## 生成迁移脚本

我已为您生成了可直接使用的迁移脚本：
