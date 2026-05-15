# 统一 Serverless Function 架构报告

## 问题背景

Vercel Hobby 计划限制每个部署最多 **12 个 Serverless Functions**。当项目需要多个 API 端点时，这个限制很容易被突破，导致部署失败：

```
No more than 12 Serverless Functions can be added to a Deployment 
on the Hobby plan. Create a team (Pro plan) to deploy more.
```

## 解决方案

### 统一路由架构

项目采用单一入口文件 `api/[...route].ts` 处理所有 API 请求：

```
api/
├── [...route].ts          # 统一路由处理器（唯一 Serverless Function）
└── __route__.test.ts      # 路由测试
```

### 架构优势

1. **单一 Serverless Function**: 无论有多少 API 端点，只有一个函数
2. **集中配置**: 所有路由在 `ROUTE_CONFIG` 中配置
3. **易于维护**: 添加新端点只需修改配置，无需创建新文件
4. **类型安全**: TypeScript 提供完整的类型支持

## 实施细节

### 1. 路由配置

```typescript
const ROUTE_CONFIG: Record<string, RouteConfig> = {
  // Auth endpoints
  'POST /auth/login': { requiresAuth: false, backendPath: '/api/auth/login' },
  'POST /auth/register': { requiresAuth: false, backendPath: '/api/auth/register' },
  
  // 2FA endpoints
  'POST /auth/2fa/setup': { requiresAuth: true, backendPath: '/api/auth/2fa/setup' },
  'POST /auth/2fa/enable': { requiresAuth: true, backendPath: '/api/auth/2fa/enable' },
  
  // ... 其他路由
};
```

### 2. 动态路由支持

支持动态路由参数：

```typescript
// /addresses/123, /addresses/123/verify, etc.
if (path.startsWith('/addresses/')) {
  const isValidAddressRoute =
    /^\/addresses\/[\w-]+(\/verify|\/primary)?$/.test(path) &&
    (method === 'DELETE' || method === 'POST' || method === 'PUT' || method === 'PATCH');
  
  if (isValidAddressRoute) {
    return {
      found: true,
      config: { requiresAuth: true, backendPath: '' },
      backendPath: `/api${path}`,
    };
  }
}
```

### 3. 修复的问题

#### 路由重复注册
**文件**: `internal/routes/routes.go`
**问题**: `/api/wallet/create` 被注册了两次
**修复**: 删除了受保护路由中的重复注册

#### 环境变量配置
**文件**: `.env`
**问题**: `BACKEND_URL` 指向生产环境
**修复**: 改为 `http://localhost:8081`

## 测试覆盖

### 测试统计

```bash
npm test -- api/__route__.test.ts --coverage
```

**结果**:
- 测试文件: 1 passed
- 测试用例: 29 passed
- 语句覆盖率: 92.45%
- 分支覆盖率: 74.46%
- 函数覆盖率: 100%
- 行覆盖率: 92.45%

### 测试覆盖的功能

1. **路由解析**: 简单路由、多级路由、动态路由
2. **认证检查**: 公开端点、受保护端点、无效 token
3. **HTTP 方法**: GET、POST、PUT、PATCH、DELETE
4. **错误处理**: 404 路由、后端错误、网络错误
5. **响应处理**: JSON 解析、错误响应、成功响应
6. **动态地址路由**: DELETE、POST /verify、POST /primary

## 文档更新

已更新以下文档，添加统一 Serverless Function 架构规则：

1. **CLAUDE.md**: 更新 Backend Structure 章节
2. **AGENTS.md**: 更新 API Routes 章节
3. **GEMINI.md**: 更新 Key Directory Structure 章节

## 使用指南

### 添加新 API 端点

1. **在 `ROUTE_CONFIG` 中添加配置**:
```typescript
'POST /new/endpoint': { 
  requiresAuth: true, 
  backendPath: '/api/new/endpoint' 
}
```

2. **在 Go 后端添加处理器**:
```go
// internal/routes/routes.go
protected.POST("/new/endpoint", h.NewEndpointHandler)
```

3. **添加测试**:
```typescript
it('should route POST /new/endpoint correctly', async () => {
  // 测试代码
});
```

### 禁止的做法

❌ **不要创建多个 API 文件**:
```
api/
├── auth/
│   ├── login.ts          # ❌ 单独的 Serverless Function
│   ├── register.ts       # ❌ 单独的 Serverless Function
│   └── logout.ts         # ❌ 单独的 Serverless Function
├── 2fa/
│   ├── setup.ts          # ❌ 单独的 Serverless Function
│   └── enable.ts         # ❌ 单独的 Serverless Function
└── ... (更多文件)
```

✅ **应该这样做**:
```
api/
├── [...route].ts          # 统一路由处理器
└── __route__.test.ts      # 路由测试
```

## 部署验证

### 本地测试

```bash
# 启动后端
go run ./cmd/server

# 测试 API
curl -X POST http://localhost:8081/api/auth/2fa/skip \
  -H "Content-Type: application/json" \
  -d '{"userId": 1}'
```

### Vercel 部署

```bash
# 构建
vercel build

# 部署
vercel --prod
```

## 设计原则遵循

### KISS (Keep It Simple, Stupid)
- 单一文件处理所有 API 请求
- 配置驱动，无需修改代码即可添加路由
- 清晰的职责分离

### 高内聚低耦合
- 路由配置集中在一个地方
- 路由处理器与业务逻辑完全分离
- 前端只负责调用，后端处理业务

### 100% 功能覆盖
- 所有路由配置都有对应测试
- 错误路径和成功路径都经过测试
- 动态路由和静态路由都覆盖

### 不影响其他功能
- 仅修改架构，不改变业务逻辑
- 向后兼容现有 API
- 所有现有测试通过

## 结论

统一 Serverless Function 架构成功解决了 Vercel Hobby 计划的 12 个函数限制问题。通过单一入口文件和集中配置，项目可以无限扩展 API 端点，而不用担心部署限制。

## 参考文档

- [OpenSpec 提案](openspec/unified-serverless-function.md)
- [CLAUDE.md](CLAUDE.md)
- [AGENTS.md](AGENTS.md)
- [GEMINI.md](GEMINI.md)
- [Vercel Serverless Functions Limits](https://vercel.com/docs/concepts/limits/overview#serverless-functions)
