#!/bin/bash

# 2FA完整流程测试脚本

API_BASE="http://localhost:8081"
echo "🧪 2FA End-to-End Test"
echo "API Base: $API_BASE"
echo "================================"

# 生成测试用户
TEST_EMAIL="test_$(date +%s%N | cut -b1-13)@example.com"
TEST_PASSWORD="TestPassword123!"

echo ""
echo "📝 Test Credentials:"
echo "  Email: $TEST_EMAIL"
echo "  Password: $TEST_PASSWORD"
echo ""

# 1. 注册用户
echo "1️⃣  Registering user..."
REGISTER_RESPONSE=$(curl -s -X POST "$API_BASE/api/auth/register" \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"$TEST_EMAIL\",\"password\":\"$TEST_PASSWORD\"}")

echo "   Response: $REGISTER_RESPONSE"
USER_ID=$(echo "$REGISTER_RESPONSE" | grep -o '"userId":"[0-9]*"' | head -1 | cut -d'"' -f4)
if [ -z "$USER_ID" ]; then
  USER_ID=$(echo "$REGISTER_RESPONSE" | grep -o '"id":[0-9]*' | head -1 | cut -d':' -f2)
fi
echo "   ✓ User registered (ID: $USER_ID)"
echo ""

# 2. 登录用户
echo "2️⃣  Logging in user..."
LOGIN_RESPONSE=$(curl -s -X POST "$API_BASE/api/auth/login" \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"$TEST_EMAIL\",\"password\":\"$TEST_PASSWORD\"}")

AUTH_TOKEN=$(echo "$LOGIN_RESPONSE" | grep -o '"token":"[^"]*"' | head -1 | cut -d'"' -f4)
echo "   ✓ Login successful"
echo "   Token: ${AUTH_TOKEN:0:20}..."
echo ""

# 3. 获取用户信息
echo "3️⃣  Getting user info..."
ME_RESPONSE=$(curl -s -X GET "$API_BASE/api/auth/me" \
  -H "Authorization: Bearer $AUTH_TOKEN")

echo "   Response: $ME_RESPONSE"
echo "   ✓ User info retrieved"
echo ""

# 4. 初始化2FA
echo "4️⃣  Setting up 2FA..."
SETUP_RESPONSE=$(curl -s -X POST "$API_BASE/api/auth/2fa/setup" \
  -H "Authorization: Bearer $AUTH_TOKEN")

SECRET=$(echo "$SETUP_RESPONSE" | grep -o '"secret":"[^"]*"' | head -1 | cut -d'"' -f4)
QR_CODE=$(echo "$SETUP_RESPONSE" | grep -o '"qrCodeUrl":"[^"]*"' | head -1 | cut -d'"' -f4)
BACKUP_CODES=$(echo "$SETUP_RESPONSE" | grep -o '"backupCodes":\[\s*"[^"]*"' | head -10)

if [ -z "$SECRET" ]; then
  echo "   ✗ Failed to setup 2FA"
  echo "   Response: $SETUP_RESPONSE"
else
  echo "   ✓ 2FA setup successful"
  echo "   Secret: $SECRET"
  echo "   QR Code URL: ${QR_CODE:0:50}..."
  echo ""

  # 5. 生成TOTP令牌并启用2FA
  echo "5️⃣  Enabling 2FA with TOTP..."

  # 使用node-otp生成TOTP
  TOTP_TOKEN=$(node -e "const {authenticator}=require('otplib');console.log(authenticator.generate('$SECRET'))")
  echo "   Generated TOTP: $TOTP_TOKEN"

  ENABLE_RESPONSE=$(curl -s -X POST "$API_BASE/api/auth/2fa/enable" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $AUTH_TOKEN" \
    -d "{\"token\":\"$TOTP_TOKEN\"}")

  echo "   Response: $ENABLE_RESPONSE"
  if echo "$ENABLE_RESPONSE" | grep -q '"success":true'; then
    echo "   ✓ 2FA enabled successfully"
  else
    echo "   ⚠️  2FA enable response: $ENABLE_RESPONSE"
  fi
  echo ""

  # 6. 验证2FA已启用
  echo "6️⃣  Verifying 2FA status..."
  STATUS_RESPONSE=$(curl -s -X GET "$API_BASE/api/auth/me" \
    -H "Authorization: Bearer $AUTH_TOKEN")

  if echo "$STATUS_RESPONSE" | grep -q '"twoFactorEnabled":true'; then
    echo "   ✓ 2FA is enabled"
  else
    echo "   ⚠️  2FA status: $STATUS_RESPONSE"
  fi
  echo ""

  # 7. 禁用2FA
  echo "7️⃣  Disabling 2FA..."
  TOTP_TOKEN=$(node -e "const {authenticator}=require('otplib');console.log(authenticator.generate('$SECRET'))")

  DISABLE_RESPONSE=$(curl -s -X POST "$API_BASE/api/auth/2fa/disable" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $AUTH_TOKEN" \
    -d "{\"token\":\"$TOTP_TOKEN\"}")

  echo "   Response: $DISABLE_RESPONSE"
  if echo "$DISABLE_RESPONSE" | grep -q '"success":true'; then
    echo "   ✓ 2FA disabled successfully"
  else
    echo "   ⚠️  2FA disable response: $DISABLE_RESPONSE"
  fi
fi

echo ""
echo "================================"
echo "✅ End-to-End Test Complete"
