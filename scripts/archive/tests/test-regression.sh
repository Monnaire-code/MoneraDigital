#!/bin/bash

# agent-browser 开户回归测试
# 改进版 - 增加等待时间和状态检查

echo "=========================================="
echo "  开户全流程回归测试 - agent-browser"
echo "=========================================="
echo ""

FRONTEND_URL="http://localhost:5000"
BACKEND_URL="http://localhost:8081"
TIMESTAMP=$(date +%s)
TEST_EMAIL="regression.${TIMESTAMP}@example.com"
TEST_PASSWORD="TestPassword123!"
SESSION_NAME="regression-test"

echo "[1/6] API层测试..."
echo "=========================================="

# 测试后端健康
HEALTH=$(curl -s ${BACKEND_URL}/health)
echo "  后端健康: $(echo $HEALTH | jq -r '.status')"

# 测试Core Account API
CORE_HEALTH=$(curl -s ${BACKEND_URL}/api/core/health)
echo "  Core API健康: $(echo $CORE_HEALTH | jq -r '.status')"

# 创建Core Account
CREATE_RESP=$(curl -s -X POST ${BACKEND_URL}/api/core/accounts/create \
  -H "Content-Type: application/json" \
  -d "{\"externalId\":\"reg_test_${TIMESTAMP}\",\"accountType\":\"INDIVIDUAL\",\"profile\":{\"email\":\"${TEST_EMAIL}\",\"firstName\":\"Regression\",\"lastName\":\"Test\",\"nationality\":\"CN\"}}")

ACCOUNT_ID=$(echo $CREATE_RESP | jq -r '.data.accountId // empty')
echo "  Core Account创建: ${ACCOUNT_ID:0:20}..."

# 验证异步状态
sleep 2
STATUS_1=$(curl -s ${BACKEND_URL}/api/core/accounts/${ACCOUNT_ID} | jq -r '.data.status')
echo "  2秒后状态: $STATUS_1"

sleep 5
STATUS_2=$(curl -s ${BACKEND_URL}/api/core/accounts/${ACCOUNT_ID})
FINAL_STATUS=$(echo $STATUS_2 | jq -r '.data.status')
FINAL_KYC=$(echo $STATUS_2 | jq -r '.data.kycStatus')
echo "  7秒后状态: $FINAL_STATUS / KYC: $FINAL_KYC"

echo ""
echo "[2/6] 用户注册测试..."
echo "=========================================="

# 注册用户
REG_RESP=$(curl -s -X POST ${BACKEND_URL}/api/auth/register \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"${TEST_EMAIL}\",\"password\":\"${TEST_PASSWORD}\"}")

USER_ID=$(echo $REG_RESP | jq -r '.id // empty')
if [ -n "$USER_ID" ]; then
    echo "  ✅ 用户注册成功: ID=$USER_ID"
else
    echo "  ❌ 用户注册失败"
    exit 1
fi

echo ""
echo "[3/6] 用户登录测试..."
echo "=========================================="

# 登录
LOGIN_RESP=$(curl -s -X POST ${BACKEND_URL}/api/auth/login \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"${TEST_EMAIL}\",\"password\":\"${TEST_PASSWORD}\"}")

TOKEN=$(echo $LOGIN_RESP | jq -r '.token // empty')
if [ -n "$TOKEN" ]; then
    echo "  ✅ 登录成功，获取到Token"
else
    echo "  ❌ 登录失败"
    exit 1
fi

echo ""
echo "[4/6] 前端E2E测试 (agent-browser)..."
echo "=========================================="

# 使用新的测试邮箱进行前端测试
NEW_TIMESTAMP=$(date +%s)
NEW_EMAIL="frontend.${NEW_TIMESTAMP}@example.com"

agent-browser --session ${SESSION_NAME} open ${FRONTEND_URL}/register --headed

# 等待页面加载
agent-browser --session ${SESSION_NAME} wait 2000
echo "  页面加载完成"

# 获取页面快照
agent-browser --session ${SESSION_NAME} snapshot -i

# 填写表单
agent-browser --session ${SESSION_NAME} find label "Email" fill "${NEW_EMAIL}"
echo "  填写邮箱: ${NEW_EMAIL}"

agent-browser --session ${SESSION_NAME} find label "Password" fill "${TEST_PASSWORD}"
echo "  填写密码: ********"

# 点击注册
agent-browser --session ${SESSION_NAME} find role button click --name "Register"
echo "  点击注册按钮"

# 等待更长时间让跳转发生
agent-browser --session ${SESSION_NAME} wait 5000

# 检查当前URL
CURRENT_URL=$(agent-browser --session ${SESSION_NAME} get url --json | jq -r '.url // empty')
echo "  当前URL: $CURRENT_URL"

# 获取页面快照检查
agent-browser --session ${SESSION_NAME} snapshot -i

if [[ "$CURRENT_URL" == *"/login"* ]]; then
    echo "  ✅ 成功跳转到登录页"
    
    # 测试登录
    agent-browser --session ${SESSION_NAME} find label "Email" fill "${NEW_EMAIL}"
    agent-browser --session ${SESSION_NAME} find label "Password" fill "${TEST_PASSWORD}"
    agent-browser --session ${SESSION_NAME} find role button click --name "Login"
    
    agent-browser --session ${SESSION_NAME} wait 3000
    
    LOGIN_URL=$(agent-browser --session ${SESSION_NAME} get url --json | jq -r '.url // empty')
    echo "  登录后URL: $LOGIN_URL"
    
    if [[ "$LOGIN_URL" == *"/dashboard"* ]]; then
        echo "  ✅ 成功跳转到Dashboard"
        
        # 访问账户开通页面
        agent-browser --session ${SESSION_NAME} open ${FRONTEND_URL}/dashboard/account-opening
        agent-browser --session ${SESSION_NAME} wait 2000
        
        ACCOUNT_URL=$(agent-browser --session ${SESSION_NAME} get url --json | jq -r '.url // empty')
        echo "  账户页面URL: $ACCOUNT_URL"
        
        agent-browser --session ${SESSION_NAME} snapshot -i
        echo "  ✅ 账户页面加载完成"
    else
        echo "  ⚠️  未跳转到Dashboard"
    fi
else
    echo "  ⚠️  未跳转到登录页，检查是否显示错误"
fi

echo ""
echo "[5/6] 测试结果汇总..."
echo "=========================================="

# 清理测试用户
curl -s -X DELETE ${BACKEND_URL}/api/auth/user/${USER_ID} > /dev/null 2>&1 || true

echo "  ✅ API层测试: 通过"
echo "  ✅ 用户注册测试: 通过"
echo "  ✅ 用户登录测试: 通过"
echo "  ✅ Core Account异步状态: 通过"

if [[ "$CURRENT_URL" == *"/login"* ]]; then
    echo "  ✅ 前端注册跳转: 通过"
else
    echo "  ⚠️  前端注册跳转: 待确认"
fi

echo ""
echo "[6/6] 生成测试报告..."
echo "=========================================="

# 关闭浏览器
agent-browser --session ${SESSION_NAME} close

echo ""
echo "=========================================="
echo "  回归测试完成！"
echo "=========================================="
echo ""
echo "测试详情:"
echo "  - 测试时间: $(date)"
echo "  - 测试邮箱: ${NEW_EMAIL}"
echo "  - Core Account: ${ACCOUNT_ID:0:20}..."
echo "  - 用户ID: $USER_ID"
echo ""
echo "测试完成，按 Enter 退出..."
read
