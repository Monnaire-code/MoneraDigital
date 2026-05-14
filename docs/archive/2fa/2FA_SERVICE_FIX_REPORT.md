# 2FA Service 未初始化错误修复报告

## 问题

用户 `gyc567@gmail.com` 在验证地址时遇到错误：
```
Failed to verify 2FA: two factor service not initialized
```

## 根本原因

在 `internal/container/container.go` 中，`TwoFAService` 的注入发生在它创建之前：

```go
// 问题代码（修复前）
c.AuthService = services.NewAuthService(db, jwtSecret)

// 此时 c.TwoFAService 还是 nil！
if c.TwoFAService != nil {
    c.AuthService.SetTwoFactorService(c.TwoFAService)  // 不会执行
}

// TwoFAService 在这里才被创建
for _, opt := range opts {
    opt(c)
}
```

## 修复

调整依赖注入顺序：

```go
// 修复后代码
c.AuthService = services.NewAuthService(db, jwtSecret)

// 先创建 TwoFAService
for _, opt := range opts {
    opt(c)
}

// 再注入（现在 TwoFAService 已初始化）
if c.TwoFAService != nil {
    c.AuthService.SetTwoFactorService(c.TwoFAService)
}
```

## 文件变更

### 修改文件
1. `internal/container/container.go` - 调整依赖注入顺序

### 新增文件
1. `internal/container/container_test.go` - 单元测试

## 测试

### 新增测试
```bash
cd /Users/eric/dreame/code/MoneraDigital && go test ./internal/container/... -v
```

结果：
```
=== RUN   TestNewContainer_WithEncryption_TwoFAServiceInjected
--- PASS
=== RUN   TestWithEncryption_CreatesTwoFAService
--- PASS
=== RUN   TestContainer_Verify_IncludesTwoFAService
--- PASS
PASS
ok      monera-digital/internal/container
```

### 回归测试
```bash
cd /Users/eric/dreame/code/MoneraDigital && go test ./internal/...
```

结果：**全部通过**

## 设计原则

1. **KISS**: 只调整代码执行顺序，不增加复杂度
2. **高内聚低耦合**: 保持 Container 的单一职责
3. **测试覆盖**: 新增 3 个单元测试
4. **隔离性**: 只修改注入顺序，不影响其他功能

## 部署

1. 构建后端：`go build -o server ./cmd/server`
2. 部署到 Replit
3. 验证修复：用户应能正常使用 2FA 验证地址
