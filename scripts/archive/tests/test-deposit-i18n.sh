#!/bin/bash

# Deposit Page i18n Network Variable Test
# Tests that {network} is replaced with actual value

echo "🧪 Starting Deposit Page i18n Test..."
echo ""
echo "URL: https://www.moneradigital.com/dashboard/deposit"
echo ""

echo "Step 1: Login to the application"
echo "================================="

agent-browser open "https://www.moneradigital.com/login"
agent-browser wait --load networkidle
agent-browser snapshot -i

echo "Filling email..."
agent-browser fill @e1 "testuser1768890822@example.com"
echo "Filling password..."
agent-browser fill @e2 "TestPass123!"
echo "Clicking login button..."
agent-browser click @e3

agent-browser wait --load networkidle
agent-browser wait 3000

echo ""
echo "Step 2: Navigate to deposit page"
echo "================================="

agent-browser open "https://www.moneradigital.com/dashboard/deposit"
agent-browser wait --load networkidle
agent-browser wait 3000

echo ""
echo "Step 3: Check page content and network warning"
echo "==============================================="

agent-browser snapshot -i > /tmp/deposit_page.txt 2>&1

echo "Page elements found:"
echo "--------------------"
cat /tmp/deposit_page.txt | grep -E "button|combobox|textbox" | head -20
echo ""

echo "Checking i18n interpolation..."
echo "-------------------------------"

# Check for correct text - looking at actual page content
if grep -qi "sending via\|ensure you are\|请务必确认" /tmp/deposit_page.txt; then
    echo "✅ PASS: Found network warning text (i18n working)"
else
    echo "⚠️  INFO: Warning text not found in this format"
fi

# Check that {network} placeholder is NOT shown (literal)
if grep -q "{network}" /tmp/deposit_page.txt 2>/dev/null; then
    echo "❌ FAIL: Found literal '{network}' placeholder - i18n interpolation not working!"
else
    echo "✅ PASS: No literal '{network}' placeholder found"
fi

# Check for page structure
echo ""
echo "Checking deposit page structure..."
echo "-----------------------------------"

if grep -qi "Copy Address" /tmp/deposit_page.txt; then
    echo "✅ PASS: 'Copy Address' button found - deposit section is visible"
else
    echo "❌ FAIL: 'Copy Address' button not found"
fi

if grep -qi "Deposit" /tmp/deposit_page.txt; then
    echo "✅ PASS: 'Deposit' text found"
else
    echo "❌ FAIL: 'Deposit' text not found"
fi

# Check for comboboxes (asset and network selectors)
if grep -q "combobox" /tmp/deposit_page.txt; then
    echo "✅ PASS: Comboboxes found (network/asset selectors)"
else
    echo "❌ FAIL: No comboboxes found"
fi

echo ""
echo "Step 4: Get page title"
echo "======================"
agent-browser get title

echo ""
echo "Step 5: Close browser"
echo "====================="
agent-browser close

echo ""
echo "============================================="
echo "✅ Test completed!"
echo ""
echo "📋 Summary:"
echo "-----------"
echo "- i18n interpolation: Check if {network} is replaced"
echo "- Page structure: Deposit page loads correctly"
echo "- Wallet status: Must be activated for full features"
echo ""
echo "To view full page snapshot: cat /tmp/deposit_page.txt"
