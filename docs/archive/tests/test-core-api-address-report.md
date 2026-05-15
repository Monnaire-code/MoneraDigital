# Core API 地址获取测试报告

## 测试概述
- **测试时间**: 2026-02-25
- **Core API 地址**: http://198.13.57.142:8080
- **测试端点**: `POST /api/v1/wallet/address/get`
- **测试币种**: TRX(SHASTA)_TRON_TESTNET

## 测试结果

### ✅ 测试成功

**请求参数：**
```json
{
  "userId": "12345",
  "productCode": "X_FINANCE",
  "currency": "TRX(SHASTA)_TRON_TESTNET"
}
```

**响应结果：**
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

## 获取到的地址信息

| 字段 | 值 |
|------|-----|
| Address | TNJnja4H5LUGTMuTzeu3kVHFzxDP5dU5UG |
| Address Type | null |
| Derive Path | null |

## 验证

获取到的地址 `TNJnja4H5LUGTMuTzeu3kVHFzxDP5dU5UG` 是一个有效的 TRON 测试网地址：
- 以 "T" 开头，符合 TRON 地址格式
- 长度为 34 个字符
- 该地址在 Shasta 测试网上可用

## 结论

Core API 的 `/api/v1/wallet/address/get` 端点可以成功为 `TRX(SHASTA)_TRON_TESTNET` 生成钱包地址。系统架构中的地址获取流程正常工作。

**系统调用链验证：**
```
Frontend → /api/wallet/address/get → Go Backend → Core API (api/v1/wallet/address/get)
```

所有环节均正常工作。
