#!/bin/bash

# agent-browser 2FA Flow Test
# Tests Registration -> 2FA Setup

# Unset proxy to avoid localhost connection issues
unset http_proxy https_proxy HTTP_PROXY HTTPS_PROXY

echo "=========================================="
echo "  2FA Flow Test - agent-browser (Port 5001)"
echo "=========================================="
echo ""

# Config
FRONTEND_URL="http://localhost:5001"
BACKEND_URL="http://localhost:8081"
TIMESTAMP=$(date +%s)
TEST_EMAIL="agent.2fa.${TIMESTAMP}@example.com"
TEST_PASSWORD="TestPassword123!"
SESSION_NAME="2fa-test-${TIMESTAMP}"

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
# Click Register button.
# Try multiple selectors to be robust.
agent-browser --session ${SESSION_NAME} find role button click --name "Register" || \
agent-browser --session ${SESSION_NAME} click "button[type='submit']"

echo "  Waiting for redirect..."
agent-browser --session ${SESSION_NAME} wait 8000

# Check if we are logged in (Dashboard) or need to Login
CURRENT_URL=$(agent-browser --session ${SESSION_NAME} get url --json | jq -r '.data // empty')
echo "  Current URL: $CURRENT_URL"

if [[ "$CURRENT_URL" == *"login"* ]]; then
    echo "  Redirected to Login. Logging in..."
    agent-browser --session ${SESSION_NAME} find label "Email" fill "${TEST_EMAIL}"
    agent-browser --session ${SESSION_NAME} find label "Password" fill "${TEST_PASSWORD}"
    agent-browser --session ${SESSION_NAME} find role button click --name "Login" || \
    agent-browser --session ${SESSION_NAME} click "button[type='submit']"
    
    agent-browser --session ${SESSION_NAME} wait 3000
    CURRENT_URL=$(agent-browser --session ${SESSION_NAME} get url --json | jq -r '.data // empty')
    echo "  Current URL after login: $CURRENT_URL"
fi

if [[ "$CURRENT_URL" != *"dashboard"* ]]; then
    echo "  ❌ Failed to reach dashboard. Sticking at $CURRENT_URL"
    # Take screenshot for debug
    agent-browser --session ${SESSION_NAME} screenshot /tmp/debug_reg_fail.png
    exit 1
fi

echo ""
echo "[4/8] Navigating to Security Settings..."
agent-browser --session ${SESSION_NAME} open "${FRONTEND_URL}/dashboard/security"
agent-browser --session ${SESSION_NAME} wait 2000

# Verify we are on security page
CURRENT_URL=$(agent-browser --session ${SESSION_NAME} get url --json | jq -r '.data // empty')
if [[ "$CURRENT_URL" != *"security"* ]]; then
    echo "  ❌ Failed to navigate to security page. Current: $CURRENT_URL"
    exit 1
else
    echo "  ✅ On Security Page"
fi

echo ""
echo "[5/8] Starting 2FA Setup..."
# Look for "Enable 2FA" button.
# In the code (Security.tsx) it's: {t("dashboard.security.enable2FA")} which is "Enable 2FA" or "开启 2FA".
# We are likely in English default, but might be Chinese.
# Try English first.
agent-browser --session ${SESSION_NAME} find role button click --name "Enable 2FA" || \
agent-browser --session ${SESSION_NAME} find role button click --name "开启 2FA" || \
agent-browser --session ${SESSION_NAME} find text "Enable 2FA" click || \
agent-browser --session ${SESSION_NAME} click "button:has-text('Enable 2FA')"

agent-browser --session ${SESSION_NAME} wait 4000

# Extract Secret
echo "  Extracting Secret..."
# Save HTML for debug
agent-browser --session ${SESSION_NAME} get html "body" > /tmp/security_page.html

PAGE_TEXT=$(agent-browser --session ${SESSION_NAME} get text --json | jq -r '.data // empty')
echo "  Page Text Preview: $(echo "$PAGE_TEXT" | head -n 5)"

