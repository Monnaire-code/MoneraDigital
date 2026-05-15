# Replit 后端部署指南

## 架构说明

本项目采用分离部署架构：
- **前端**: 部署在 Vercel (https://www.moneradigital.com)
- **后端**: 部署在 Replit (Auto-scale)

## 部署配置

### 1. `.replit` 配置

```toml
[deployment]
deploymentTarget = "autoscale"
build = ["bash", "scripts/build-backend-only.sh"]
run = ["./server"]
```

### 2. 构建脚本 `scripts/build-backend-only.sh`

```bash
#!/bin/bash
set -e

cd "$(dirname "$0")/.."

echo "Installing Go dependencies..."
go mod download

echo "Building Go backend..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o server ./cmd/server/main.go

echo "Build complete!"
```

## 关键优化

### 1. 只构建后端
- 移除了 `npm install` 和 `npm run build`
- 只下载 Go 依赖并构建后端二进制文件
- 构建时间从几分钟缩短到几十秒

### 2. 端口配置
- 后端默认监听端口 80
- Replit 会自动设置 PORT 环境变量
- 通过 `viper.AutomaticEnv()` 读取环境变量

### 3. 静态文件处理
- 后端代码仍然保留静态文件服务逻辑
- 如果 `dist` 目录不存在，会返回 404
- 前端通过 Vercel 独立部署，不依赖后端的静态文件

## 环境变量

在 Replit 的 Secrets 中设置以下变量：

```
DATABASE_URL=postgres://...
REDIS_URL=redis://...
JWT_SECRET=your-jwt-secret
ENCRYPTION_KEY=your-encryption-key
GIN_MODE=release
ENV=production
```

## 部署步骤

1. 推送代码到 Replit
2. Replit 会自动检测到 `.replit` 配置
3. 点击 Deploy 按钮
4. 等待构建完成（约 30-60 秒）
5. 获取部署 URL

## 验证部署

```bash
curl https://your-replit-app.replit.app/api/health
```

## 前端配置

确保前端指向正确的后端 URL：

```javascript
// src/lib/api.ts
const API_BASE_URL = 'https://your-replit-app.replit.app';
```

或者在 Vercel 环境变量中设置：
```
VITE_API_URL=https://your-replit-app.replit.app
```

## 故障排除

### 构建失败
检查 `scripts/build-backend-only.sh` 是否有执行权限：
```bash
chmod +x scripts/build-backend-only.sh
```

### 端口冲突
确保 `PORT` 环境变量已正确设置。Replit 会自动设置，但本地测试时需要手动设置。

### 数据库连接失败
检查 `DATABASE_URL` 是否正确配置在 Replit Secrets 中。
