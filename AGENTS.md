# AGENTS.md

> Monera Digital 项目代理编码指南
> 机构级数字资产平台，提供安全的静态金融和借贷解决方案

## 项目概览

**Monera Digital** 是一个全栈应用，遵循严格的**后端业务逻辑**架构要求。

## 技术栈

- **Frontend**: React 18, TypeScript, Vite, Tailwind CSS, Shadcn UI (Radix Primitives)
- **Backend**: Golang (Go) - **Mandatory for all interfaces, database access, and operations.**
- **Database**: PostgreSQL (Neon); production schema is owned by Go migrations under `internal/migration/migrations/`
- **External Core System**: Monnaire Core API (Account Management)
- **State/Cache**: PostgreSQL is authoritative; Redis/Upstash is optional acceleration where the code provides a fallback
- **Testing**: Vitest (Unit/Integration), Playwright (E2E)
- **Language**: TypeScript (Frontend), Go (Backend)

## Architecture Overview

```
Frontend (React) → API Routes (Vercel) → Go Backend → Core API / Database
```

### Layer Responsibilities

| Layer | Technology | Responsibilities |
|-------|------------|------------------|
| **Frontend** | React + TypeScript | UI rendering, form validation, API calls only |
| **API Routes** | Vercel Serverless | Request routing, auth validation, proxy to backend |
| **Go Backend** | Go (internal/) | Business logic, database operations, Core API integration |
| **Core API** | External/Mock | Core account management, KYC, compliance |
| **Database** | PostgreSQL (Neon) | User data, transactions, application state |

## Architecture Principles

### 1. Frontend-Only API Calls
**⚠️ CRITICAL**: Frontend code **MUST NOT** directly access the database or Core API.

```
✅ Correct:  Frontend → /api/* → Go Backend → Database/Core API
❌ Forbidden: Frontend → Direct Database Access
❌ Forbidden: Frontend → Direct Core API Access
```

### 2. Service Layer Pattern

Frontend services (`src/lib/*-service.ts`) **only** make HTTP API calls:

```typescript
// ✅ Correct: Service calls local API
export class UserService {
  static async getUser() {
    const response = await fetch('/api/users/me', {
      headers: { 'Authorization': `Bearer ${token}` }
    });
    return response.json();
  }
}

// ❌ Forbidden: Direct database access
export class UserService {
  static async getUser() {
    return db.select().from(users);  // Never do this!
  }
}

// ❌ Forbidden: Direct Core API access
export class UserService {
  static async getUser() {
    return fetch('https://core-api.monera.com/users');  // Never do this!
  }
}
```

### 3. Go Backend Responsibilities

Go backend (`internal/`) is the **only** layer that can:
- Access PostgreSQL database
- Call Monnaire Core API
- Implement business logic
- Handle authentication/authorization

```go
// ✅ Correct: Go Backend calls Core API
func (s *AuthService) createCoreAccount(userID int, email string) (string, error) {
    coreAPIURL := os.Getenv("Monnaire_Core_API_URL") + "/accounts/create"
    resp, err := http.Post(coreAPIURL, "application/json", body)
    // ...
}

// ✅ Correct: Go Backend accesses database
func (s *AuthService) CreateUser(req models.RegisterRequest) (*models.User, error) {
    _, err := s.DB.Exec("INSERT INTO users ...", req.Email, hashedPassword)
    // ...
}
```

### 4. Data Flow Example (User Registration)

```
1. Frontend (React)
   POST /api/auth/register
   { email, password }
   
2. API Routes (Vercel)
   Validate JWT → Forward to Go Backend
   
3. Go Backend (internal/services/auth.go)
   a. Create user in PostgreSQL
   b. Call Monnaire Core API to create core account
      POST Monnaire_Core_API_URL/accounts/create
   c. Return { user, token }
   
4. Frontend
   Receive response, update UI
```

---

## 构建、代码检查和测试命令

### 核心命令
```bash
npm install                    # 安装依赖
npm run dev                    # 开发服务器 (Vite) - 端口: 5001
npm run build                  # 生产构建
npm run build:dev              # 开发模式构建
npm run preview                # 预览生产构建
npm run lint                   # ESLint 代码检查
npm run lint -- --fix          # 自动修复代码检查问题
```

