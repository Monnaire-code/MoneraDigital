# Core API 地址获取测试总结

## 测试环境
- **Core API 地址**: http://198.13.57.142:8080
- **测试时间**: 2026-02-25
- **测试端点**: `POST /api/v1/wallet/address/get`

## 测试结果汇总

### ✅ 成功的币种

| 币种 | 状态 | 获取到的地址 |
|------|------|-------------|
| **TRX(SHASTA)_TRON_TESTNET** | ✅ 成功 | TNJnja4H5LUGTMuTzeu3kVHFzxDP5dU5UG |

### ❌ 失败的币种

| 币种 | 状态 | 错误信息 |
|------|------|---------|
| BTC_TESTNET | ❌ 失败 | 系统内部错误 |
| ETH_SEPOLIA | ❌ 失败 | 系统内部错误 |
| USDT_TRON_TESTNET_SHASTA | ❌ 失败 | 系统内部错误 |

## 详细测试数据

### TRX(SHASTA)_TRON_TESTNET 成功响应
```json
{
  "code": 200,
  "message": "成功",
  "data": {
    "address": "TNJnja4H5LUGTMuTzeu3kVHFzxDP5dU5UG",
    "addressType": null,
    "derivePath": null
  },
  "success": true,
  "timestamp": 1772011932401
}
```

## 结论

1. **Core API 的 `/api/v1/wallet/address/get` 端点可用**
   - 能够成功为 TRX(SHASTA)_TRON_TESTNET 生成地址

2. **当前限制**
   - 只有 TRX(SHASTA)_TRON_TESTNET 配置正常
   - 其他测试币种（BTC_TESTNET、ETH_SEPOLIA、USDT_TRON_TESTNET）返回系统内部错误

3. **建议**
   - 如需要支持其他币种，需要检查 Core API 的配置
   - 确认 Core API 的数据库中是否有其他币种的配置数据

## 验证命令

如需手动验证，可使用以下 curl 命令：

```bash
curl -X POST "http://198.13.57.142:8080/api/v1/wallet/address/get" \
  -H "Content-Type: application/json" \
  -d '{
    "userId": "12345",
    "productCode": "X_FINANCE",
    "currency": "TRX(SHASTA)_TRON_TESTNET"
  }'
```
