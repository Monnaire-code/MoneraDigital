#!/bin/bash

echo "🧪 Starting i18n key verification for Security page..."

# Run the node verification script
node scripts/verify-security-i18n.cjs

if [ $? -eq 0 ]; then
    echo "✅ PASS: All security keys are present in translation files."
    exit 0
else
    echo "❌ FAIL: Missing security keys detected."
    exit 1
fi