### 测试命令 (关键)
```bash
npm test                           # 运行所有测试 (Vitest)
npm test src/lib/auth-service.test.ts  # 运行特定测试文件
npm test src/__tests__/           # 运行 __tests__ 目录下的测试
npm test -- --coverage            # 运行测试并生成覆盖率报告
npm test -- --testNamePattern="login"  # 运行匹配模式的测试
npm run test:ui                   # Vitest UI 界面
npm run test:e2e                  # E2E 测试 (Playwright)
```

### 数据库命令
```bash
go run ./cmd/migrate -dry-run
EXPECTED_MIGRATION_CEILING=<version> go run ./cmd/migrate -exact-version <version>
```

- Stage/production 只允许执行本次发布的受控单版本迁移，不得使用默认全量 pending 入口。
- `npm run db:push` / `npm run db:generate` 属于历史前端工具，不是共享生产 schema 的发布方式。

### 辅助命令
```bash
npm run favicon                   # 生成网站图标
```

## 架构：后端业务逻辑架构 (强制要求)

**所有 API 调用流程**: 前端 → Vercel API (仅代理) → Go Backend (业务逻辑)

- **前端 API 路由 (`api/`)**: 纯 HTTP 代理 - **禁止包含业务逻辑**
- **Go 后端 (`internal/`)**: **所有业务逻辑、数据库操作、认证逻辑**
- **前端服务层 (`src/lib/`)**: **禁止直接数据库访问或认证逻辑**
- **允许的例外**: 简单 UI 工具和表单验证

## 代码风格指南

### 导入规则
- **使用绝对导入** - `@/` 前缀用于内部模块
- **使用相对导入** - 仅用于同目录文件
- **导入顺序**: 内置模块 → 第三方模块 → 绝对导入 (`@/`) → 相对导入

### TypeScript 规则
- **显式类型定义** - 参数和返回值必须显式声明类型
- **禁止使用 `any`** - 使用 `unknown` 或proper类型
- **使用 `zod`** - 进行运行时验证
- **使用接口定义对象** - 使用类型别名定义联合类型
- **TypeScript 配置**:
  - 严格模式: `false` (便于开发)
  - 路径别名: `@/*` → `./src/*`

### 代码格式化
- **行长度**: 100 字符软限制
- **缩进**: 2 空格
- **分号**: 始终使用
- **引号**: 字符串使用双引号，JSX 使用单引号
- **尾随逗号**: 多行对象/数组使用

### 命名约定
| Type | Convention | Examples |
|------|------------|----------|
| Variables | camelCase | `userId`, `isLoading` |
| Constants | UPPER_SNAKE_CASE | `JWT_SECRET`, `MAX_RETRY_COUNT` |
| JSON Fields | camelCase | `userId`, `accessToken`, `requires2FA` |
| Database Columns | snake_case | `user_id`, `created_at` |
| Functions | camelCase (verb-first) | `getUser()`, `fetchWithdrawalHistory()` |
| Classes/Interfaces | PascalCase | `AuthService`, `WithdrawalAddress` |
| Components | PascalCase | `DashboardLayout`, `WithdrawPage` |
| Files | kebab-case | `auth-service.ts`, `withdrawal-history.tsx` |
| Database tables | snake_case | `withdrawal_addresses`, `lending_positions` |

**Critical Rule**: All API request/response JSON fields MUST use camelCase (`userId`, not `user_id`).

### JSON Field Naming Convention

| Layer | Format | Example |
|-------|--------|---------|
| **API Request/Response** | camelCase | `userId`, `createdAt`, `walletAddress` |
| **Database Columns** | snake_case | `user_id`, `created_at`, `wallet_address` |
| **TypeScript Interfaces** | camelCase | `userId: number` |
| **Go Struct JSON Tags** | camelCase | `json:"userId"` |
| **Go Struct DB Tags** | snake_case | `db:"user_id"` |

```go
// Go struct - JSON camelCase, DB snake_case
type WithdrawalAddress struct {
    ID            int          `json:"id" db:"id"`
    UserID        int          `json:"userId" db:"user_id"`
    WalletAddress string       `json:"walletAddress" db:"wallet_address"`
    ChainType     string       `json:"chainType" db:"chain_type"`
    AddressAlias  string       `json:"addressAlias" db:"address_alias"`
    Verified      bool         `json:"verified" db:"verified"`
    CreatedAt     time.Time    `json:"createdAt" db:"created_at"`
    VerifiedAt    sql.NullTime `json:"verifiedAt,omitempty" db:"verified_at"`
}
```

