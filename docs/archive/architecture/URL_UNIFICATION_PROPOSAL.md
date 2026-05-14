# URL配置统一化提案

## 提案信息
- **创建日期**: 2026-01-25
- **创建人**: Sisyphus
- **状态**: 待实施
- **优先级**: 高
- **标签**: architecture, configuration, deployment

## 问题陈述

### 现状分析
当前系统存在两套不同的URL配置机制：

1. **前端服务层** (`src/lib/api-client.ts`): 使用 `VITE_API_BASE_URL`
2. **Vercel API代理层** (`api/auth/*`): 使用 `GO_BACKEND_URL`

### 问题影响
- **配置分散**: 需要维护两套不同的环境变量
- **部署复杂**: 生产环境需要配置多个URL变量
- **架构不一致**: 违反单一配置源原则
- **维护困难**: URL变更需要修改多处配置

## 解决方案

### 核心原则
统一使用 `VITE_API_BASE_URL` 作为唯一的API基础URL配置源。

### 实施计划

#### 阶段1: 配置统一化
1. 更新 `.env.example`，移除 `GO_BACKEND_URL` 相关配置
2. 更新所有Vercel API代理文件，使用 `VITE_API_BASE_URL`
3. 更新部署文档和环境变量说明

#### 阶段2: 代码重构
文件修改清单：
- `api/auth/me.ts` - 替换 `GO_BACKEND_URL` 为 `process.env.VITE_API_BASE_URL` ✅
- `api/auth/login.ts` - 替换 `GO_BACKEND_URL` 为 `process.env.VITE_API_BASE_URL` ✅
- `api/auth/register.ts` - 替换 `GO_BACKEND_URL` 为 `process.env.VITE_API_BASE_URL` ✅
- `api/auth/2fa/verify-login.ts` - 替换 `GO_BACKEND_URL` 为 `process.env.VITE_API_BASE_URL` ✅
- `.env.example` - 移除 `GO_BACKEND_URL` 相关配置说明

#### 阶段3: 验证和测试
1. 本地开发环境测试
2. 生产环境部署验证
3. 所有API端点功能测试

### 配置模式

#### 开发环境
```bash
# 不设置VITE_API_BASE_URL，使用Vite代理
# 前端通过 /api -> http://localhost:8081
```

#### 生产环境
```bash
# 设置VITE_API_BASE_URL指向实际后端
VITE_API_BASE_URL=https://monera-digital-backend.com
```

## 实施影响

### 优势
- **简化配置**: 只需维护一个URL环境变量
- **统一架构**: 前后端使用相同的配置源
- **降低复杂度**: 减少部署时的配置错误风险
- **提升可维护性**: URL变更只需修改一处

### 风险评估
- **低风险**: 仅涉及环境变量名称变更
- **向后兼容**: 需要更新部署脚本和环境配置
- **测试覆盖**: 需要验证所有API代理功能

## 验收标准

1. ✅ 所有API代理文件使用 `VITE_API_BASE_URL`
2. ✅ `.env.example` 只包含 `VITE_API_BASE_URL` 配置
3. ✅ 本地开发环境正常工作
4. ✅ 生产环境部署成功
5. ✅ 所有API端点响应正常

## 实施时间线

- **预计完成时间**: 1小时
- **关键里程碑**: 
  - 配置文件更新 (15分钟)
  - 代码重构 (20分钟)
  - 测试验证 (25分钟)

## 相关文档

- [DEPLOYMENT_GUIDE.md](./DEPLOYMENT_GUIDE.md) - 需要更新
- [AGENTS.md](./AGENTS.md) - 可能需要提及配置变更

## 审批状态

- [x] 技术方案确认
- [x] 风险评估完成
- [ ] 实施完成
- [ ] 测试验证通过