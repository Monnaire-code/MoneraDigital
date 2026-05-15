# 地址验证 500 错误修复报告

## 问题描述

用户 `gyc567@gmail.com` 在验证地址时遇到 500 错误：
```
POST https://www.moneradigital.com/api/addresses/12/verify 500 (Internal Server Error)
```

## 根本原因

在 `internal/services/twofa_service.go` 的 `getSecret` 函数中，当数据库中的 `two_factor_secret` 列值为 NULL 时：

1. `Scan` 方法将 `encryptedSecret` 设置为空字符串（而不是返回 `sql.ErrNoRows`）
2. 随后尝试解密空字符串导致错误
3. 这个错误被传播到 handler，导致 500 响应

## 修复内容

### 1. 修复 `getSecret` 函数
**文件**: `internal/services/twofa_service.go:191-210`

```go
// 修复前
var encryptedSecret string
err := s.DB.QueryRow(query, userID).Scan(&encryptedSecret)
if err == sql.ErrNoRows {
    return "", fmt.Errorf("2FA not set up")
}

// 修复后
var encryptedSecret sql.NullString
err := s.DB.QueryRow(query, userID).Scan(&encryptedSecret)
if err == sql.ErrNoRows {
    return "", fmt.Errorf("2FA not set up")
}
if !encryptedSecret.Valid || encryptedSecret.String == "" {
    return "", fmt.Errorf("2FA not set up")
}
```

### 2. 修复 `getBackupCodes` 函数
**文件**: `internal/services/twofa_service.go:212-230`

同样使用 `sql.NullString` 处理可能为 NULL 的备份码。

### 3. 增强错误日志
**文件**: `internal/handlers/handlers.go:377-432`

添加了详细的错误日志，便于后续诊断问题：
```go
if err != nil {
    fmt.Printf("[VerifyAddress] 2FA verification error for user %d: %v\n", userID, err)
    c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify 2FA: " + err.Error()})
    return
}
```

## 测试

### 单元测试
```bash
go test ./internal/services/... -v
# 结果: PASS
go test ./internal/handlers/... -v  
# 结果: PASS
```

### 构建测试
```bash
go build -o server ./cmd/server
# 结果: 成功
npm run build
# 结果: 成功
```

## 部署

- **部署时间**: 2026-01-28
- **部署环境**: 生产环境
- **部署状态**: ✅ 成功
- **域名**: https://www.moneradigital.com

## 验证步骤

用户需要验证修复是否成功：

1. 登录系统
2. 进入地址管理页面
3. 点击"验证"按钮
4. 在 2FA 验证对话框中输入 Google Authenticator 的 6 位验证码
5. 验证应该成功，不再返回 500 错误

## 监控

如果问题仍然存在，请检查：
1. 浏览器开发者工具中的网络请求响应
2. 后端日志中的详细错误信息
3. 用户数据库中的 2FA 设置状态

## 后续建议

1. **数据库检查**: 确认用户 `gyc567@gmail.com` 的 `two_factor_secret` 是否正确存储
2. **加密密钥**: 确认 `ENCRYPTION_KEY` 环境变量是否正确设置
3. **2FA 重置**: 如果 secret 损坏，考虑为用户提供重置 2FA 的选项