```typescript
// TypeScript - always camelCase
interface WithdrawalAddress {
  id: number;
  userId: number;
  walletAddress: string;
  chainType: "BTC" | "ETH" | "USDC" | "USDT";
  addressAlias: string;
  verified: boolean;
  createdAt: string;
  verifiedAt: string | null;
}
```

**⚠️ WARNING**: Never mix snake_case and camelCase in API JSON. All API communication MUST use camelCase.

### 错误处理
- **从不遗漏未捕获错误** - 始终处理或重新抛出
- **禁止空 catch 块**
- **结构化日志记录** - 包含上下文信息
- **抛出 Error 实例** - 不抛出字符串

### 不可变性 (关键)
始终创建新对象，**从不直接修改**。使用展开运算符或 structuredClone。

```typescript
// 错误：直接修改
function updateUser(user, name) {
  user.name = name;
  return user;
}

// 正确：不可变方式
function updateUser(user, name) {
  return { ...user, name };
}
```

## 文件组织

**小文件优先于大文件**：
- 高内聚，低耦合
- 典型 200-400 行，单文件建议不超过 2000 行；超限时需评估职责是否过于分散，不做机械拆分
- 函数保持精简 (< 50 行)
- 嵌套层次最多 4 层

## 目录结构

```
src/
├── api/                      # Vercel 无服务器函数 (仅 HTTP 代理)
├── components/
│   ├── ui/                   # Shadcn/Radix UI 组件
│   └── DashboardLayout.tsx   # 布局组件
├── lib/                      # 仅 UI 工具和表单验证
├── pages/                    # 路由页面 (React Router)
├── hooks/                    # 自定义 React hooks
├── __tests__/                # 测试文件
└── i18n/                     # 国际化

internal/
├── companyfund/              # 公司资金采集、幂等合并、风险、估值和对账
├── handlers/                 # Go HTTP handlers
├── migration/migrations/     # 共享数据库的受控 Go migrations
└── wallet/                   # 客户钱包和充值流水线
```

## 组件模式
- **属性接口**: `ComponentNameProps` 后缀
- **页面组件**: 默认导出
- **可复用组件**: 命名导出
- **使用 Shadcn UI**: 来自 `@/components/ui/*`
- **使用图标**: `lucide-react`
- **使用通知**: `sonner`

---

## 测试要求

- **覆盖率**: 新增或修改的业务逻辑必须完整覆盖其行为分支，目标代码覆盖率以 **100%** 为目标
- **测试方法**: 测试驱动开发 (TDD) - 先写测试
- **测试类型**: 单元测试 (Vitest), 集成测试, E2E 测试 (Playwright)
- **测试文件**: 使用 `.test.ts` 后缀，与源文件放在一起

### TDD 工作流
1. 先写测试 (红色)
2. 运行测试 - 应该失败
3. 编写最小实现 (绿色)
4. 运行测试 - 应该通过
5. 重构 (改进)
6. 验证新增/修改行为分支已完整覆盖

### 测试文件位置
- **单元测试**: `src/lib/*/*.test.ts`, `src/__tests__/*`
- **API 测试**: `api/*/*.test.ts`
- **集成测试**: `tests/*-integration.test.ts`
- **E2E 测试**: `tests/*.spec.ts`

## 安全指南

提交前必须检查：
- [ ] 无硬编码密钥 (API 密钥、密码、令牌)
- [ ] 所有用户输入已验证
- [ ] SQL 注入防护 (参数化查询)
- [ ] XSS 防护 (HTML 清理)
- [ ] CSRF 保护启用
- [ ] 认证/授权验证
- [ ] 公网或高风险端点已限流，或有明确的不限流理由
- [ ] 错误消息不泄露敏感数据

**密钥管理**: 始终使用 `process.env.VARIABLE_NAME`，绝不硬编码。

## 常用模式

### API 响应格式
```typescript
interface ApiResponse<T> {
  success: boolean
  data?: T
  error?: string
  meta?: {
    total: number
    page: number
    limit: number
  }
}
```

### 自定义 Hook 模式
```typescript
export function useDebounce<T>(value: T, delay: number): T {
  const [debouncedValue, setDebouncedValue] = useState<T>(value)
  useEffect(() => {
    const handler = setTimeout(() => setDebouncedValue(value), delay)
    return () => clearTimeout(handler)
  }, [value, delay])
  return debouncedValue
}
```

