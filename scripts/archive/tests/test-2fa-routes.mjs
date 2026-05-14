import fetch from 'node-fetch';
import { authenticator } from 'otplib';

const API_BASE = 'http://localhost:8081';

const results = [];

async function test(name, fn) {
  try {
    await fn();
    results.push({ name, success: true });
    console.log(`✓ ${name}`);
  } catch (error) {
    results.push({ name, success: false, error: error.message });
    console.log(`✗ ${name}: ${error.message}`);
  }
}

// Test user credentials
const testEmail = `test_${Date.now()}@example.com`;
const testPassword = 'TestPassword123!';
let authToken = '';
let userId = 0;
let setupSecret = '';
let backupCodes = [];

async function main() {
  console.log('🧪 2FA API Routes Test Suite\n');

  // Test 1: Register user
  await test('Register user', async () => {
    const res = await fetch(`${API_BASE}/api/auth/register`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email: testEmail, password: testPassword }),
    });
    if (!res.ok) throw new Error(`Status ${res.status}`);
    const data = await res.json();
    if (!data.user?.id) throw new Error('No user ID returned');
    userId = data.user.id;
  });

  // Test 2: Login user
  await test('Login user', async () => {
    const res = await fetch(`${API_BASE}/api/auth/login`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email: testEmail, password: testPassword }),
    });
    if (!res.ok) throw new Error(`Status ${res.status}`);
    const data = await res.json();
    if (!data.token) throw new Error('No token returned');
    authToken = data.token;
  });

  // Test 3: Get user info
  await test('Get user info (/api/auth/me)', async () => {
    const res = await fetch(`${API_BASE}/api/auth/me`, {
      headers: { Authorization: `Bearer ${authToken}` },
    });
    if (!res.ok) throw new Error(`Status ${res.status}`);
    const data = await res.json();
    if (!data.email) throw new Error('No email returned');
  });

  // Test 4: Setup 2FA
  await test('Setup 2FA (/api/auth/2fa/setup)', async () => {
    const res = await fetch(`${API_BASE}/api/auth/2fa/setup`, {
      method: 'POST',
      headers: { Authorization: `Bearer ${authToken}` },
    });
    if (!res.ok) throw new Error(`Status ${res.status}`);
    const data = await res.json();
    if (!data.secret || !data.qrCodeUrl) throw new Error('Missing 2FA data');
    if (!data.otpauth) throw new Error('Missing otpauth URL in response (Security.tsx requirement)');
    setupSecret = data.secret;
    backupCodes = data.backupCodes;
  });

  // Test 5: Enable 2FA (with TOTP)
  await test('Enable 2FA (/api/auth/2fa/enable)', async () => {
    if (!setupSecret) throw new Error('No secret from setup');
    const token = authenticator.generate(setupSecret);

    const res = await fetch(`${API_BASE}/api/auth/2fa/enable`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Authorization: `Bearer ${authToken}`,
      },
      body: JSON.stringify({ token }),
    });
    if (!res.ok) {
      const error = await res.text();
      throw new Error(`Status ${res.status}: ${error}`);
    }
  });

  // Test 6: Verify 2FA is enabled
  await test('Verify 2FA is enabled', async () => {
    const res = await fetch(`${API_BASE}/api/auth/me`, {
      headers: { Authorization: `Bearer ${authToken}` },
    });
    if (!res.ok) throw new Error(`Status ${res.status}`);
    const data = await res.json();
    if (!data.twoFactorEnabled) throw new Error('2FA not enabled');
  });

  // Test 7: Disable 2FA
  await test('Disable 2FA (/api/auth/2fa/disable)', async () => {
    if (!setupSecret) throw new Error('No secret for verification');
    const token = authenticator.generate(setupSecret);

    const res = await fetch(`${API_BASE}/api/auth/2fa/disable`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Authorization: `Bearer ${authToken}`,
      },
      body: JSON.stringify({ token }),
    });
    if (!res.ok) {
      const error = await res.text();
      throw new Error(`Status ${res.status}: ${error}`);
    }
  });

  // Print summary
  console.log('\n' + '='.repeat(50));
  const passed = results.filter(r => r.success).length;
  const total = results.length;
  console.log(`📊 Results: ${passed}/${total} tests passed`);

  if (passed === total) {
    console.log('✅ All tests passed! 2FA API routes are working correctly.');
  } else {
    console.log('❌ Some tests failed. Details:');
    results.filter(r => !r.success).forEach(r => {
      console.log(`  - ${r.name}: ${r.error}`);
    });
  }
}

main().catch(console.error);
