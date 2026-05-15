#!/bin/bash

# Test script for 2FA address verification
# This script tests the 2FA verification flow

echo "=== Testing 2FA Address Verification ==="

# Check if we have the required tools
if ! command -v node &> /dev/null; then
    echo "Node.js is required but not installed"
    exit 1
fi

# Create a test file
cat > /tmp/test-2fa-verify.js << 'EOF'
const crypto = require('crypto');

// Test TOTP generation with the user's secret
const secret = 'YO5CXNI64PL3ZDCUFPIFWJWMCHWECV6O';

console.log('Testing TOTP generation with secret:', secret);

// Generate TOTP code (simplified version for testing)
function generateTOTP(secret, timeStep = 30) {
    // Base32 decode
    const base32Chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
    let bits = '';
    for (let char of secret) {
        const val = base32Chars.indexOf(char.toUpperCase());
        if (val === -1) continue;
        bits += val.toString(2).padStart(5, '0');
    }
    
    const bytes = [];
    for (let i = 0; i < bits.length - 7; i += 8) {
        bytes.push(parseInt(bits.substring(i, i + 8), 2));
    }
    
    const key = Buffer.from(bytes);
    const time = Math.floor(Date.now() / 1000 / timeStep);
    const timeBuffer = Buffer.alloc(8);
    timeBuffer.writeBigUInt64BE(BigInt(time), 0);
    
    const hmac = crypto.createHmac('sha1', key);
    hmac.update(timeBuffer);
    const hash = hmac.digest();
    
    const offset = hash[hash.length - 1] & 0x0f;
    const code = ((hash[offset] & 0x7f) << 24 |
                  (hash[offset + 1] & 0xff) << 16 |
                  (hash[offset + 2] & 0xff) << 8 |
                  (hash[offset + 3] & 0xff)) % 1000000;
    
    return code.toString().padStart(6, '0');
}

const code = generateTOTP(secret);
console.log('Generated TOTP code:', code);
console.log('Current timestamp:', new Date().toISOString());

// Test with different time steps to account for clock drift
console.log('\nCodes around current time:');
for (let i = -1; i <= 1; i++) {
    const time = Math.floor(Date.now() / 1000 / 30) + i;
    const timeBuffer = Buffer.alloc(8);
    timeBuffer.writeBigUInt64BE(BigInt(time), 0);
    
    const base32Chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
    let bits = '';
    for (let char of secret) {
        const val = base32Chars.indexOf(char.toUpperCase());
        if (val === -1) continue;
        bits += val.toString(2).padStart(5, '0');
    }
    
    const bytes = [];
    for (let i = 0; i < bits.length - 7; i += 8) {
        bytes.push(parseInt(bits.substring(i, i + 8), 2));
    }
    
    const key = Buffer.from(bytes);
    const hmac = crypto.createHmac('sha1', key);
    hmac.update(timeBuffer);
    const hash = hmac.digest();
    
    const offset = hash[hash.length - 1] & 0x0f;
    const code = ((hash[offset] & 0x7f) << 24 |
                  (hash[offset + 1] & 0xff) << 16 |
                  (hash[offset + 2] & 0xff) << 8 |
                  (hash[offset + 3] & 0xff)) % 1000000;
    
    console.log(`  Time step ${i}: ${code.toString().padStart(6, '0')}`);
}
EOF

node /tmp/test-2fa-verify.js

echo ""
echo "=== Test Complete ==="
echo ""
echo "To use this code:"
echo "1. Open Google Authenticator on your phone"
echo "2. Find the code for Monera Digital"
echo "3. Compare with the generated code above"
echo "4. If they match, the secret is correct"
echo ""
echo "If codes don't match, the secret may be:"
echo "- Not stored correctly in database"
echo "- Encrypted/decrypted incorrectly"
echo "- Different from what Google Authenticator has"