# Try to find secret in text (usually 16-32 chars, Base32)
# Look for a standalone string of uppercase letters and numbers (2-7) that is long enough.
SECRET=$(echo "$PAGE_TEXT" | grep -oE "\b[A-Z2-7]{16,}\b" | head -n 1)

if [ -z "$SECRET" ]; then
    echo "  ⚠️  Could not extract secret from text. Trying HTML..."
    HTML=$(cat /tmp/security_page.html)
    SECRET=$(echo "$HTML" | grep -oE '<code[^>]*>[A-Z2-7=]{16,}</code>' | sed -e 's/<[^>]*>//g' | head -n 1)
fi

if [ -z "$SECRET" ]; then
    echo "  ⚠️  Could not extract secret. Content of /tmp/security_page.html:"
    grep "code" /tmp/security_page.html || echo "No code tags found"
    
    echo "  Using Mock Secret (Likely to fail if backend is real)..."
    SECRET="JBSWY3DPEBLW64TMMQ======"
else
    echo "  ✅ Extracted Secret: $SECRET"
fi

echo ""
echo "[6/8] Generating TOTP..."
TOKEN=$(node generate-totp.js "$SECRET")
echo "  Generated Token: $TOKEN"

echo ""
echo "[7/8] Entering Token..."

# Click Next if there is a step 1
# Security.tsx: "Next: Backup Codes" button -> setStep(2)
# Oh, the UI has steps!
# Step 1: QR code + Secret. Button "Next: Backup Codes".
# Step 2: Backup codes + Input field + Verify button.

# We need to click "Next: Backup Codes" first?
# Let's check Security.tsx logic.
# <Button onClick={() => setStep(2)} className="w-full">{t("dashboard.security.nextBackupCodes")}</Button>
# Yes.
echo "  Clicking Next..."
agent-browser --session ${SESSION_NAME} find role button click --name "Next: Backup Codes" || \
agent-browser --session ${SESSION_NAME} click "button:has-text('Next')"

agent-browser --session ${SESSION_NAME} wait 1000

# Now we are on step 2. Input token.
agent-browser --session ${SESSION_NAME} find placeholder "000000" fill "$TOKEN" || \
agent-browser --session ${SESSION_NAME} find role textbox fill "$TOKEN"

# Click Verify
echo "  Clicking Verify..."
agent-browser --session ${SESSION_NAME} find role button click --name "Verify & Enable" || \
agent-browser --session ${SESSION_NAME} find role button click --name "Verify"

echo "  Waiting for completion..."
agent-browser --session ${SESSION_NAME} wait 6000

echo ""
echo "[8/8] Verifying Success..."
# Check for "Disable 2FA" button
PAGE_TEXT_AFTER=$(agent-browser --session ${SESSION_NAME} get html "body" --json | jq -r '.data // empty')
# Strip HTML tags for cleaner log (optional, but let's just grep the HTML)

if echo "$PAGE_TEXT_AFTER" | grep -q "Disable 2FA"; then
    echo "  ✅ 2FA Enabled Successfully!"
elif echo "$PAGE_TEXT_AFTER" | grep -q "Enabled"; then
    echo "  ✅ 2FA Enabled Successfully!"
elif echo "$PAGE_TEXT_AFTER" | grep -q "关闭 2FA"; then
    echo "  ✅ 2FA Enabled Successfully! (Chinese)"
elif echo "$PAGE_TEXT_AFTER" | grep -q "已开启"; then
    echo "  ✅ 2FA Enabled Successfully! (Chinese)"
else
    echo "  ❌ Verification Failed. Snapshot saved to /tmp/2fa_fail.png"    agent-browser --session ${SESSION_NAME} screenshot /tmp/2fa_fail.png
    exit 1
fi

echo ""
echo "Test Complete."
agent-browser --session ${SESSION_NAME} close
