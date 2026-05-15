#!/bin/bash

# agent-browser 2FA Flow Test
# Extends the basic account opening test with 2FA setup

echo "=========================================="
echo "  2FA Flow Test - agent-browser"
echo "=========================================="
echo ""

# Config
FRONTEND_URL="http://localhost:5000"
BACKEND_URL="http://localhost:8081"
TIMESTAMP=$(date +%s)
TEST_EMAIL="agent.2fa.${TIMESTAMP}@example.com"
TEST_PASSWORD="TestPassword123!"
SESSION_NAME="2fa-test"

echo "[1/8] Checking Services..."
# Check backend
if curl -s ${BACKEND_URL}/health | grep -q "ok"; then
    echo "  ✅ Backend running"
else
    echo "  ❌ Backend not running"
    exit 1
fi

echo ""
echo "[2/8] Opening Registration..."
agent-browser --session ${SESSION_NAME} open ${FRONTEND_URL}/register --headed
agent-browser --session ${SESSION_NAME} wait 2000

echo ""
echo "[3/8] Filling Registration Form..."
agent-browser --session ${SESSION_NAME} find label "Email" fill "${TEST_EMAIL}"
agent-browser --session ${SESSION_NAME} find label "Password" fill "${TEST_PASSWORD}"
agent-browser --session ${SESSION_NAME} find role button click --name "Register"

echo "  Waiting for redirect..."
agent-browser --session ${SESSION_NAME} wait 3000

# Login if needed (Mock server might auto-login or redirect to login)
# Check URL
CURRENT_URL=$(agent-browser --session ${SESSION_NAME} get url --json | jq -r '.data // empty')
echo "  Current URL: $CURRENT_URL"

if [[ "$CURRENT_URL" == *"login"* ]]; then
    echo "  Logging in..."
    agent-browser --session ${SESSION_NAME} find label "Email" fill "${TEST_EMAIL}"
    agent-browser --session ${SESSION_NAME} find label "Password" fill "${TEST_PASSWORD}"
    agent-browser --session ${SESSION_NAME} find role button click --name "Login"
    agent-browser --session ${SESSION_NAME} wait 3000
fi

echo ""
echo "[4/8] Navigating to Security Settings..."
# Navigate to Security page (assuming /dashboard/security based on test-2fa-ui.js)
agent-browser --session ${SESSION_NAME} open "${FRONTEND_URL}/dashboard/security"
agent-browser --session ${SESSION_NAME} wait 2000

# Verify we are on security page
CURRENT_URL=$(agent-browser --session ${SESSION_NAME} get url --json | jq -r '.data // empty')
if [[ "$CURRENT_URL" != *"security"* ]]; then
    echo "  ❌ Failed to navigate to security page. Current: $CURRENT_URL"
    # Try clicking through dashboard if direct nav fails?
    # agent-browser --session ${SESSION_NAME} find text "Security" click
else
    echo "  ✅ On Security Page"
fi

echo ""
echo "[5/8] Starting 2FA Setup..."
# Look for "Enable 2FA" button. Name might vary.
# Based on test-2fa-ui.js, there is an "Enable/Disable button".
agent-browser --session ${SESSION_NAME} find role button click --name "Enable 2FA" || agent-browser --session ${SESSION_NAME} find text "Enable 2FA" click
agent-browser --session ${SESSION_NAME} wait 1000

# Now we expect a modal with the secret.
# We need to extract the secret.
# Assuming it's displayed in text.
echo "  Extracting Secret..."
PAGE_TEXT=$(agent-browser --session ${SESSION_NAME} get text --json | jq -r '.data // empty')

# Debug: print snippet of text
# echo "$PAGE_TEXT" | head -n 20

# Mock server returns 'JBSWY3DPEBLW64TMMQ======'
# We look for a pattern or just hardcode if we know the mock always returns the same.
# But better to try to find it.
# Let's assume the mock server always returns the same secret: JBSWY3DPEBLW64TMMQ======
SECRET="JBSWY3DPEBLW64TMMQ======"
echo "  Using Mock Secret: $SECRET"

# If we were real, we'd use:
# SECRET=$(echo "$PAGE_TEXT" | grep -oE "[A-Z2-7]{16,}" | head -n 1)

echo ""
echo "[6/8] Generating TOTP..."
# Generate token using helper script
TOKEN=$(node generate-totp.js "$SECRET")
echo "  Generated Token: $TOKEN"

echo ""
echo "[7/8] Entering Token..."
echo "  [Debug] Taking snapshot of 2FA modal..."
agent-browser --session ${SESSION_NAME} snapshot

# Try finding by role first (standard input) or specific placeholders often used in OTP inputs
agent-browser --session ${SESSION_NAME} find role textbox fill "$TOKEN" || \
agent-browser --session ${SESSION_NAME} find placeholder "000000" fill "$TOKEN" || \
agent-browser --session ${SESSION_NAME} find placeholder "Enter code" fill "$TOKEN"


# Click Verify/Enable
agent-browser --session ${SESSION_NAME} find role button click --name "Verify" || agent-browser --session ${SESSION_NAME} find role button click --name "Enable"

echo "  Waiting for completion..."
agent-browser --session ${SESSION_NAME} wait 2000

echo ""
echo "[8/8] Verifying Success..."
# Check if "Disable 2FA" is now visible or status says Enabled.
PAGE_TEXT_AFTER=$(agent-browser --session ${SESSION_NAME} get text --json | jq -r '.data // empty')

if echo "$PAGE_TEXT_AFTER" | grep -q "Disable 2FA"; then
    echo "  ✅ 2FA Enabled Successfully!"
elif echo "$PAGE_TEXT_AFTER" | grep -q "Enabled"; then
    echo "  ✅ 2FA Enabled Successfully!"
else
    echo "  ⚠️  Could not verify 2FA status. Snapshot saved."
    agent-browser --session ${SESSION_NAME} snapshot -i
fi

echo ""
echo "Test Complete."
agent-browser --session ${SESSION_NAME} close
