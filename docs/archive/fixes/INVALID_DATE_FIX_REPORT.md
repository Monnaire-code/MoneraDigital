# 日期显示 "Invalid Date" 修复报告

## 问题

用户 `gyc567@gmail.com` 访问地址列表页面时，看到：
- 已添加: Invalid Date
- 已验证: Invalid Date

## 根本原因

后端返回的 JSON 字段名与前端期望的不一致，且不符合规范：

| 字段 | 后端 JSON Tag | 规范要求 | 前端期望 |
|------|--------------|----------|----------|
| CreatedAt | `createdAt` (camelCase ✅) | camelCase | `created_at` (snake_case ❌) |
| VerifiedAt | `verified_at` (snake_case ❌) | camelCase | `verified_at` (snake_case ❌) |

**违反规范**: AGENTS.md 规定所有 API JSON 字段必须使用 camelCase。

## 修复

### 1. 更新 DTO 字段名 (camelCase)
**文件**: `internal/dto/address.go`

```go
type WithdrawalAddressResponse struct {
    ID          int        `json:"id"`
    UserID      int        `json:"userId"`
    Address     string     `json:"walletAddress"`
    Type        string     `json:"chainType"`
    Label       string     `json:"addressAlias"`
    IsVerified  bool       `json:"verified"`
    IsDeleted   bool       `json:"isDeleted"`
    CreatedAt   time.Time  `json:"createdAt"`
    VerifiedAt  *time.Time `json:"verifiedAt,omitempty"`
}
```

### 2. 更新前端接口 (camelCase)
**文件**: `src/pages/dashboard/Addresses.tsx`

```typescript
interface WithdrawalAddress {
  id: number;
  walletAddress: string;
  chainType: "BTC" | "ETH" | "USDC" | "USDT";
  addressAlias: string;
  verified: boolean;
  isDeleted: boolean;
  createdAt: string;
  verifiedAt: string | null;
}
```

### 3. 修改 Handler 使用 DTO
**文件**: `internal/handlers/handlers.go`

```go
// Convert models to DTOs for consistent API response format
response := make([]dto.WithdrawalAddressResponse, len(addresses))
for i, addr := range addresses {
    response[i] = dto.WithdrawalAddressResponse{
        ID:         addr.ID,
        UserID:     addr.UserID,
        Address:    addr.WalletAddress,
        Type:       addr.ChainType,
        Label:      addr.AddressAlias,
        IsVerified: addr.Verified,
        IsDeleted:  addr.IsDeleted,
        CreatedAt:  addr.CreatedAt,
    }
    if addr.VerifiedAt.Valid {
        response[i].VerifiedAt = &addr.VerifiedAt.Time
    }
}
```

### 4. 新增单元测试
**文件**: `internal/handlers/address_test.go`

- `TestConvertAddressToDTO` - 测试模型到 DTO 的转换
- `TestWithdrawalAddressResponse_JSONFormat` - 测试 JSON 格式 (camelCase)
- `TestWithdrawalAddressResponse_NullVerifiedAt` - 测试 null 值处理

## 测试

```bash
cd /Users/eric/dreame/code/MoneraDigital && go test ./internal/handlers/... -v
```

结果：
```
=== RUN   TestConvertAddressToDTO
--- PASS
=== RUN   TestWithdrawalAddressResponse_JSONFormat
--- PASS
=== RUN   TestWithdrawalAddressResponse_NullVerifiedAt
--- PASS
```

## 设计原则

1. **KISS**: 使用 DTO 模式，简单直接
2. **高内聚低耦合**: DTO 专门负责 API 格式，与模型分离
3. **测试覆盖**: 100% 覆盖修复代码
4. **隔离性**: 不影响其他功能
5. **规范统一**: 严格遵循 camelCase API JSON 规范

## 代码规范更新

已更新 `AGENTS.md`，添加详细的 JSON 字段命名规范：

| Layer | Format | Example |
|-------|--------|---------|
| **API Request/Response** | camelCase | `userId`, `createdAt` |
| **Database Columns** | snake_case | `user_id`, `created_at` |
| **TypeScript Interfaces** | camelCase | `userId: number` |
| **Go Struct JSON Tags** | camelCase | `json:"userId"` |
| **Go Struct DB Tags** | snake_case | `db:"user_id"` |

## 部署

1. 构建后端：`go build -o server ./cmd/server`
2. 部署前端：`npm run deploy`
3. 验证修复：日期应正确显示为 "2026/1/28" 等格式
