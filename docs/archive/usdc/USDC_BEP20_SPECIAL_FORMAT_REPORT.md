# USDC_BEP20 特殊长格式处理报告

## 需求说明

**只有 USDC_BEP20 使用长格式**：`USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET`

**其他币种保持短格式**：`代币名称_网络`

| 币种 | DB 存储格式 | 说明 |
|------|------------|------|
| USDT_ERC20 | **USDT_ERC20** | 短格式 |
| USDT_TRC20 | **USDT_TRC20** | 短格式 |
| USDT_BEP20 | **USDT_BEP20** | 短格式 |
| USDC_ERC20 | **USDC_ERC20** | 短格式 |
| USDC_TRC20 | **USDC_TRC20** | 短格式 |
| USDC_BEP20 | **USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET** | ⚠️ 特例：长格式 |

---

## 修改文件

### 1. 后端 (Go)

#### `internal/currency/currency.go`

```go
const (
    USDT_ERC20      = "USDT_ERC20"
    USDT_TRC20      = "USDT_TRC20"
    USDT_BEP20      = "USDT_BEP20"
    USDC_ERC20      = "USDC_ERC20"
    USDC_TRC20      = "USDC_TRC20"
    USDC_BEP20      = "USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET" // ⚠️ 特例
)
```

- **USDC_BEP20**: 定义为长格式
- **ShortFormatToFull**: 只有 USDC_BEP20 映射到长格式
- **IsValid()**: 接受长格式的 USDC_BEP20，也接受短格式的 "USDC_BEP20"
- **ToFullFormat()**: 只有 USDC_BEP20 需要转换
- **ToShortFormat()**: 只有 USDC_BEP20 长格式需要转换
- **NetworkFromCurrency()**: 特殊处理 USDC_BEP20 返回 "BEP20"
- **TokenFromCurrency()**: 特殊处理 USDC_BEP20 返回 "USDC"

#### `internal/services/wallet.go`

```go
func buildCurrencyKey(token, network string) string {
    // ... 构建短格式
    currencyKey := currency.BuildCurrency(token, network)
    // ...
    // Convert to full format for USDC_BEP20 (特例)
    return currency.ToFullFormat(currencyKey)
}
```

- **buildCurrencyKey()**: 调用 ToFullFormat()，只有 USDC_BEP20 会转换

#### `internal/currency/currency_test.go`

- 更新所有测试用例，验证 USDC_BEP20 的特殊处理

### 2. 前端 (TypeScript)

#### `src/lib/wallet-service.ts`

```typescript
export const SUPPORTED_CURRENCIES = [
  'USDT_ERC20',
  'USDT_TRC20',
  'USDT_BEP20',
  'USDC_ERC20',
  'USDC_TRC20',
  'USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET', // ⚠️ 特例
] as const;
```

#### `src/lib/wallet-service.test.ts`

- 更新测试用例，包含 USDC_BEP20 长格式

#### `src/pages/dashboard/AccountOpening.tsx`

```typescript
const CURRENCY_OPTIONS = [
  { value: "USDT_ERC20", label: "USDT (ERC20)" },
  { value: "USDT_TRC20", label: "USDT (TRC20)" },
  { value: "USDT_BEP20", label: "USDT (BEP20)" },
  { value: "USDC_ERC20", label: "USDC (ERC20)" },
  { value: "USDC_TRC20", label: "USDC (TRC20)" },
  { value: "USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET", label: "USDC (BEP20)" }, // ⚠️ 特例
];
```

---

## 数据流示例

### USDT_BEP20 (短格式)

```
前端选择: Token=USDT, Network=BEP20
    ↓
前端显示: "USDT (BEP20)"
    ↓
BuildCurrency("USDT", "BEP20"): "USDT_BEP20"
    ↓
ToFullFormat("USDT_BEP20"): "USDT_BEP20" (不转换)
    ↓
DB 存储: currency = "USDT_BEP20" ✅
    ↓
Core API 调用: "USDT_BEP20" ✅
```

### USDC_BEP20 (长格式 - 特例)

```
前端选择: Token=USDC, Network=BEP20
    ↓
前端显示: "USDC (BEP20)"
    ↓
BuildCurrency("USDC", "BEP20"): "USDC_BEP20" (短格式)
    ↓
ToFullFormat("USDC_BEP20"): "USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET" (转换!)
    ↓
DB 存储: currency = "USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET" ✅ (特例)
    ↓
Core API 调用: ToShortFormat() → "USDC_BEP20" ✅
```

---

## 关键函数说明

### ToFullFormat(currency string) string

```go
// 只有 USDC_BEP20 需要转换
"USDT_BEP20"  → "USDT_BEP20"                          // 不转换
"USDC_BEP20"  → "USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET" // ⚠️ 转换!
```

### ToShortFormat(currency string) string

```go
// 只有 USDC_BEP20 长格式需要转换
"USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET" → "USDC_BEP20" // ⚠️ 转换!
"USDT_BEP20"                            → "USDT_BEP20" // 不转换
```

---

## 测试验证

### 后端测试

```bash
go test ./internal/currency/... -v
```

✅ 所有测试通过 (10个测试)
- TestIsValid: 验证长短格式都接受
- TestNetworkFromCurrency: 验证网络提取正确
- TestTokenFromCurrency: 验证代币提取正确
- TestToFullFormat: 验证只有 USDC_BEP20 转换
- TestToShortFormat: 验证只有 USDC_BEP20 长格式转换

### 前端测试

```bash
npm test -- src/lib/wallet-service.test.ts
```

✅ 所有测试通过 (7个测试)
- 验证 USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET 被接受

---

## 向后兼容性

- **DB 中的旧数据**: 如果已有短格式的 "USDC_BEP20"，IsValid() 仍然接受
- **Core API**: 调用时使用 ToShortFormat() 转换为短格式
- **前端选项**: CURRENCY_OPTIONS 直接使用长格式

---

## 总结

✅ USDC_BEP20 使用长格式存储在 DB  
✅ 其他币种保持短格式  
✅ Core API 调用统一使用短格式  
✅ 所有单元测试通过  
✅ 向后兼容旧数据  

---
**修改时间**: 2026-02-25  
**修改者**: Kimi Code CLI
