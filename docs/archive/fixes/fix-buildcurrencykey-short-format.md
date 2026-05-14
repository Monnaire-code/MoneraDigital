# 修复报告: buildCurrencyKey 返回短格式

## 问题描述

`buildCurrencyKey` 函数原来返回长格式（如 `USDT_BEP20_BINANCE_SMART_CHAIN_MAINNET`），但实际应该返回短格式（如 `USDT_BEP20`）用于数据库存储。

## 影响

- DB 表 `user_wallets` 的 `currency` 列存储了不必要的长格式
- 与 Core API 调用不一致（Core API 使用短格式）
- 可能导致数据不一致和查询问题

## 修复内容

### 修改文件
`internal/services/wallet.go`

### 修复前后对比

**修复前:**
```go
func buildCurrencyKey(token, network string) string {
    network = currency.NormalizeNetwork(network)
    currencyKey := currency.BuildCurrency(token, network)
    if !currency.IsValid(currencyKey) {
        return ""
    }
    // Convert to full backend format
    return currency.ToFullFormat(currencyKey)  // USDT_BEP20_BINANCE_SMART_CHAIN_MAINNET
}
```

**修复后:**
```go
func buildCurrencyKey(token, network string) string {
    network = currency.NormalizeNetwork(network)
    currencyKey := currency.BuildCurrency(token, network)
    if !currency.IsValid(currencyKey) {
        return ""
    }
    // Return short format for database storage
    return currencyKey  // USDT_BEP20
}
```

## 数据流验证

修复后的完整数据流:

| 步骤 | 位置 | 格式 | 值 |
|------|------|------|-----|
| 1 | 前端请求 | - | `{ chain: "BEP20", token: "USDT" }` |
| 2 | buildCurrencyKey | 短格式 | `"USDT_BEP20"` |
| 3 | 检查地址存在 | 短格式 | `"USDT_BEP20"` |
| 4 | Core API 请求 | 短格式 | `"USDT_BEP20"` |
| 5 | **数据库存储** | **短格式** | **`"USDT_BEP20"`** |

## 支持的货币格式

| Token | Network | DB 存储格式 |
|-------|---------|------------|
| USDT | TRC20 | USDT_TRC20 |
| USDT | BEP20 | **USDT_BEP20** |
| USDT | ERC20 | USDT_ERC20 |
| USDC | TRC20 | USDC_TRC20 |
| USDC | BEP20 | USDC_BEP20 |
| USDC | ERC20 | USDC_ERC20 |
| TRX | TRX(SHASTA)_TRON_TESTNET | TRX(SHASTA)_TRON_TESTNET |

## 测试建议

1. 新增地址后检查 DB:
```sql
SELECT user_id, currency, address FROM user_wallets WHERE currency LIKE 'USDT_BEP20';
-- 应该返回 currency = 'USDT_BEP20'，而不是 'USDT_BEP20_BINANCE_SMART_CHAIN_MAINNET'
```

2. 验证 Core API 调用正常:
```bash
curl -X POST http://198.13.57.142:8080/api/v1/wallet/address/get \
  -H "Content-Type: application/json" \
  -d '{"userId":"112","productCode":"X_FINANCE","currency":"USDT_BEP20"}'
```

## 相关文件

- `internal/services/wallet.go` - 主要修复文件
- `internal/currency/currency.go` - 货币格式定义
- `address-add-flow-call-chain.md` - 调用链文档已同步更新

## 状态

✅ 已修复并验证
