# 数据库 Schema 更新报告

**日期**: 2026-01-28  
**文件**: `drizzle/monera_complete_schema.sql`  
**状态**: ✅ 已更新

---

## 更新摘要

统一更新了 `monera_complete_schema.sql` 文件，添加了所有 2FA 相关字段和 wallet_creation_request 表的扩展字段。

---

## 具体变更

### 1. Users 表 - 2FA 字段完善

**位置**: 第 24-37 行

**添加的字段**:
```sql
two_factor_last_used_at BIGINT DEFAULT 0,
updated_at TIMESTAMP DEFAULT NOW() NOT NULL
```

**添加的索引**:
```sql
CREATE INDEX IF NOT EXISTS idx_users_two_factor_last_used_at 
ON users(two_factor_last_used_at) 
WHERE two_factor_enabled = TRUE;
```

**完整的 users 表结构**:
```sql
CREATE TABLE IF NOT EXISTS users (
  id SERIAL PRIMARY KEY,
  email TEXT NOT NULL UNIQUE,
  password TEXT NOT NULL,
  two_factor_secret TEXT,                          -- 加密 TOTP 密钥
  two_factor_enabled BOOLEAN DEFAULT FALSE NOT NULL, -- 2FA 启用状态
  two_factor_backup_codes TEXT,                    -- 加密备用验证码
  two_factor_last_used_at BIGINT DEFAULT 0,        -- 最后使用时间戳（防重放攻击）
  created_at TIMESTAMP DEFAULT NOW() NOT NULL,
  updated_at TIMESTAMP DEFAULT NOW() NOT NULL
);
```

### 2. Wallet Creation Request 表 - 扩展字段

**位置**: 第 251-269 行

**添加的字段**:
```sql
product_code VARCHAR(50) DEFAULT '',
currency VARCHAR(20) DEFAULT ''
```

**添加的索引**:
```sql
CREATE UNIQUE INDEX IF NOT EXISTS idx_wallet_requests_user_product_currency 
ON wallet_creation_request(user_id, product_code, currency) 
WHERE status = 'SUCCESS';
```

**完整的 wallet_creation_request 表结构**:
```sql
CREATE TABLE IF NOT EXISTS wallet_creation_request (
  id BIGSERIAL PRIMARY KEY,
  user_id BIGINT NOT NULL,
  request_id TEXT NOT NULL,
  status TEXT DEFAULT 'PENDING' NOT NULL,
  safeheron_wallet_id TEXT,
  coin_address TEXT,
  error_message TEXT,
  retry_count INTEGER DEFAULT 0 NOT NULL,
  product_code VARCHAR(50) DEFAULT '',             -- 产品代码（如 SPOT, EARN）
  currency VARCHAR(20) DEFAULT '',                 -- 币种代码（如 USDT, BTC）
  created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL
);
```

### 3. 注释更新

**位置**: 第 538-548 行, 第 564-566 行

**添加的注释**:
```sql
COMMENT ON TABLE users IS 'Core user authentication table with 2FA support';
COMMENT ON COLUMN users.two_factor_secret IS 'Encrypted TOTP secret key';
COMMENT ON COLUMN users.two_factor_enabled IS 'Whether 2FA is enabled for the user';
COMMENT ON COLUMN users.two_factor_backup_codes IS 'Encrypted backup codes for 2FA recovery';
COMMENT ON COLUMN users.two_factor_last_used_at IS 'Timestamp of last 2FA usage for replay attack prevention';

COMMENT ON TABLE wallet_creation_request IS 'Wallet creation requests with product and currency support';
COMMENT ON COLUMN wallet_creation_request.product_code IS 'Product code for the wallet (e.g., SPOT, EARN)';
COMMENT ON COLUMN wallet_creation_request.currency IS 'Currency code for the wallet (e.g., USDT, BTC)';
```

---

## 与 Migration 的对应关系

| Migration | Schema 文件位置 | 状态 |
|-----------|----------------|------|
| 001 - CreateUsersTable | 第 24-37 行 | ✅ 已同步 |
| 004 - AddTwoFactorColumnsMigration | 第 28-31 行 | ✅ 已同步 |
| 005 - AddTwoFactorTimestampMigration | 第 31, 37 行 | ✅ 已同步 |
| 007 - UpdateWalletRequestsTable | 第 260-261, 269 行 | ✅ 已同步 |

---

## 验证检查

### 2FA 相关字段
```bash
$ grep "two_factor" drizzle/monera_complete_schema.sql
  two_factor_secret TEXT,
  two_factor_enabled BOOLEAN DEFAULT FALSE NOT NULL,
  two_factor_backup_codes TEXT,
  two_factor_last_used_at BIGINT DEFAULT 0,
CREATE INDEX IF NOT EXISTS idx_users_two_factor_last_used_at ON users(two_factor_last_used_at) WHERE two_factor_enabled = TRUE;
COMMENT ON COLUMN users.two_factor_secret IS 'Encrypted TOTP secret key';
COMMENT ON COLUMN users.two_factor_enabled IS 'Whether 2FA is enabled for the user';
COMMENT ON COLUMN users.two_factor_backup_codes IS 'Encrypted backup codes for 2FA recovery';
COMMENT ON COLUMN users.two_factor_last_used_at IS 'Timestamp of last 2FA usage for replay attack prevention';
```

### Wallet Creation Request 扩展字段
```bash
$ grep "product_code\|currency" drizzle/monera_complete_schema.sql
  product_code VARCHAR(50) DEFAULT '',
  currency VARCHAR(20) DEFAULT '',
CREATE UNIQUE INDEX IF NOT EXISTS idx_wallet_requests_user_product_currency ON wallet_creation_request(user_id, product_code, currency) WHERE status = 'SUCCESS';
COMMENT ON COLUMN wallet_creation_request.product_code IS 'Product code for the wallet (e.g., SPOT, EARN)';
COMMENT ON COLUMN wallet_creation_request.currency IS 'Currency code for the wallet (e.g., USDT, BTC)';
```

---

## 使用说明

### 初始化数据库
```bash
# 使用 psql 执行 schema 文件
psql -d your_database -f drizzle/monera_complete_schema.sql
```

### 或者使用 Migration 工具
```bash
# 编译并运行 migration
go build -o migrate ./cmd/migrate
./migrate
```

---

## 结论

Schema 文件已成功更新，包含：

1. ✅ Users 表完整的 2FA 字段（4 个字段 + 1 个索引）
2. ✅ Wallet Creation Request 表扩展字段（2 个字段 + 1 个索引）
3. ✅ 所有字段的详细注释
4. ✅ 与 Go migrations 完全同步

**状态**: Schema 文件已准备好用于数据库初始化
