# USDC_BEP20 Core API 长格式使用说明

## 重要变更

**USDC_BEP20 在 Core API 调用中必须使用长格式：**
`USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET`

## 格式使用对照表

| 场景 | USDT_BEP20 | USDC_BEP20 |
|------|------------|------------|
| DB 存储 | `USDT_BEP20` (短格式) | `USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET` (长格式) |
| Core API 调用 | `USDT_BEP20` (短格式) | `USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET` (长格式) |

## 代码实现

### 调用 Core API 前转换

所有调用 Core API 的地方都使用 `currency.ToFullFormat()` 确保格式正确：

```go
// 1. CreateWallet 调用
coreCurrency := currency.ToFullFormat(currencyCode)
 coreResp, err := s.coreAPIClient.CreateWallet(ctx, coreapi.CreateWalletRequest{
    UserID:      userID,
    ProductCode: productCode,
    Currency:    coreCurrency,  // USDC_BEP20 -> 长格式
})

// 2. GetAddress 调用
coreCurrency := currency.ToFullFormat(currencyCode)
addressInfo, err := s.coreAPIClient.GetAddress(ctx, coreapi.GetAddressRequest{
    UserID:      fmt.Sprintf("%d", userID),
    ProductCode: productCode,
    Currency:    coreCurrency,  // USDC_BEP20 -> 长格式
})
```

### ToFullFormat 函数行为

```go
// internal/currency/currency.go
func ToFullFormat(currency string) string {
    if full, ok := ShortFormatToFull[currency]; ok {
        return full
    }
    return currency
}

// ShortFormatToFull 映射:
{
    "USDT_ERC20": "USDT_ERC20",
    "USDT_TRC20": "USDT_TRC20",
    "USDT_BEP20": "USDT_BEP20",
    "USDC_ERC20": "USDC_ERC20",
    "USDC_TRC20": "USDC_TRC20",
    "USDC_BEP20": "USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET", // ⚠️ 唯一的长格式
}
```

## 调用链示例

### USDT_BEP20 调用链

```
1. 前端: currency = "USDT_BEP20"
2. Handler: 接收 "USDT_BEP20"
3. Service.CreateWallet:
   - ToFullFormat("USDT_BEP20") -> "USDT_BEP20"
   - Core API 调用: "USDT_BEP20"
4. DB 存储: "USDT_BEP20"
```

### USDC_BEP20 调用链 (特例)

```
1. 前端: currency = "USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET"
2. Handler: 接收 "USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET"
3. Service.CreateWallet:
   - ToFullFormat("USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET") -> "USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET"
   - Core API 调用: "USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET"
4. DB 存储: "USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET"
```

或者如果前端传入短格式：

```
1. 前端: currency = "USDC_BEP20"
2. Handler: 接收 "USDC_BEP20"
3. Service.CreateWallet:
   - ToFullFormat("USDC_BEP20") -> "USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET"
   - Core API 调用: "USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET"
4. DB 存储: "USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET"
```

## 修改的文件

### 后端
- `internal/currency/currency.go` - 定义长格式常量
- `internal/services/wallet.go` - 所有 Core API 调用前使用 ToFullFormat
- `internal/services/wallet_test.go` - 更新测试期望

### 前端
- `src/lib/wallet-service.ts` - SUPPORTED_CURRENCIES 使用长格式
- `src/pages/dashboard/AccountOpening.tsx` - CURRENCY_OPTIONS 使用长格式

## 测试验证

```bash
# 货币测试
go test ./internal/currency/... -v
# ✅ PASS (10个测试)

# 服务测试
go test ./internal/services/... -v -run "TestBuildCurrencyKey"
# ✅ PASS (USDC_BEP20 返回长格式)

# 前端测试
npm test -- src/lib/wallet-service.test.ts
# ✅ PASS (包含 USDC_BEP20 长格式测试)
```

## 总结

✅ DB 存储：USDC_BEP20 使用长格式  
✅ Core API 调用：USDC_BEP20 使用长格式  
✅ 其他币种：保持短格式  
✅ 统一使用 `ToFullFormat()` 确保格式正确  

---
**修改时间**: 2026-02-25  
**修改者**: Kimi Code CLI