## 代理工作流

- 小型、本地、边界清晰的任务直接执行，不为了形式引入额外编排。
- 涉及新功能或 Bug 修复时使用 TDD；需要审查、诊断或端到端验收时，使用当前已安装的对应 skill。
- 只有独立子任务确有吞吐收益时才并行；有依赖的操作顺序执行。

---

## 开始任务前的澄清协议 (强制)

**在我们开始之前，你必须遵循以下澄清流程：**

### 1. 理解问题 (你的责任)

先用你自己的话说说你理解的：
- **我要解决什么问题** - 描述问题的本质
- **交付物是什么** - 描述预期产出
- **你做了哪些假设** - 标出你不确定但自行假设的地方

如果你觉得有更好的技术方案，直接在这一步说出来，我来决定。

### 2. 向我提问 (每次最多 3 个)

在开始写代码之前，你必须向我提问，直到你对以下三点有 **100% 的把握**：

1. **我真正想要达成的目标是什么** —— 而不是字面上说的
2. **有哪些我没说出口的约束或偏好** —— 如技术栈、性能要求、需要兼容的现有代码、不能动的部分
3. **你计划怎么实现** —— 核心思路是什么、为什么选这个方案

### 3. 等待我的明确许可

**在你没有得到我明确的「可以开始」之前：**
- ❌ 不要写任何代码
- ❌ 不要修改任何文件
- ❌ 不要创建任何计划或文档

只有当我明确说「可以开始」后，你才能开始实现。

---

## 数据库（Go Migrations）
- **表名**: `snake_case`，复数形式 (`users`, `withdrawal_addresses`)
- **列名**: `snake_case` (`created_at`, `user_id`)
- **外键**: `tableName_id` 后缀
- **Schema 所有权**: 生产共享 schema 只由 `internal/migration/migrations/` 中的 Go migration 修改
- **发布边界**: Stage/production 只执行本次新增的受控单版本迁移，不重放历史全量迁移
- **本地测试**: 使用当前环境的 `DATABASE_URL`，不强制 `_test` 数据库命名；测试数据由用例自行隔离和清理

## Git 工作流

**提交格式**: 使用 Lore Commit Protocol；首行说明为什么做此变更，并根据实际情况记录 `Constraint` / `Rejected` / `Confidence` / `Scope-risk` / `Directive` / `Tested` / `Not-tested` trailers。

**功能实现流程**:
1. 明确领域边界、验收标准和依赖关系
2. TDD 方法（先红后绿）
3. 代码审查和针对性回归验证
4. 提交推送与详细消息

### 统一 Serverless Function 架构（强制）

**重要**: Vercel Hobby 计划限制最多 **12 个 Serverless Functions**。
项目必须使用统一的路由架构，所有 API 请求通过单一入口处理。

**正确的文件结构**:
```
api/
├── [...route].ts          # 统一路由处理器（唯一 Serverless Function）
└── __route__.test.ts      # 路由测试
```

**禁止的文件结构**（会导致超过 12 个函数限制）:
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

### 统一路由配置

所有路由在 `api/[...route].ts` 的 `ROUTE_CONFIG` 中集中配置：

```typescript
const ROUTE_CONFIG: Record<string, RouteConfig> = {
  // Auth endpoints
  'POST /auth/login': { requiresAuth: false, backendPath: '/api/auth/login' },
  'POST /auth/register': { requiresAuth: false, backendPath: '/api/auth/register' },
  'GET /auth/me': { requiresAuth: true, backendPath: '/api/auth/me' },
  
  // 2FA endpoints
  'POST /auth/2fa/setup': { requiresAuth: true, backendPath: '/api/auth/2fa/setup' },
  'POST /auth/2fa/enable': { requiresAuth: true, backendPath: '/api/auth/2fa/enable' },
  'POST /auth/2fa/disable': { requiresAuth: true, backendPath: '/api/auth/2fa/disable' },
  'GET /auth/2fa/status': { requiresAuth: true, backendPath: '/api/auth/2fa/status' },
  'POST /auth/2fa/verify-login': { requiresAuth: false, backendPath: '/api/auth/2fa/verify-login' },
  'POST /auth/2fa/skip': { requiresAuth: false, backendPath: '/api/auth/2fa/skip' },
  
  // Address endpoints
  'GET /addresses': { requiresAuth: true, backendPath: '/api/addresses' },
  'POST /addresses': { requiresAuth: true, backendPath: '/api/addresses' },
  
  // ... 其他路由
};
```

