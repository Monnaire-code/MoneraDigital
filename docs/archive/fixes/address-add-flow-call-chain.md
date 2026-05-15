# 新增地址完整调用链文档

## 场景
用户 `112@33.com` 登录后，在网页 `https://www.moneradigital.com/dashboard/account-opening` 点击"添加新地址"，在弹出窗口中选择网络和代币，点击确认的完整调用链。

---

## 一、前端调用链 (Frontend)

### 1. 页面组件层

**文件**: `src/pages/dashboard/AccountOpening.tsx`

| 步骤 | 函数/组件 | 说明 |
|------|----------|------|
| 1.1 | `AccountOpening` 组件 | 主页面组件，渲染开户界面 |
| 1.2 | `showAddAddressDialog` state | 控制"添加地址"弹窗显示/隐藏 |
| 1.3 | `handleAddAddress()` | 点击确认按钮时触发 (第329-343行) |
| 1.4 | `getBackendChain(selectedChain)` | 将前端选择的链转换为后端格式 |
| 1.5 | `addAddressMutation.mutate(requestData)` | 调用 React Query Mutation |

### 2. 网络请求层

**文件**: `src/pages/dashboard/AccountOpening.tsx` (第298-327行)

```typescript
const addAddressMutation = useMutation({
  mutationFn: async (data: AddAddressRequest) => {
    return apiRequest("/api/wallet/addresses", {
      method: "POST",
      headers: {
        Authorization: `Bearer ${token}`,
      },
      body: JSON.stringify(data),  // { chain: "BEP20", token: "USDT" }
    });
  },
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ["walletInfo"] });
    setShowAddAddressDialog(false);
    toast({...});  // 显示成功提示
  },
  onError: (err) => {
    toast({...});  // 显示错误提示
  },
});
```

**文件**: `src/lib/api-client.ts` (第162-227行)

| 步骤 | 函数 | 说明 |
|------|------|------|
| 2.1 | `apiRequest<T>()` | 通用 API 请求函数 |
| 2.2 | `getApiUrl(path)` | 构建完整 API URL |
| 2.3 | `tokenManager.getAccessToken()` | 从 localStorage 获取 JWT Token |
| 2.4 | `fetch(url, options)` | 发起 HTTP POST 请求 |
| 2.5 | `parseSuccessResponse<T>()` | 解析成功响应 |

### 3. 前端选择网络的数据映射

**文件**: `src/pages/dashboard/AccountOpening.tsx` (第111-131行)

```typescript
// 前端显示的网络选项
const NETWORK_OPTIONS = [
  { value: "TRC20", label: "TRON (TRC20)", name: "TRON" },
  { value: "TRX(SHASTA)_TRON_TESTNET", label: "TRX (SHASTA) - TRON Testnet", name: "TRON Testnet" },
  { value: "ERC20", label: "Ethereum (ERC20)", name: "Ethereum" },
  { value: "BEP20", label: "BNB Smart Chain (BEP20)", name: "BNB Smart Chain" },
];

// 映射到后端的链值
const CHAIN_TO_BACKEND_MAP: Record<string, string> = {
  "TRC20": "TRC20",
  "TRON_TESTNET": "TRON_TESTNET",
  "ERC20": "ERC20",
  "BEP20": "BEP20",
};
```

---

## 二、API 路由层 (API Routes)

### 1. Vercel Serverless Function

**文件**: `api/[...route].ts` (第43行)

```typescript
'POST /wallet/addresses': { 
  requiresAuth: true, 
  backendPath: '/api/wallet/addresses' 
},
```

| 步骤 | 函数 | 说明 |
|------|------|------|
| 1 | `handler()` | 主路由处理器 (第92行) |
| 2 | `parseRoute(req)` | 解析请求方法和路径 |
| 3 | `findRoute(method, path)` | 查找路由配置 |
| 4 | `verifyToken(req)` | JWT 认证验证 |
| 5 | 代理请求到后端 | `fetch(backendUrl, options)` |

---

## 三、后端调用链 (Backend)

### 1. HTTP Handler 层

**文件**: `internal/handlers/wallet_handler.go` (第204-271行)

