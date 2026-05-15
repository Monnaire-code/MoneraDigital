#!/bin/bash

# 测试脚本：验证从 Core API 获取钱包地址
# Core API 地址: http://198.13.57.142:8080
# 测试链: TRX(SHASTA)_TRON_TESTNET

set -e

CORE_API_URL="http://198.13.57.142:8080"
TEST_EMAIL="test@example.com"
TEST_PASSWORD="Test123456"

echo "=========================================="
echo "Core API 地址获取测试"
echo "=========================================="
echo "Core API URL: $CORE_API_URL"
echo "测试币种: TRX(SHASTA)_TRON_TESTNET"
echo ""

# 步骤 1: 登录获取 Token
echo "步骤 1: 登录获取 Token..."
LOGIN_RESPONSE=$(curl -s -X POST "$CORE_API_URL/api/auth/login" \
  -H "Content-Type: application/json" \
  -d "{
    \"email\": \"$TEST_EMAIL\",
    \"password\": \"$TEST_PASSWORD\"
  }")

echo "登录响应:"
echo "$LOGIN_RESPONSE" | jq '.' 2>/dev/null || echo "$LOGIN_RESPONSE"
echo ""

# 提取 Token
TOKEN=$(echo "$LOGIN_RESPONSE" | jq -r '.data.token' 2>/dev/null)
USER_ID=$(echo "$LOGIN_RESPONSE" | jq -r '.data.user.id' 2>/dev/null)

if [ "$TOKEN" == "null" ] || [ -z "$TOKEN" ]; then
    echo "❌ 登录失败，无法获取 Token"
    echo "尝试注册新用户..."
    
    # 尝试注册
    REGISTER_RESPONSE=$(curl -s -X POST "$CORE_API_URL/api/auth/register" \
      -H "Content-Type: application/json" \
      -d "{
        \"email\": \"$TEST_EMAIL\",
        \"password\": \"$TEST_PASSWORD\",
        \"confirmPassword\": \"$TEST_PASSWORD\"
      }")
    
    echo "注册响应:"
    echo "$REGISTER_RESPONSE" | jq '.' 2>/dev/null || echo "$REGISTER_RESPONSE"
    echo ""
    
    # 再次登录
    LOGIN_RESPONSE=$(curl -s -X POST "$CORE_API_URL/api/auth/login" \
      -H "Content-Type: application/json" \
      -d "{
        \"email\": \"$TEST_EMAIL\",
        \"password\": \"$TEST_PASSWORD\"
      }")
    
    TOKEN=$(echo "$LOGIN_RESPONSE" | jq -r '.data.token' 2>/dev/null)
    USER_ID=$(echo "$LOGIN_RESPONSE" | jq -r '.data.user.id' 2>/dev/null)
    
    if [ "$TOKEN" == "null" ] || [ -z "$TOKEN" ]; then
        echo "❌ 仍然无法获取 Token，退出测试"
        exit 1
    fi
fi

echo "✅ 登录成功"
echo "Token: ${TOKEN:0:50}..."
echo "User ID: $USER_ID"
echo ""

# 步骤 2: 调用 Core API 获取钱包地址
echo "步骤 2: 调用 Core API 获取钱包地址 (api/v1/wallet/address/get)..."
echo "请求参数:"
echo "  - userId: $USER_ID"
echo "  - productCode: X_FINANCE"
echo "  - currency: TRX(SHASTA)_TRON_TESTNET"
echo ""

ADDRESS_RESPONSE=$(curl -s -X POST "$CORE_API_URL/api/v1/wallet/address/get" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d "{
    \"userId\": \"$USER_ID\",
    \"productCode\": \"X_FINANCE\",
    \"currency\": \"TRX(SHASTA)_TRON_TESTNET\"
  }")

echo "Core API 响应:"
echo "$ADDRESS_RESPONSE" | jq '.' 2>/dev/null || echo "$ADDRESS_RESPONSE"
echo ""

# 检查结果
SUCCESS=$(echo "$ADDRESS_RESPONSE" | jq -r '.success' 2>/dev/null)
CODE=$(echo "$ADDRESS_RESPONSE" | jq -r '.code' 2>/dev/null)
ADDRESS=$(echo "$ADDRESS_RESPONSE" | jq -r '.data.address' 2>/dev/null)

if [ "$SUCCESS" == "true" ] || [ "$CODE" == "200" ]; then
    echo "✅ 测试成功！"
    echo ""
    echo "获取到的地址信息:"
    echo "  - Address: $ADDRESS"
    echo ""
    echo "完整响应数据:"
    echo "$ADDRESS_RESPONSE" | jq '.data' 2>/dev/null || echo "$ADDRESS_RESPONSE"
else
    echo "❌ 测试失败"
    MESSAGE=$(echo "$ADDRESS_RESPONSE" | jq -r '.message' 2>/dev/null)
    echo "错误信息: $MESSAGE"
    exit 1
fi

echo ""
echo "=========================================="
echo "测试完成"
echo "=========================================="
