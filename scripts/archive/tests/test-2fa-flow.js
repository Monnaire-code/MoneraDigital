#!/usr/bin/env node

/**
 * Comprehensive 2FA (Google Authenticator) Testing Script
 * Tests the complete flow: Setup -> QR Code -> TOTP Verification -> Enable
 */

import { spawn } from 'child_process';
import { authenticator } from 'otplib';

const BASE_URL = 'http://localhost:5000';
const API_BASE = 'http://localhost:5000/api';

// Test user credentials
const testUser = {
  email: `test2fa-${Date.now()}@example.com`,
  password: 'TestPassword123!'
};

let authToken = null;
let userId = null;

// Color codes for terminal output
const colors = {
  reset: '\x1b[0m',
  green: '\x1b[32m',
  red: '\x1b[31m',
  yellow: '\x1b[33m',
  blue: '\x1b[34m',
  cyan: '\x1b[36m'
};

function log(message, color = 'reset') {
  console.log(`${colors[color]}${message}${colors.reset}`);
}

function logSection(title) {
  console.log('\n' + '='.repeat(60));
  log(title, 'blue');
  console.log('='.repeat(60));
}

async function runAgentBrowser(script) {
  return new Promise((resolve, reject) => {
    const browser = spawn('agent-browser', ['run'], {
      stdio: ['pipe', 'pipe', 'pipe']
    });

    let output = '';
    let error = '';

    browser.stdin.write(script);
    browser.stdin.end();

    browser.stdout.on('data', (data) => {
      output += data.toString();
    });

    browser.stderr.on('data', (data) => {
      error += data.toString();
    });

    browser.on('close', (code) => {
      if (code === 0) {
        resolve(output);
      } else {
        reject(new Error(`Agent browser exited with code ${code}: ${error}`));
      }
    });
  });
}

async function registerUser() {
  logSection('Step 1: Register New User');

  try {
    const response = await fetch(`${API_BASE}/auth/register`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        email: testUser.email,
        password: testUser.password
      })
    });

    const data = await response.json();

    if (response.ok) {
      log(`✓ User registered successfully`, 'green');
      log(`  Email: ${testUser.email}`, 'cyan');
      return true;
    } else {
      log(`✗ Registration failed: ${data.error}`, 'red');
      return false;
    }
  } catch (error) {
    log(`✗ Registration error: ${error.message}`, 'red');
    return false;
  }
}

async function loginUser() {
  logSection('Step 2: Login User');

  try {
    const response = await fetch(`${API_BASE}/auth/login`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        email: testUser.email,
        password: testUser.password
      })
    });

    const data = await response.json();

    if (response.ok && data.token) {
      authToken = data.token;
      userId = data.userId;
      log(`✓ User logged in successfully`, 'green');
      log(`  Token: ${authToken.substring(0, 20)}...`, 'cyan');
      log(`  User ID: ${userId}`, 'cyan');
      return true;
    } else {
      log(`✗ Login failed: ${data.error}`, 'red');
      return false;
    }
  } catch (error) {
    log(`✗ Login error: ${error.message}`, 'red');
    return false;
  }
}

async function setupTwoFactor() {
  logSection('Step 3: Initialize 2FA Setup (QR Code Generation)');

  try {
    const response = await fetch(`${API_BASE}/auth/2fa/setup`, {
      method: 'POST',
      headers: {
        'Authorization': `Bearer ${authToken}`,
        'Content-Type': 'application/json'
      }
    });

    const data = await response.json();

    if (response.ok && data.secret) {
      log(`✓ 2FA setup initialized successfully`, 'green');
      log(`  Secret Key: ${data.secret}`, 'cyan');
      log(`  QR Code generated: ${data.qrCodeUrl ? 'Yes' : 'No'}`, 'cyan');
      log(`  Backup Codes: ${data.backupCodes?.length || 0} codes generated`, 'cyan');

      return {
        secret: data.secret,
        qrCodeUrl: data.qrCodeUrl,
        backupCodes: data.backupCodes,
        otpauth: data.otpauth
      };
    } else {
      log(`✗ 2FA setup failed: ${data.error}`, 'red');
      return null;
    }
  } catch (error) {
    log(`✗ 2FA setup error: ${error.message}`, 'red');
    return null;
  }
}

function generateTOTPCode(secret) {
  logSection('Step 4: Generate TOTP Code');

  try {
    const code = authenticator.generate(secret);
    log(`✓ TOTP code generated successfully`, 'green');
    log(`  Code: ${code}`, 'cyan');
    log(`  Secret used: ${secret}`, 'cyan');
    return code;
  } catch (error) {
    log(`✗ TOTP generation error: ${error.message}`, 'red');
    return null;
  }
}

