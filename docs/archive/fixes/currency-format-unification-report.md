# 币种格式统一修改报告

## 修改目标
统一前后端币种格式为短格式：`代币名称_网络`

## 统一后的格式列表

| 币种 | 格式 |
|------|------|
| USDT (ERC20) | **USDT_ERC20** |
| USDT (TRC20) | **USDT_TRC20** |
| USDT (BEP20) | **USDT_BEP20** |
| USDC (ERC20) | **USDC_ERC20** |
| USDC (TRC20) | **USDC_TRC20** |
| USDC (BEP20) | **USDC_BEP20** |

## 修改文件清单

### 1. 后端文件

#### `internal/currency/currency.go`
- 修改所有币种常量为短格式
- 删除 `ShortFormatToFull` 映射（不再需要）
- 更新 `IsValid()` 函数，只检查短格式
- 简化 `ToFullFormat()` 和 `ToShortFormat()` 为向后兼容保留，直接返回原值

**修改前:**
```go
const (
    USDT_BEP20 = "USDT_BEP20_BINANCE_SMART_CHAIN_MAINNET"
    USDC_BEP20 = "USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET"
)
```

**修改后:**
```go
const (
    USDT_BEP20 = "USDT_BEP20"
    USDC_BEP20 = "USDC_BEP20"
)
```

#### `internal/services/wallet.go`
- 已修复 `buildCurrencyKey()` 返回短格式（见之前的修复）

#### `internal/currency/currency_test.go`
- 更新测试用例期望值为短格式
- 修改 `NetworkFromCurrency` 测试，期望 `"BEP20"` 而非 `"BEP20_BINANCE_SMART_CHAIN_MAINNET"`

### 2. 前端文件

#### `src/lib/wallet-service.ts`
- 更新 `SUPPORTED_CURRENCIES` 数组为短格式

**修改前:**
```typescript
export const SUPPORTED_CURRENCIES = [
  'USDT_BEP20_BINANCE_SMART_CHAIN_MAINNET',
  'USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET',
] as const;
```

**修改后:**
```typescript
export const SUPPORTED_CURRENCIES = [
  'USDT_BEP20',
  'USDC_BEP20',
] as const;
```

#### `src/lib/wallet-service.test.ts`
- 更新测试用例中的币种格式为短格式

#### `src/pages/dashboard/AccountOpening.tsx`
- 更新注释说明现在使用统一短格式
- `CURRENCY_OPTIONS` 已经使用短格式，无需修改
- `CHAIN_TO_BACKEND_MAP` 注释更新

## 测试验证

### 后端测试
```bash
go test ./internal/currency/... -v
```
✅ 所有测试通过 (9个测试)

### 前端测试
```bash
npm test -- src/lib/wallet-service.test.ts
```
✅ 所有测试通过 (7个测试)

## 数据流验证

### 新增地址流程
```
前端: { chain: "BEP20", token: "USDT" }
    ↓
BuildCurrency(): "USDT_BEP20"
    ↓
DB 存储: currency = "USDT_BEP20" ✅
    ↓
Core API: 调用使用 "USDT_BEP20" ✅
```

### 创建钱包流程
```
前端: currency = "USDT_BEP20"
    ↓
WalletService.createWallet(): 验证通过 ✅
    ↓
后端: 存储 "USDT_BEP20" ✅
    ↓
Core API: 调用使用 "USDT_BEP20" ✅
```

## 数据库影响

### user_wallets 表
- `currency` 列现在统一存储短格式
- 现有数据如果是长格式，需要迁移脚本转换

### wallet_creation_requests 表
- `currency` 列存储短格式
- 与 Core API 调用保持一致

## 迁移建议

如果现有数据库中有长格式的数据，运行以下迁移：

```sql
-- 将长格式转换为短格式
UPDATE user_wallets 
SET currency = 'USDT_BEP20' 
WHERE currency = 'USDT_BEP20_BINANCE_SMART_CHAIN_MAINNET';

UPDATE user_wallets 
SET currency = 'USDC_BEP20' 
WHERE currency = 'USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET';

-- 同样处理 wallet_creation_requests 表
UPDATE wallet_creation_requests 
SET currency = 'USDT_BEP20' 
WHERE currency = 'USDT_BEP20_BINANCE_SMART_CHAIN_MAINNET';

UPDATE wallet_creation_requests 
SET currency = 'USDC_BEP20' 
WHERE currency = 'USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET';
```

## 向后兼容性

- `ToFullFormat()` 和 `ToShortFormat()` 函数保留，但直接返回原值
- 如果外部系统调用这些函数，不会中断
- 建议在代码中逐步移除对这些函数的依赖

## 总结

✅ 前后端币种格式已统一为短格式  
✅ 所有单元测试通过  
✅ DB 存储、Core API 调用保持一致  
✅ 代码更简洁，易于维护  

---
**修改时间**: 2026-02-25  
**修改者**: Kimi Code CLI
