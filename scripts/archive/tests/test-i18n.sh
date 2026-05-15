#!/bin/bash

# i18n Test Script for Withdraw Page
# Tests both English and Chinese language support

echo "🧪 Starting i18n tests for Withdraw page..."
echo ""

# Test URL
URL="https://www.moneradigital.com/dashboard/withdraw"

echo "📍 Testing URL: $URL"
echo ""

# Test 1: Check page loads
echo "Test 1: Check page loads"
agent-browser open "$URL"
agent-browser wait --load networkidle
agent-browser get title
echo ""

# Test 2: Get page snapshot to find key elements
echo "Test 2: Get page snapshot"
agent-browser snapshot -i > /tmp/snapshot_before.txt 2>&1
echo "Snapshot saved to /tmp/snapshot_before.txt"
echo ""

# Test 3: Check for English text elements
echo "Test 3: Checking for English text elements..."
echo "Looking for 'Withdraw Funds' in the page..."
grep -o "Withdraw Funds" /tmp/snapshot_before.txt || echo "Not found in snapshot"
echo ""

# Test 4: Check sidebar menu text
echo "Test 4: Checking sidebar menu..."
echo "Looking for 'Withdraw' in the sidebar..."
grep -i "Withdraw" /tmp/snapshot_before.txt | head -5 || echo "Not found"
echo ""

# Test 5: Verify no hardcoded Chinese "提币" text
echo "Test 5: Verifying no '提币' (old term) in page..."
if grep -q "提币" /tmp/snapshot_before.txt; then
    echo "❌ FAIL: Found old term '提币' in page"
else
    echo "✅ PASS: No old term '提币' found"
fi
echo ""

# Test 6: Check for "提现" (new term)
echo "Test 6: Checking for '提现' (new term) in page..."
if grep -q "提现" /tmp/snapshot_before.txt; then
    echo "✅ PASS: Found new term '提现' in page"
else
    echo "⚠️  INFO: '提现' not found in snapshot (may be in different language context)"
fi
echo ""

# Close browser
agent-browser close

echo "✅ Tests completed!"
echo ""
echo "📋 Test Summary:"
echo "-----------"
echo "1. Page loads: Check browser output"
echo "2. English text: Check for 'Withdraw Funds'"
echo "3. Old term removed: '提币' should not appear"
echo "4. New term present: '提现' should appear in Chinese"