```go
func (h *Handler) AddWalletAddress(c *gin.Context) {
    // 1. 从 JWT Token 获取 userID
    userID, exists := c.Get("userID")
    
    // 2. 解析请求体
    var req dto.AddWalletAddressRequest
    c.ShouldBindJSON(&req)  // { "chain": "BEP20", "token": "USDT" }
    
    // 3. 验证必填字段
    if req.Chain == "" || req.Token == "" {
        return 400 错误
    }
    
    // 4. 调用 Service 层
    wallet, err := h.WalletService.AddAddress(c.Request.Context(), userID.(int), 
        services.AddAddressRequest{
            Chain: req.Chain,
            Token: req.Token,
        })
    
    // 5. 返回响应
    c.JSON(http.StatusOK, gin.H{...})
}
```

### 2. Service 层

**文件**: `internal/services/wallet.go` (第281-375行)

```go
func (s *WalletService) AddAddress(ctx context.Context, userID int, req AddAddressRequest) (*models.UserWallet, error) {
    // 1. 获取用户钱包信息
    wallet, err := s.repo.GetActiveWalletByUserID(ctx, userID)
    
    // 2. 构建货币键 (如: USDT_BEP20)
    addressKey := buildCurrencyKey(req.Token, req.Chain)
    // 结果: "USDT_BEP20_BINANCE_SMART_CHAIN_MAINNET"
    
    // 3. 检查地址是否已存在
    existingWallet, err := s.repo.GetUserWalletByUserAndCurrency(ctx, userID, addressKey)
    if existingWallet != nil {
        return existingWallet, nil  // 已存在，直接返回
    }
    
    // 4. 调用 Core API 获取地址
    coreCurrency := currency.ToShortFormat(addressKey)  // "USDT_BEP20"
    coreResp, err := s.coreAPIClient.GetAddress(ctx, coreapi.GetAddressRequest{
        UserID:      fmt.Sprintf("%d", userID),
        ProductCode: productCode,  // "X_FINANCE"
        Currency:    coreCurrency, // "USDT_BEP20"
    })
    
    // 5. 创建用户钱包记录
    newWallet := &models.UserWallet{
        UserID:    userID,
        WalletID:  wallet.WalletID.String,
        Currency:  addressKey,
        Address:   coreResp.Address,  // 从 Core API 返回的地址
        Status:    models.UserWalletStatusNormal,
        IsPrimary: false,
    }
    
    // 6. 存入数据库
    result, err := s.repo.AddUserWalletAddress(ctx, newWallet)
    
    return result, nil
}
```

### 3. 货币格式转换

**文件**: `internal/currency/currency.go`

| 步骤 | 函数 | 输入 | 输出 |
|------|------|------|------|
| 1 | `NormalizeNetwork(network)` | "BEP20" | "BEP20" |
| 2 | `BuildCurrency(token, network)` | "USDT", "BEP20" | "USDT_BEP20" |
| 3 | `ToFullFormat(currency)` | "USDT_BEP20" | "USDT_BEP20_BINANCE_SMART_CHAIN_MAINNET" |
| 4 | `ToShortFormat(currency)` | "USDT_BEP20_BINANCE_SMART_CHAIN_MAINNET" | "USDT_BEP20" |

### 4. Core API 客户端调用

**文件**: `internal/coreapi/wallet_client.go` (第122-157行)

```go
func (c *Client) GetAddress(ctx context.Context, req GetAddressRequest) (*AddressInfo, error) {
    // 1. 构建请求 URL
    url := fmt.Sprintf("%s/api/v1/wallet/address/get", c.baseURL)
    // 结果: "http://198.13.57.142:8080/api/v1/wallet/address/get"
    
    // 2. 序列化请求体
    body, _ := json.Marshal(req)
    // { "userId": "112", "productCode": "X_FINANCE", "currency": "USDT_BEP20" }
    
    // 3. 创建 HTTP 请求
    httpReq, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
    httpReq.Header.Set("Content-Type", "application/json")
    
    // 4. 发送请求到 Core API
    resp, err := c.httpClient.Do(httpReq)
    
    // 5. 解析响应
    var addressInfo AddressInfo
    json.Unmarshal(respData, &addressInfo)
    
    return &addressInfo, nil
}
```

### 5. 数据持久化层

**文件**: `internal/repository/postgres/wallet.go` (AddUserWalletAddress 方法)

