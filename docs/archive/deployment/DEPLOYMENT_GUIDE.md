# 部署说明 - 使用Replit后端直接URL

## 方案3实施：配置前端直接使用Replit后端

### 已完成的修改

1. **创建统一API Client**
   - 文件: `src/lib/api-client.ts`
   - 功能: 自动处理开发/生产环境API URL配置
   - 支持环境变量 `VITE_API_BASE_URL`

2. **修改开户页面使用新API Client**
   - 文件: `src/pages/dashboard/AccountOpening.tsx`
   - 将所有 `fetch("/api/...")` 改为 `apiRequest("/api/...")`
   - API Client会自动使用配置的后端URL

3. **创建环境变量模板**
   - 文件: `.env.example`
   - 包含所有环境变量配置示例

### 配置步骤

#### 步骤1: 在Vercel Dashboard配置环境变量

1. 访问 [Vercel Dashboard](https://vercel.com/gyc567s-projects/monera-digital/settings/environment-variables)
2. 添加以下环境变量：

| 变量名 | 值 | 环境 |
|---------|-----|------|
| `VITE_API_BASE_URL` | `https://monera-digital--gyc567.replit.app` | Production |

3. 保存后重新部署应用（或等待自动重新部署）

#### 步骤2: 本地开发

本地开发时，在项目根目录创建 `.env` 文件：

```bash
VITE_API_BASE_URL=https://monera-digital--gyc567.replit.app
```

然后运行：

```bash
npm run dev
```

### 工作原理

#### 开发环境（Vite代理）
```typescript
// VITE_API_BASE_URL 未设置
API_BASE_URL = ''; // 空字符串

getApiUrl("/api/wallet/info")
// 返回: "/api/wallet/info"
// Vite 代理将其转发到: http://localhost:8081/api/wallet/info
```

#### 生产环境（直接后端URL）
```typescript
// VITE_API_BASE_URL = "https://monera-digital--gyc567.replit.app"

getApiUrl("/api/wallet/info")
// 返回: "https://monera-digital--gyc567.replit.app/api/wallet/info"
// 直接请求后端，绕过Vercel代理
```

### 验证方法

#### 1. 检查环境变量是否生效

在浏览器控制台运行：

```javascript
console.log('API Base URL:', import.meta.env.VITE_API_BASE_URL);
```

应该显示: `https://monera-digital--gyc567.replit.app`

#### 2. 测试API请求

在登录后访问页面，检查网络请求：

- 打开开发者工具（F12）
- 切换到 Network 标签
- 访问 /api/wallet/info
- 查看请求URL应该是 `https://monera-digital--gyc567.replit.app/api/wallet/info`
- 不应该包含 `monera-digital-*.vercel.app` 域名

### 已部署版本

- **部署ID**: `dpl_9Kj8dHcaPWrg6sZHvNgLaUiUdsVg`
- **时间**: Sun Jan 18 2026 15:14:40
- **URL**: https://monera-digital-l621zffg0-gyc567s-projects.vercel.app

### 注意事项

⚠️ **Vercel部署保护**

如果Vercel的部署保护仍然拦截请求，需要在Dashboard中：

1. 导航到 **Settings → Protection**
2. 查看密码保护配置
3. 为API路径添加例外规则，或禁用生产环境的保护

### Swagger UI

**生产环境Swagger**: https://monera-digital--gyc567.replit.app/swagger/index.html
- 无需认证即可访问
- 可以直接测试所有API端点

### 故障排除

#### 问题1: API仍然返回401

**原因**: Vite环境变量没有正确注入到前端代码

**解决**:
1. 检查Vercel环境变量是否正确设置（必须是 `VITE_API_BASE_URL`，前缀 `VITE_` 很重要）
2. 确保环境变量值包含协议 `https://`
3. 重新部署Vercel应用

#### 问题2: 本地开发无法工作

**原因**: `.env` 文件未创建或内容错误

**解决**:
1. 复制 `.env.example` 为 `.env`
2. 设置 `VITE_API_BASE_URL` 为空（使用Vite代理）或指向本地后端
3. 重启开发服务器

### 文件变更清单

- [x] 新建: `src/lib/api-client.ts` - 统一API请求工具
- [x] 新建: `.env.example` - 环境变量模板
- [x] 修改: `src/pages/dashboard/AccountOpening.tsx` - 使用新API Client
- [x] 部署: Vercel生产环境（已部署）

### 下一步建议

1. **其他页面也需要更新**: Login, Register, Deposit, Withdraw 等页面也应使用 `apiRequest`
2. **添加TypeScript类型**: 为API响应添加类型定义
3. **错误处理增强**: 统一错误处理和用户提示
4. **测试覆盖**: 为API Client添加单元测试
