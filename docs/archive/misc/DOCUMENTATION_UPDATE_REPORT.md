# 文档更新报告 - Core API 架构统一

**日期**: 2026-01-28  
**任务**: 更新所有文档以反映新架构：Frontend → API → Go Backend → Core API  
**状态**: ✅ 完成

---

## 架构概述

### 新架构流程

```
Frontend (React) → API Routes (Vercel) → Go Backend → Core API / Database
```

### 各层职责

| 层级 | 技术 | 职责 | 禁止事项 |
|------|------|------|---------|
| **Frontend** | React + TypeScript | UI 渲染、表单验证、调用 `/api/*` | 直接访问数据库、直接访问 Core API |
| **API Routes** | Vercel Serverless | 路由、认证校验、代理到 Go Backend | 业务逻辑 |
| **Go Backend** | Go (internal/) | 业务逻辑、数据库操作、Core API 调用 | 无 - 这是核心层 |
| **Core API** | 外部/Mock | 核心账户管理、KYC、合规 | 被前端直接调用 |
| **Database** | PostgreSQL | 用户数据、交易数据 | 被前端直接访问 |

---

## 更新的文档

### 1. AGENTS.md

**新增/修改内容**:
- ✅ 新增 **Architecture Overview** 章节
- ✅ 新增 **Layer Responsibilities** 表格
- ✅ 扩展 **Architecture Principles**:
  - Frontend-Only API Calls 规则
  - Service Layer Pattern 详细说明
  - Go Backend Responsibilities 说明
  - Data Flow Example (用户注册流程)
- ✅ 更新 **Tech Stack** - 添加 External Core System
- ✅ 更新 **Directory Structure** - 移除 `src/db/`
- ✅ 更新 **Key Files to Know** - 添加 `internal/migration/migrations/`

### 2. CLAUDE.md

**新增/修改内容**:
- ✅ 新增 **Architecture** 章节，包含架构图
- ✅ 新增 **Critical Architecture Rules**:
  - Frontend ONLY calls `/api/*` endpoints
  - Go Backend handles ALL external integrations
  - Service Layer Pattern 说明
- ✅ 更新 **Project Overview** - 添加 External Core System
- ✅ 更新 **High-Level Architecture**:
  - 更新 Services 说明（API clients only）
  - 更新 Go Backend 结构说明
  - 添加 External Systems 说明
- ✅ 移除过时的 Database (Drizzle) 说明

### 3. GEMINI.md

**新增/修改内容**:
- ✅ 新增 **Architecture** 章节，包含架构图
- ✅ 新增 **Critical Architecture Rules**:
  - Frontend ONLY calls `/api/*`
  - Go Backend handles ALL external integrations
  - Service Layer Pattern
- ✅ 更新 **Project Overview** - 添加 External Core System
- ✅ 更新 **Key Directory Structure** - 添加 Go Backend 结构
- ✅ 更新 **Architecture & Patterns**:
  - 更新 Service Layer Pattern 说明
  - 新增 Go Backend Structure 说明
  - 新增 External Integrations 说明
- ✅ 更新 **Important Context (Recent Changes)** - 添加 Architecture Unification
- ✅ 更新 **New Feature Development Rules** - 强调 Core API 集成

---

## 架构规则总结

### 1. Frontend 规则

```typescript
// ✅ 正确：调用本地 API
const response = await fetch('/api/withdrawals', {
  method: 'POST',
  headers: { 'Authorization': `Bearer ${token}` },
  body: JSON.stringify(data)
});

// ❌ 禁止：直接访问数据库
await db.insert(withdrawals).values(data);

// ❌ 禁止：直接调用 Core API
await fetch('https://core-api.monera.com/accounts/create');
```

### 2. Go Backend 规则

```go
// ✅ 正确：调用 Core API
func (s *AuthService) createCoreAccount(userID int, email string) (string, error) {
    coreAPIURL := os.Getenv("Monnaire_Core_API_URL") + "/accounts/create"
    resp, err := http.Post(coreAPIURL, "application/json", body)
    // ...
}

// ✅ 正确：访问数据库
func (s *AuthService) CreateUser(req models.RegisterRequest) (*models.User, error) {
    _, err := s.DB.Exec("INSERT INTO users ...", req.Email, hashedPassword)
    // ...
}
```

### 3. 数据流示例

**用户注册**:
```
1. Frontend
   POST /api/auth/register
   { email, password }
   
2. API Routes (Vercel)
   验证 JWT → 转发到 Go Backend
   
3. Go Backend (internal/services/auth.go)
   a. 创建用户到 PostgreSQL
   b. 调用 Monnaire Core API
      POST Monnaire_Core_API_URL/accounts/create
   c. 返回 { user, token }
```

---

## 生成的文档

| 文档 | 路径 | 说明 |
|------|------|------|
| 架构 V2 提案 | `openspec/architecture-v2-core-api-integration.md` | 完整的架构说明文档 |
| 更新报告 | `DOCUMENTATION_UPDATE_REPORT.md` | 本文档 |

---

## 验证结果

### 构建测试
```bash
$ npm run build
✓ built in 1.91s
```
**状态**: ✅ 前端构建成功

### Go 测试
```bash
$ go test ./internal/...
ok  monera-digital/internal/account
ok  monera-digital/internal/handlers
ok  monera-digital/internal/migration/migrations
ok  monera-digital/internal/repository/postgres
ok  monera-digital/internal/scheduler
ok  monera-digital/internal/services
```
**状态**: ✅ 所有测试通过

---

## 关键变化

### 移除的内容
- Drizzle ORM 相关文件和依赖
- 前端直接数据库访问代码
- 过时的 `src/db/` 目录说明

### 新增的内容
- Core API 集成说明
- 三层架构详细说明
- 各层职责和禁止事项
- 数据流示例

### 强调的重点
1. **Frontend** - 只调用 `/api/*`，不直接访问数据库或 Core API
2. **API Routes** - 纯代理，无业务逻辑
3. **Go Backend** - 唯一可以访问数据库和 Core API 的层
4. **Core API** - 外部系统，由 Go Backend 调用

---

## 后续建议

1. **团队培训** - 确保所有开发人员理解新架构
2. **代码审查** - 检查是否有违反架构规则的代码
3. **监控** - 确保 Core API 集成正常工作
4. **文档同步** - 保持 README 和其他文档同步更新

---

## 总结

所有主要文档已更新以反映新架构：

✅ AGENTS.md - 更新了架构说明和规则  
✅ CLAUDE.md - 更新了架构图和各层职责  
✅ GEMINI.md - 更新了架构规则和最佳实践  
✅ 前端构建成功  
✅ Go 测试全部通过  

**新架构**: Frontend → API → Go Backend → Core API/Database