```go
func (r *WalletRepository) AddUserWalletAddress(ctx context.Context, wallet *models.UserWallet) (*models.UserWallet, error) {
    query := `
        INSERT INTO user_wallets (
            user_id, wallet_id, currency, address, 
            address_type, derive_path, status, is_primary, request_id
        ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
        RETURNING id, created_at
    `
    // 执行 SQL 插入
    row := r.db.QueryRowContext(ctx, query, ...)
    row.Scan(&wallet.ID, &wallet.CreatedAt)
    return wallet, nil
}
```

---

## 四、Core API 调用

### 外部 Core API 端点

**URL**: `http://198.13.57.142:8080/api/v1/wallet/address/get`

**请求**:
```json
{
  "userId": "112",
  "productCode": "X_FINANCE",
  "currency": "USDT_BEP20"
}
```

**响应**:
```json
{
  "code": 200,
  "message": "成功",
  "data": {
    "address": "0x8755925045F5fBb9f2098A154951D2601D2deCfd",
    "addressType": null,
    "derivePath": null
  },
  "success": true
}
```

---

## 五、完整调用链图示

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              FRONTEND (React)                               │
├─────────────────────────────────────────────────────────────────────────────┤
│  1. AccountOpening.tsx                                                      │
│     └── handleAddAddress()                                                  │
│         └── addAddressMutation.mutate({ chain: "BEP20", token: "USDT" })   │
│             └── apiRequest("/api/wallet/addresses", POST)                   │
│                 └── fetch("/api/wallet/addresses", {                        │
│                       body: JSON.stringify({ chain: "BEP20", token:"USDT" })│
│                     })                                                      │
└───────────────────────────────────┬─────────────────────────────────────────┘
                                    │ HTTP POST
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                         API ROUTES (Vercel)                                 │
├─────────────────────────────────────────────────────────────────────────────┤
│  2. api/[...route].ts                                                       │
│     └── ROUTE_CONFIG['POST /wallet/addresses']                              │
│         └── requiresAuth: true                                              │
│             └── verifyToken(req)  // JWT 验证                               │
│                 └── proxy to BACKEND_URL/api/wallet/addresses               │
└───────────────────────────────────┬─────────────────────────────────────────┘
                                    │ HTTP POST
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                          BACKEND (Go + Gin)                                 │
├─────────────────────────────────────────────────────────────────────────────┤
│  3. internal/routes/routes.go                                               │
│     └── wallet.POST("/addresses", h.AddWalletAddress)                       │
│         └── AuthMiddleware()  // JWT 验证                                   │
│             └── internal/handlers/wallet_handler.go                         │
│                 └── AddWalletAddress(c *gin.Context)                        │
│                     └── h.WalletService.AddAddress(ctx, userID, req)        │
└───────────────────────────────────┬─────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                      SERVICE LAYER (Go)                                     │
├─────────────────────────────────────────────────────────────────────────────┤
│  4. internal/services/wallet.go                                             │
│     └── AddAddress(ctx, userID, {Chain, Token})                             │
│         ├── buildCurrencyKey("USDT", "BEP20")                               │
│         │   └── "USDT_BEP20" (短格式，用于 DB 存储)                         │
│         ├── s.repo.GetUserWalletByUserAndCurrency() // 检查是否已存在        │
│         └── s.coreAPIClient.GetAddress(ctx, coreapi.GetAddressRequest{...}) │
│             └── internal/coreapi/wallet_client.go                           │
│                 └── GetAddress(ctx, req)                                    │
└───────────────────────────────────┬─────────────────────────────────────────┘
                                    │ HTTP POST
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                        CORE API (External)                                  │
├─────────────────────────────────────────────────────────────────────────────┤
│  5. http://198.13.57.142:8080                                               │
│     └── POST /api/v1/wallet/address/get                                     │
│         └── Request: {                                                      │
│               "userId": "112",                                              │
│               "productCode": "X_FINANCE",                                   │
│               "currency": "USDT_BEP20"                                      │
│             }                                                               │
│         └── Response: {                                                     │
│               "address": "0x8755925045F5fBb9f2098A154951D2601D2deCfd"       │
│             }                                                               │
└───────────────────────────────────┬─────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                    BACKEND - Repository Layer                               │
├─────────────────────────────────────────────────────────────────────────────┤
│  6. internal/repository/postgres/wallet.go                                  │
│     └── AddUserWalletAddress(ctx, newWallet)                                │
│         └── INSERT INTO user_wallets (...) VALUES (...)                     │
└───────────────────────────────────┬─────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                         DATABASE (PostgreSQL)                               │
├─────────────────────────────────────────────────────────────────────────────┤
│  7. user_wallets 表                                                         │
│     └── 插入新记录:                                                         │
│         user_id=112, currency="USDT_BEP20_BINANCE_SMART_CHAIN_MAINNET",    │
│         address="0x8755925045F5fBb9f2098A154951D2601D2deCfd"                │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 六、响应返回链

```
1. Database → Repository: 插入成功，返回 wallet ID
2. Repository → Service: 返回 *models.UserWallet
3. Service → Handler: 返回 wallet 对象
4. Handler → API Route: JSON 响应 { walletId, address, currency, status }
5. API Route → Frontend: HTTP 200 OK
6. Frontend → UI: 
   - queryClient.invalidateQueries(["walletInfo"])  // 刷新钱包列表
   - setShowAddAddressDialog(false)  // 关闭弹窗
   - toast({ title: "地址添加成功" })  // 显示成功提示
```

---

## 七、关键数据转换

### 7.1 Token + Network → Currency Key

| Token | Network | Short Format | DB 存储格式 |
|-------|---------|--------------|------------|
| USDT | TRC20 | USDT_TRC20 | **USDT_TRC20** |
| USDT | BEP20 | USDT_BEP20 | **USDT_BEP20** |
| USDT | ERC20 | USDT_ERC20 | **USDT_ERC20** |
| USDC | BEP20 | USDC_BEP20 | **USDC_BEP20** |
| TRX | TRX(SHASTA)_TRON_TESTNET | TRX(SHASTA)_TRON_TESTNET | **TRX(SHASTA)_TRON_TESTNET** |

> ⚠️ **注意**: DB 存储使用短格式（如 `USDT_BEP20`），Core API 调用也使用短格式。长格式（如 `USDT_BEP20_BINANCE_SMART_CHAIN_MAINNET`）仅用于显示或特定后端逻辑。

### 7.2 请求/响应数据流

```
前端请求:                    后端处理:                     Core API 请求:
{                            {                             {
  "chain": "BEP20",    →     buildCurrencyKey()    →      "currency": "USDT_BEP20"
  "token": "USDT"           结果:                          (Short Format)
}                             "USDT_BEP20"               }
                            }
                            (DB 存储格式 - 短格式)
```

**数据流说明**:
1. 前端发送: `{ chain: "BEP20", token: "USDT" }`
2. 后端 `buildCurrencyKey()`: 返回短格式 `"USDT_BEP20"`
3. 数据库存储: `currency = "USDT_BEP20"` (短格式)
4. Core API 调用: 使用短格式 `"USDT_BEP20"`

---

## 八、错误处理链

| 层级 | 错误类型 | 处理方式 |
|------|---------|---------|
| Frontend | 网络错误 | apiRequest 捕获，显示 toast 错误 |
| Frontend | 401 未授权 | 自动刷新 token，失败则跳转登录 |
| API Route | JWT 无效 | 返回 401，前端处理 |
| Handler | 参数错误 | 返回 400 Bad Request |
| Service | 钱包不存在 | 返回 "wallet not found" 错误 |
| Service | Core API 失败 | 返回 500 + 错误信息 |
| Repository | 数据库错误 | 返回 SQL 错误信息 |

---

## 九、文件清单

| 层级 | 文件路径 | 关键函数 |
|------|---------|---------|
| Frontend Page | `src/pages/dashboard/AccountOpening.tsx` | `handleAddAddress()`, `addAddressMutation` |
| Frontend API Client | `src/lib/api-client.ts` | `apiRequest()` |
| API Route | `api/[...route].ts` | `handler()`, `ROUTE_CONFIG` |
| Backend Route | `internal/routes/routes.go` | `SetupRoutes()`, `wallet.POST()` |
| Backend Handler | `internal/handlers/wallet_handler.go` | `AddWalletAddress()` |
| Backend Service | `internal/services/wallet.go` | `AddAddress()`, `buildCurrencyKey()` |
| Backend Currency | `internal/currency/currency.go` | `NormalizeNetwork()`, `BuildCurrency()`, `ToFullFormat()` |
| Core API Client | `internal/coreapi/wallet_client.go` | `GetAddress()` |
| Backend Repository | `internal/repository/postgres/wallet.go` | `AddUserWalletAddress()` |
| Database Schema | `drizzle/migrations/` | `user_wallets` 表 |

---

**文档生成时间**: 2026-02-25  
**适用版本**: Monera Digital 最新代码库
