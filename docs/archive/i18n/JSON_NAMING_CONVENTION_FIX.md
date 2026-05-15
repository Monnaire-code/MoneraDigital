# JSON 命名规范统一修复

## 问题

项目中 JSON 字段命名不一致：
- 后端使用 `user_id`（下划线命名 - snake_case）
- 前端期望 `userId`（驼峰命名 - camelCase）

## 命名规范决策

**统一使用驼峰命名（camelCase）**：
- `userId` ✅
- `requires2FA` ✅
- `accessToken` ✅
- `refreshToken` ✅

## 需要修复的文件

### 1. DTO 层 (`internal/dto/`)

#### `internal/dto/auth.go`
```go
// 修改前
UserID       int       `json:"user_id,omitempty"`

// 修改后
UserID       int       `json:"userId,omitempty"`
```

#### `internal/dto/lending.go`
```go
// 修改前
UserID       int       `json:"user_id"`

// 修改后
UserID       int       `json:"userId"`
```

#### `internal/dto/address.go`
```go
// 修改前
UserID     int        `json:"user_id"`

// 修改后
UserID     int        `json:"userId"`
```

#### `internal/dto/withdrawal.go`
```go
// 修改前
UserID        int        `json:"user_id"`

// 修改后
UserID        int        `json:"userId"`
```

### 2. 模型层 (`internal/models/`)

#### `internal/models/token.go`
```go
// 修改前
UserID    int    `json:"user_id"`

// 修改后
UserID    int    `json:"userId"`
```

#### `internal/models/models.go`
所有 `json:"user_id"` 改为 `json:"userId"`

### 3. 服务层 (`internal/services/`)

#### `internal/services/auth.go`
已修复：
```go
Requires2FA  bool         `json:"requires2FA,omitempty"`
UserID       int          `json:"userId,omitempty"`
```

### 4. 前端服务层 (`src/lib/`)

#### `src/lib/wallet-service.ts`
```typescript
// 修改前
user_id: z.number().int().positive()

// 修改后
userId: z.number().int().positive()
```

## 命名规则文档

### 规则 1: JSON 字段使用驼峰命名

所有 API 请求和响应的 JSON 字段必须使用驼峰命名（camelCase）：

✅ **正确**:
```json
{
  "userId": 1,
  "accessToken": "xxx",
  "refreshToken": "xxx",
  "requires2FA": true
}
```

❌ **错误**:
```json
{
  "user_id": 1,
  "access_token": "xxx",
  "refresh_token": "xxx",
  "requires_2fa": true
}
```

### 规则 2: 数据库字段使用下划线命名

数据库表和字段使用下划线命名（snake_case）：

```sql
CREATE TABLE users (
    user_id INTEGER PRIMARY KEY,  -- 数据库中使用下划线
    created_at TIMESTAMP
);
```

### 规则 3: Go 结构体字段使用驼峰命名

Go 代码中的结构体字段使用驼峰命名：

```go
type User struct {
    UserID      int       `json:"userId" db:"user_id"`  // JSON 驼峰，DB 下划线
    CreatedAt   time.Time `json:"createdAt" db:"created_at"`
}
```

### 规则 4: TypeScript/JavaScript 使用驼峰命名

前端代码中所有变量使用驼峰命名：

```typescript
const userId = 1;
const accessToken = 'xxx';
const requires2FA = true;
```

## 实施步骤

1. 更新所有 DTO 文件的 JSON tag
2. 更新所有模型文件的 JSON tag
3. 更新前端服务层的 schema 定义
4. 重新构建后端
5. 重新部署前端
6. 运行测试验证

## 验证清单

- [ ] 所有 DTO 使用驼峰命名
- [ ] 所有模型使用驼峰命名
- [ ] 前端代码使用驼峰命名
- [ ] API 测试通过
- [ ] 端到端测试通过