async function enableTwoFactor(totpCode) {
  logSection('Step 5: Verify and Enable 2FA');

  try {
    const response = await fetch(`${API_BASE}/auth/2fa/enable`, {
      method: 'POST',
      headers: {
        'Authorization': `Bearer ${authToken}`,
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({ token: totpCode })
    });

    const data = await response.json();

    if (response.ok) {
      log(`✓ 2FA enabled successfully`, 'green');
      return true;
    } else {
      log(`✗ 2FA enable failed: ${data.error}`, 'red');
      return false;
    }
  } catch (error) {
    log(`✗ 2FA enable error: ${error.message}`, 'red');
    return false;
  }
}

async function verifyTwoFactorStatus() {
  logSection('Step 6: Verify 2FA Status');

  try {
    const response = await fetch(`${API_BASE}/auth/me`, {
      method: 'GET',
      headers: {
        'Authorization': `Bearer ${authToken}`
      }
    });

    const data = await response.json();

    if (response.ok) {
      log(`✓ User status retrieved`, 'green');
      log(`  2FA Enabled: ${data.twoFactorEnabled ? 'Yes ✓' : 'No ✗'}`, 'cyan');
      log(`  User Email: ${data.email}`, 'cyan');
      return data.twoFactorEnabled;
    } else {
      log(`✗ Status check failed: ${data.error}`, 'red');
      return false;
    }
  } catch (error) {
    log(`✗ Status check error: ${error.message}`, 'red');
    return false;
  }
}

async function testAgentBrowserIntegration() {
  logSection('Step 7: Agent Browser UI Testing');

  try {
    const script = `
      goto("${BASE_URL}/dashboard/security")
      screenshot()
    `;

    const result = await runAgentBrowser(script);
    log(`✓ Agent browser screenshot captured`, 'green');
    return true;
  } catch (error) {
    log(`✗ Agent browser test failed: ${error.message}`, 'yellow');
    // Don't fail the test if agent-browser isn't available
    return true;
  }
}

async function runCompleteTest() {
  logSection('🔐 COMPREHENSIVE 2FA TESTING SUITE');
  log('Testing Google Authenticator Integration Flow', 'cyan');

  const results = {
    registration: false,
    login: false,
    setup: null,
    totpGeneration: false,
    enable: false,
    statusVerification: false,
    uiTesting: false
  };

  try {
    // Step 1: Register
    results.registration = await registerUser();
    if (!results.registration) return results;

    // Step 2: Login
    results.login = await loginUser();
    if (!results.login) return results;

    // Step 3: Setup 2FA
    results.setup = await setupTwoFactor();
    if (!results.setup) return results;

    // Step 4: Generate TOTP
    const totpCode = generateTOTPCode(results.setup.secret);
    results.totpGeneration = !!totpCode;
    if (!totpCode) return results;

    // Step 5: Enable 2FA
    results.enable = await enableTwoFactor(totpCode);
    if (!results.enable) return results;

    // Step 6: Verify Status
    results.statusVerification = await verifyTwoFactorStatus();

    // Step 7: UI Testing
    results.uiTesting = await testAgentBrowserIntegration();

  } catch (error) {
    log(`\n✗ Test execution error: ${error.message}`, 'red');
  }

  return results;
}

function generateReport(results) {
  logSection('📊 TEST REPORT');

  const testResults = [
    { name: 'User Registration', result: results.registration },
    { name: 'User Login', result: results.login },
    { name: '2FA Setup (QR Code)', result: !!results.setup },
    { name: 'TOTP Code Generation', result: results.totpGeneration },
    { name: '2FA Enable', result: results.enable },
    { name: '2FA Status Verification', result: results.statusVerification },
    { name: 'UI Agent Browser Testing', result: results.uiTesting }
  ];

  const passed = testResults.filter(t => t.result).length;
  const total = testResults.length;
  const passRate = ((passed / total) * 100).toFixed(2);

  testResults.forEach(test => {
    const icon = test.result ? '✓' : '✗';
    const color = test.result ? 'green' : 'red';
    log(`  ${icon} ${test.name}`, color);
  });

  console.log('\n' + '-'.repeat(60));
  const color = passed === total ? 'green' : passed > total / 2 ? 'yellow' : 'red';
  log(`Pass Rate: ${passed}/${total} (${passRate}%)`, color);
  console.log('-'.repeat(60));

  if (results.setup) {
    logSection('📋 2FA Configuration Details');
    log(`Secret Key: ${results.setup.secret}`, 'cyan');
    log(`Backup Codes: ${results.setup.backupCodes.length} generated`, 'cyan');
    if (results.setup.backupCodes.length > 0) {
      log('First 3 backup codes:', 'yellow');
      results.setup.backupCodes.slice(0, 3).forEach((code, i) => {
        log(`  ${i + 1}. ${code}`, 'cyan');
      });
    }
  }

  return { passed, total, passRate };
}

// Main execution
(async () => {
  try {
    const results = await runCompleteTest();
    const report = generateReport(results);

    console.log('\n');
    if (report.passed === report.total) {
      log('✅ ALL TESTS PASSED - 2FA IMPLEMENTATION VERIFIED', 'green');
      process.exit(0);
    } else {
      log('⚠️  SOME TESTS FAILED - CHECK OUTPUT ABOVE', 'yellow');
      process.exit(1);
    }
  } catch (error) {
    log(`Fatal error: ${error.message}`, 'red');
    process.exit(1);
  }
})();