### 添加新 API 端点的步骤

1. **在 `ROUTE_CONFIG` 中添加配置**（不需要创建新文件）:
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

3. **在 `api/__route__.test.ts` 中添加测试**:
```typescript
it('should route POST /new/endpoint correctly', async () => {
  // 测试代码
});
```

### 动态路由支持

支持动态路由参数（如 `/addresses/:id`）:

```typescript
// Handle dynamic address routes: /addresses/123, /addresses/123/verify, etc.
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

### 旧的 API 路由模式（已废弃）

以下模式不再使用，仅作为参考：

```typescript
// ❌ 废弃：每个端点一个文件
// api/auth/login.ts
import type { VercelRequest, VercelResponse } from '@vercel/node';
export default async function handler(req: VercelRequest, res: VercelResponse) {
  // ...
}
```

---

## 开发基本原则 (强制要求)

**所有代码修复和新功能开发必须遵循以下原则：**

### 1. 设计原则
- **KISS 原则**: 保持代码简洁、干净
- **高内聚、低耦合**: 用精简的设计模式
- **单一职责**: 每个模块只做一件事

### 2. 测试要求
- **覆盖率**: 新增或修改的业务逻辑必须完整覆盖其行为分支，目标代码覆盖率以 **100%** 为目标
- **TDD 方法**: 先写测试，再写实现代码
- **回归测试**: 修复后必须验证不影响其他功能

### 3. 变更原则
- **隔离性**: 修改不能影响无关功能
- **最小化**: 只改必要的代码，不做大规模重构

### 4. 提案流程
- **OpenSpec**: 新功能和 Bug 修复不强制使用 OpenSpec；需要耐久规格时由用户或计划明确指定
- **任何代码变更**: 保持目标清晰、改动最小，完成时说明验证证据

---

## 代码质量检查清单

完成任务前检查：
- [ ] 代码可读性好，命名清晰
- [ ] 函数精简 (< 50 行)
- [ ] 文件职责集中（建议 < 2000 行；不做无业务收益的机械拆分）
- [ ] 无深层嵌套 (> 4 层)
- [ ] 错误处理完善
- [ ] 无 console.log 语句
- [ ] 无硬编码值
- [ ] 使用不可变模式

---

## Go 后端约定 (`internal/`)
- **包名**: 小写，无下划线，无复数
- **文件**: 小写 + 下划线；建议 ≤ 2000 行，超限时优先按真实领域责任拆分
- **Context**: 第一个参数，仅用于 I/O
- **错误**: 始终处理，业务错误不 panic
- **工具**: 提交前运行 `gofmt -w .` 和 `go vet ./...`

---

## Quick Reference

| Category | Rule |
|----------|------|
| Imports | Absolute `@/` for internal, relative for co-located |
| Types | Explicit, no `any`, use Zod for validation |
| Errors | Always handle, never empty catch, log with context |
| Components | Props interface, default export pages |
| Services | Static methods, Zod schema, structured logging |
| Files | kebab-case for non-components, PascalCase for components |
| Tests | Run single file with `npm test -- <path>` |

---

## Key Files to Know

| File | Purpose |
|------|---------|
| `internal/migration/migrations/` | Go database migrations (backend only) |
| `src/lib/auth-service.ts` | Auth API client (frontend) |
| `src/lib/withdrawal-service.ts` | Withdrawal API client (frontend) |
| `api/[...route].ts` | Unified API router (Vercel) |
| `vite.config.ts` | Build configuration |
| `eslint.config.js` | Linting rules |
| `tailwind.config.ts` | Tailwind CSS configuration |

**Note**: Database schema is defined in Go backend (`internal/migration/migrations/`), not in frontend.

---

## Developer Environment Tips

- **Setup**: Copy `.env.example` to `.env`, then run `npm install`
- **Ports**: Vite dev server uses 5001; project scripts and deployed Go services use 8081 unless an explicit `PORT` override is provided
- **Database**: Database schema is managed by Go backend (`internal/migration/`)
- **Tests**: Run `npm test` before committing

**Note**: Frontend does NOT directly access database. All database operations go through Go backend API.
