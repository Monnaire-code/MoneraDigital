# 2FA设置405错误修复提案

## 提案信息
- **创建日期**: 2026-01-25
- **创建人**: Sisyphus
- **状态**: 待实施
- **优先级**: 紧急
- **标签**: bug, 2fa, api, deployment, infrastructure

## 问题描述

### 现象症状
用户登录后点击"开启2FA"，页面报错：
- **错误信息**: `405 Method Not Allowed` on POST to `https://www.moneradigital.com/api/auth/2fa/setup`
- **前端错误**: `2FA Setup error: SyntaxError: Unexpected end of JSON input`
- **用户访问域名**: `www.moneradigital.com`
- **错误来源**: 前端调用时被重定向到 `monera-digital--gyc567.replit.app`

### 根本原因分析

经过系统性测试，确认了**双重根本原因**：

#### 🎯 主要原因：API端点缺失
**问题**: `www.moneradigital.com` 域名下没有部署 `api/auth/2fa/setup` 端点
**验证**: `curl -I https://www.moneradigital.com/api/auth/2fa/setup` 返回 `200` (HTML页面，不是API)
**结果**: 405错误是因为Vercel找不到API端点，返回SPA默认路由

#### 🔧 次要原因：域名配置不匹配
**问题**: 前端期望访问 `www.moneradigital.com` 但API请求被重定向到 `monera-digital--gyc567.replit.app`
**验证**: `monera-digital--gyc567.replit.app/api/auth/2fa/setup` 返回正确的 `401` (需要token)
**结果**: JSON解析错误是因为跨域或无效的响应格式

## 解决方案

### 阶段1: API端点部署（根本解决）

#### 步骤1: 在www.moneradigital.com部署2FA API
```bash
# 需要在www.moneradigital.com上部署/api/auth/2fa/* 端点
# 选项A: 直接在主域名上部署API
# 选项B: 使用子域名 api.moneradigital.com
```

#### 步骤2: 验证API端点可用性
```bash
# 部署后测试
curl -X POST https://www.moneradigital.com/api/auth/2fa/setup \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer valid-token"

# 期望结果: 返回JSON响应，而不是405
```

### 阶段2: Vercel配置优化

#### 步骤3: 更新vercel.json路由规则
```json
{
  "rewrites": [
    {
      "source": "/api/auth/2fa/setup",
      "destination": "/api/v2-auth/2fa/setup"
    },
    {
      "source": "/api/auth/2fa/enable",
      "destination": "/api/v2-auth/2fa/enable"
    },
    {
      "source": "/api/auth/2fa/disable",
      "destination": "/api/v2-auth/2fa/disable"
    },
    {
      "source": "/api/auth/2fa/verify-login",
      "destination": "/api/v2-auth/2fa/verify-login"
    },
    // 保留其他重写规则...
    {
      "source": "/(.*)",
      "destination": "/index.html"
    }
  ]
}
```

#### 步骤4: 前端API客户端更新
```typescript
// 更新环境变量配置
const API_BASE_URL = process.env.NEXT_PUBLIC_API_URL || 'https://api.moneradigital.com';
```

### 阶段3: 域名统一配置

#### 步骤5: 环境变量统一
```bash
# 在www.moneradigital.com部署环境变量
NEXT_PUBLIC_API_URL=https://api.moneradigital.com
```

## 技术风险评估

### 🔴 高风险: 2FA功能完全不可用
- **影响范围**: 用户无法启用双因素认证
- **安全风险**: 账户安全性降级
- **用户影响**: 核心安全功能失效

### 🟡 中风险: 部署复杂度
- **技术债务**: 需要在多个域名部署API
- **维护成本**: 需要管理多套部署配置

### 🟢 低风险: 配置更新
- **回退方案**: 快速回退到当前配置
- **数据安全**: 不会影响现有数据

## 实施计划

### 准备工作
1. [ ] 备份当前vercel.json配置
2. [ ] 确认Go后端2FA接口在新域名下可用
3. [ ] 测试当前错误复现步骤

### 修复实施
1. [ ] 在www.moneradigital.com部署/api/auth/2fa/* API端点
2. [ ] 更新vercel.json添加2FA路由重写规则
3. [ ] 更新前端API客户端域名配置
4. [ ] 提交修改到git
5. [ ] 重新部署www.moneradigital.com

### 验证测试
1. [ ] 测试2FA setup端点返回正确JSON响应
2. [ ] 测试2FA enable流程正常工作
3. [ ] 测试2FA disable功能正常
4. [ ] 验证错误处理逻辑正确
5. [ ] 测试完整2FA启用/禁用流程

## 验收标准

1. ✅ www.moneradigital.com/api/auth/2fa/setup 返回JSON响应（非405）
2. ✅ 2FA setup可以生成QR码和备用码
3. ✅ 2FA enable功能正常工作
4. ✅ 2FA disable功能正常工作
5. ✅ 错误处理返回正确的HTTP状态码和错误消息
6. ✅ 前端不再出现JSON解析错误
7. ✅ 完整的2FA流程从启用到禁用正常工作

## 相关文档

- [DEPLOYMENT_GUIDE.md](./DEPLOYMENT_GUIDE.md) - 需要更新多域名部署说明
- [API文档](./docs/openapi.yaml) - 验证2FA端点定义
- [错误处理文档](./docs/audit/2fa-handler-audit.md) - 参考2FA错误处理模式

## 审批状态

- [x] 问题根因确认 (API端点缺失)
- [x] 解决方案设计完成
- [ ] API端点部署完成
- [ ] vercel.json配置更新完成
- [ ] 前端配置更新完成
- [ ] 测试验证通过
- [ ] 用户验收确认

---

**备注**: 这是关键的功能缺失bug，需要立即部署API端点以恢复2FA功能。临时解决方案是使用现有可用的后端 monera-digital--gyc567.replit.app。