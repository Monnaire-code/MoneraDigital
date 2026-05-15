# 2FA设置失败Bug修复提案

## 提案信息
- **创建日期**: 2026-01-25
- **创建人**: Sisyphus
- **状态**: 待实施
- **优先级**: 高
- **标签**: bug, 2fa, deployment, infrastructure

## 问题描述

### 现象症状
用户登录后点击"开启2FA"，页面报错：
- **错误信息**: `Failed to load resource: server responded with a status of 401 ()`
- **API端点**: `/api/auth/2fa/setup:1` 返回405 Method Not Allowed
- **API端点**: `/api/auth/2fa/setup` 返回401 Unauthorized  
- **错误来源**: 指向 `monera-digital--gyc567.replit.app` (已关闭的后端)

### 根本原因分析

经过系统性排除法，确认了**根本原因**：

#### 原因1：Vercel路由重写冲突 ❌ 
**vercel.json配置**：
```json
{
  "source": "/api/auth/:path*",
  "destination": "/api/v2-auth/:path*"
}
```

**影响**：
- 前端调用：`/api/auth/2fa/setup`
- Vercel重写为：`/api/v2-auth/2fa/setup`
- Go后端实际路径：`/api/auth/2fa/setup`
- 结果：**404 Not Found**

#### 原因2：Go后端服务器未运行 ✅ **根本原因**
**验证过程**：
```bash
curl -s http://localhost:8081/api/health # 无响应
curl -s http://localhost:8081/swagger/index.html # 无响应
```

**确认**：Go后端服务在端口8081未运行，所有API调用都失败。

## 解决方案

### 阶段1: Vercel路由重写修复
1. **移除冲突的重写规则**：
   ```json
   // 删除这个重写规则
   {
     "source": "/api/auth/:path*",
     "destination": "/api/v2-auth/:path*"
   }
   ```

2. **确保2FA端点直接映射**：
   ```json
   {
     "source": "/api/auth/2fa/setup",
     "destination": "https://monera-digital--gyc567.replit.app/api/auth/2fa/setup"
   }
   ```

### 阶段2: 启动Go后端服务
1. **启动后端服务**：
   ```bash
   cd /path/to/MoneraDigital
   go run cmd/server/main.go
   ```

2. **验证服务运行**：
   ```bash
   curl http://localhost:8081/api/health
   ```

### 阶段3: 端口配置验证
1. **检查端口占用**：
   ```bash
   lsof -i :8081
   ```

2. **环境变量同步**：
   ```bash
   # 确保Go后端使用相同的环境变量
   export DATABASE_URL="postgresql://..."
   export JWT_SECRET="..."
   export ENCRYPTION_KEY="..."
   ```

## 实施计划

### 文件修改清单
- [x] `vercel.json` - 移除冲突的auth重写规则
- [ ] `internal/routes/routes.go` - 验证路由注册
- [ ] `cmd/server/main.go` - 确保服务器启动配置
- [ ] 启动脚本 `scripts/start-backend.sh` - 创建启动脚本

### 验证计划
1. **Go服务启动验证**：`curl http://localhost:8081/api/health`
2. **2FA Setup接口测试**：`curl -X POST http://localhost:8081/api/auth/2fa/setup`
3. **前端功能测试**：登录→点击"开启2FA"→获取QR码
4. **Vercel部署测试**：重新部署后测试生产环境

## 风险评估

### 技术风险
- **低风险**：仅涉及配置文件修改和服务启动
- **回退方案**：快速回退到现有vercel.json配置

### 业务风险
- **服务中断**：Go后端重启期间2FA功能不可用
- **影响范围**：仅影响开发/测试环境

## 验收标准

1. ✅ Go后端服务成功启动（端口8081监听）
2. ✅ 2FA Setup端点返回200状态和正确数据
3. ✅ 前端2FA设置页面正常工作
4. ✅ Vercel重写规则不冲突
5. ✅ 错误日志消除

## 实施时间线

- **预计完成时间**: 30分钟
- **关键里程碑**：
  - Vercel配置修复 (5分钟)
  - Go服务启动 (10分钟)  
  - 集成测试 (10分钟)
  - 验收验证 (5分钟)

## 相关文档

- [DEPLOYMENT_GUIDE.md](./DEPLOYMENT_GUIDE.md) - 需要更新
- [API文档](./docs/openapi.yaml) - 验证2FA端点
- [错误处理文档](./docs/audit/2fa-handler-audit.md) - 参考

## 审批状态

- [x] 问题根因确认
- [x] 解决方案设计
- [ ] 实施完成
- [ ] 测试验证通过
- [ ] 生产部署成功

---

**备注**: 这是一个基础设施级bug，需要同时修复网络配置和服务状态两个层面的问题。