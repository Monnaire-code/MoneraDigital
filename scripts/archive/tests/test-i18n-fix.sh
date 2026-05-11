#!/bin/bash

# i18n Verification Test Script for Withdraw Page
# Tests translation keys are showing values instead of keys

echo "🧪 Starting i18n verification test..."
echo ""

# Login first to get access to the dashboard
echo "Step 1: Login to the application"
agent-browser open "https://www.moneradigital.com/login"

# Wait for page to load
agent-browser wait --load networkidle
agent-browser snapshot -i

# Check if we're on login page
echo ""
echo "Step 2: Login with test credentials"
echo "Filling email..."
agent-browser fill @e1 "testuser1768890822@example.com"
echo "Filling password..."
agent-browser fill @e2 "TestPass123!"
echo "Clicking login button..."
agent-browser click @e3

# Wait for navigation to dashboard
agent-browser wait --load networkidle
agent-browser wait 2000

echo ""
echo "Step 3: Navigate to withdraw page"
agent-browser open "https://www.moneradigital.com/dashboard/withdraw"
agent-browser wait --load networkidle
agent-browser wait 2000

echo ""
echo "Step 4: Take snapshot of withdraw page content"
agent-browser snapshot -i > /tmp/withdraw_snapshot.txt 2>&1
echo "Snapshot saved to /tmp/withdraw_snapshot.txt"
echo ""

echo "Step 5: Check for translation values (not keys)"
echo "============================================="

# Check for expected values in English
echo ""
echo "Checking for expected English text:"
echo "-----------------------------------"

# Check page title - look for "Withdraw Funds"
if grep -qi "Withdraw Funds" /tmp/withdraw_snapshot.txt; then
    echo "✅ PASS: Found 'Withdraw Funds' in page"
else
    echo "❌ FAIL: 'Withdraw Funds' NOT found in page"
    echo "   (might be using different text or login required)"
fi

# Check tab labels
if grep -qi "New Withdrawal" /tmp/withdraw_snapshot.txt; then
    echo "✅ PASS: Found 'New Withdrawal' in page"
else
    echo "❌ FAIL: 'New Withdrawal' NOT found in page"
fi

if grep -qi "History" /tmp/withdraw_snapshot.txt; then
    echo "✅ PASS: Found 'History' in page"
else
    echo "❌ FAIL: 'History' NOT found in page"
fi

# Check for "Add Withdrawal Address"
if grep -qi "Add Withdrawal Address" /tmp/withdraw_snapshot.txt; then
    echo "✅ PASS: Found 'Add Withdrawal Address' in page"
else
    echo "❌ FAIL: 'Add Withdrawal Address' NOT found in page"
fi

# Check for "Confirm Withdrawal"
if grep -qi "Confirm Withdrawal" /tmp/withdraw_snapshot.txt; then
    echo "✅ PASS: Found 'Confirm Withdrawal' in page"
else
    echo "❌ FAIL: 'Confirm Withdrawal' NOT found in page"
fi

echo ""
echo "Checking for problematic key names (should NOT be present):"
echo "------------------------------------------------------------"

# Check for translation keys - should NOT be present
if grep -q 'withdraw\.title' /tmp/withdraw_snapshot.txt 2>/dev/null; then
    echo "❌ FAIL: Found 'withdraw.title' (translation key) in page - i18n not working!"
else
    echo "✅ PASS: No 'withdraw.title' key found"
fi

if grep -q 'withdraw\.tab\.new' /tmp/withdraw_snapshot.txt 2>/dev/null; then
    echo "❌ FAIL: Found 'withdraw.tab.new' (translation key) in page - i18n not working!"
else
    echo "✅ PASS: No 'withdraw.tab.new' key found"
fi

if grep -q 'withdraw\.tab\.history' /tmp/withdraw_snapshot.txt 2>/dev/null; then
    echo "❌ FAIL: Found 'withdraw.tab.history' (translation key) in page - i18n not working!"
else
    echo "✅ PASS: No 'withdraw.tab.history' key found"
fi

if grep -q 'withdraw\.address\.empty' /tmp/withdraw_snapshot.txt 2>/dev/null; then
    echo "❌ FAIL: Found 'withdraw.address.empty' (translation key) in page - i18n not working!"
else
    echo "✅ PASS: No 'withdraw.address.empty' key found"
fi

if grep -q 'withdraw\.address\.addButton' /tmp/withdraw_snapshot.txt 2>/dev/null; then
    echo "❌ FAIL: Found 'withdraw.address.addButton' (translation key) in page - i18n not working!"
else
    echo "✅ PASS: No 'withdraw.address.addButton' key found"
fi

echo ""
echo "Step 6: Get page title for verification"
agent-browser get title
echo ""

echo "Step 7: Close browser"
agent-browser close
echo ""

echo "============================================="
echo "✅ Test completed!"
echo ""
echo "📋 Summary:"
echo "-----------"
echo "If all tests show PASS, i18n is working correctly."
echo "If any tests show FAIL, there's an issue with the translation system."
echo ""
echo "To view full snapshot: cat /tmp/withdraw_snapshot.txt"
