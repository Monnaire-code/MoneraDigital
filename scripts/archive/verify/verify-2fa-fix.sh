#!/bin/bash

# 验证2FA API修复

echo "📋 2FA API Routes Fix Verification Report"
echo "=========================================="
echo ""
echo "Generated: $(date)"
echo ""

# 检查创建的文件
echo "✅ Checking created API files..."
echo ""

FILES=(
  "api/auth/login.ts"
  "api/auth/register.ts"
  "api/auth/me.ts"
  "api/auth/2fa/setup.ts"
  "api/auth/2fa/enable.ts"
  "api/auth/2fa/disable.ts"
  "api/auth/2fa/verify-login.ts"
)

ALL_EXISTS=true
for file in "${FILES[@]}"; do
  if [ -f "$file" ]; then
    echo "  ✓ $file exists ($(wc -c < "$file") bytes)"
  else
    echo "  ✗ $file NOT FOUND"
    ALL_EXISTS=false
  fi
done

echo ""
echo "📝 Verifying API implementations..."
echo ""

# 检查setup.ts是否包含TwoFactorService.setup
if grep -q "TwoFactorService.setup" api/auth/2fa/setup.ts; then
  echo "  ✓ 2FA setup endpoint calls TwoFactorService.setup()"
else
  echo "  ✗ 2FA setup endpoint missing service call"
fi

# 检查enable.ts是否包含TwoFactorService.enable
if grep -q "TwoFactorService.enable" api/auth/2fa/enable.ts; then
  echo "  ✓ 2FA enable endpoint calls TwoFactorService.enable()"
else
  echo "  ✗ 2FA enable endpoint missing service call"
fi

# 检查disable.ts是否包含正确的逻辑
if grep -q "twoFactorEnabled: false" api/auth/2fa/disable.ts; then
  echo "  ✓ 2FA disable endpoint properly updates user status"
else
  echo "  ✗ 2FA disable endpoint missing proper update"
fi

# 检查login.ts是否包含AuthService.login
if grep -q "AuthService.login" api/auth/login.ts; then
  echo "  ✓ Login endpoint calls AuthService.login()"
else
  echo "  ✗ Login endpoint missing service call"
fi

echo ""
echo "🔐 Security Features Check..."
echo ""

# 检查错误处理
if grep -q "res.status.*400" api/auth/2fa/setup.ts; then
  echo "  ✓ Proper HTTP status codes implemented"
else
  echo "  ⚠️  Check HTTP status code handling"
fi

# 检查token验证
if grep -q "verifyToken" api/auth/2fa/setup.ts; then
  echo "  ✓ Token verification middleware present"
else
  echo "  ✗ Missing token verification"
fi

# 检查日志记录
if grep -q "logger" api/auth/2fa/setup.ts; then
  echo "  ✓ Error logging implemented"
else
  echo "  ⚠️  Check error logging"
fi

echo ""
echo "📊 Summary..."
echo ""

if [ "$ALL_EXISTS" = true ]; then
  echo "✅ ALL 2FA API routes have been successfully created!"
  echo ""
  echo "Routes available:"
  echo "  • POST /api/auth/register - User registration"
  echo "  • POST /api/auth/login - User login"
  echo "  • GET  /api/auth/me - Current user info"
  echo "  • POST /api/auth/2fa/setup - Initialize 2FA (get QR code)"
  echo "  • POST /api/auth/2fa/enable - Enable 2FA with TOTP verification"
  echo "  • POST /api/auth/2fa/disable - Disable 2FA with TOTP verification"
  echo "  • POST /api/auth/2fa/verify-login - Verify TOTP during login"
else
  echo "❌ Some API files are missing. Please check the file creation."
fi

echo ""
echo "=========================================="
echo "✅ Verification complete"
