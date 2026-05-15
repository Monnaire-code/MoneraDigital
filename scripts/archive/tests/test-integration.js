#!/usr/bin/env node

/**
 * 前后端集成测试脚本
 * 用于验证注册和登陆功能的完整流程
 */

import fetch from 'node-fetch';

const API_BASE_URL = 'http://localhost:8081';
const FRONTEND_URL = 'http://localhost:8080';

// 测试数据
const testUser = {
  email: `test-${Date.now()}@example.com`,
  password: 'TestPassword123!',
};

let testResults = {
  passed: 0,
  failed: 0,
  errors: [],
};

// 颜色输出
const colors = {
  reset: '\x1b[0m',
  green: '\x1b[32m',
  red: '\x1b[31m',
  yellow: '\x1b[33m',
  blue: '\x1b[34m',
};

function log(message, color = 'reset') {
  console.log(`${colors[color]}${message}${colors.reset}`);
}

function logTest(name, passed, error = null) {
  if (passed) {
    log(`✓ ${name}`, 'green');
    testResults.passed++;
  } else {
    log(`✗ ${name}`, 'red');
    if (error) {
      log(`  Error: ${error}`, 'red');
      testResults.errors.push({ test: name, error });
    }
    testResults.failed++;
  }
}

async function testBackendConnection() {
  log('\n=== 测试后端连接 ===', 'blue');
  try {
    const response = await fetch(`${API_BASE_URL}/api/auth/register`, {
      method: 'OPTIONS',
    });
    logTest('后端连接', response.status !== 404);
  } catch (error) {
    logTest('后端连接', false, error.message);
  }
}

async function testRegistration() {
  log('\n=== 测试注册功能 ===', 'blue');

  // 测试1: 成功注册
  try {
    const response = await fetch(`${API_BASE_URL}/api/auth/register`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        email: testUser.email,
        password: testUser.password,
      }),
    });

    const data = await response.json();
    const passed =
      response.status === 201 &&
      data.user &&
      data.user.email === testUser.email;

    logTest('成功注册用户', passed, !passed ? JSON.stringify(data) : null);
  } catch (error) {
    logTest('成功注册用户', false, error.message);
  }

  // 测试2: 重复注册应该失败
  try {
    const response = await fetch(`${API_BASE_URL}/api/auth/register`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        email: testUser.email,
        password: testUser.password,
      }),
    });

    const passed = response.status === 400;
    logTest('拒绝重复邮箱注册', passed);
  } catch (error) {
    logTest('拒绝重复邮箱注册', false, error.message);
  }

  // 测试3: 无效邮箱应该失败
  try {
    const response = await fetch(`${API_BASE_URL}/api/auth/register`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        email: 'invalid-email',
        password: testUser.password,
      }),
    });

    const passed = response.status === 400;
    logTest('拒绝无效邮箱', passed);
  } catch (error) {
    logTest('拒绝无效邮箱', false, error.message);
  }

  // 测试4: 弱密码应该失败
  try {
    const response = await fetch(`${API_BASE_URL}/api/auth/register`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        email: `weak-${Date.now()}@example.com`,
        password: '123',
      }),
    });

    const passed = response.status === 400;
    logTest('拒绝弱密码', passed);
  } catch (error) {
    logTest('拒绝弱密码', false, error.message);
  }
}

async function testLogin() {
  log('\n=== 测试登陆功能 ===', 'blue');

  // 测试1: 成功登陆
  try {
    const response = await fetch(`${API_BASE_URL}/api/auth/login`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        email: testUser.email,
        password: testUser.password,
      }),
    });

    const data = await response.json();
    const passed =
      response.status === 200 &&
      data.token &&
      data.user &&
      data.user.email === testUser.email;

    logTest('成功登陆', passed, !passed ? JSON.stringify(data) : null);

    if (passed) {
      // 保存token用于后续测试
      global.authToken = data.token;
    }
  } catch (error) {
    logTest('成功登陆', false, error.message);
  }

  // 测试2: 错误密码应该失败
  try {
    const response = await fetch(`${API_BASE_URL}/api/auth/login`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        email: testUser.email,
        password: 'WrongPassword123!',
      }),
    });

    const passed = response.status === 401;
    logTest('拒绝错误密码', passed);
  } catch (error) {
    logTest('拒绝错误密码', false, error.message);
  }

  // 测试3: 不存在的用户应该失败
  try {
    const response = await fetch(`${API_BASE_URL}/api/auth/login`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        email: 'nonexistent@example.com',
        password: 'SomePassword123!',
      }),
    });

    const passed = response.status === 401;
    logTest('拒绝不存在的用户', passed);
  } catch (error) {
    logTest('拒绝不存在的用户', false, error.message);
  }
}

async function testTokenValidation() {
  log('\n=== 测试Token验证 ===', 'blue');

  if (!global.authToken) {
    log('跳过Token验证测试（未获得有效token）', 'yellow');
    return;
  }

  // 测试1: 有效token应该成功
  try {
    const response = await fetch(`${API_BASE_URL}/api/auth/me`, {
      method: 'GET',
      headers: {
        'Authorization': `Bearer ${global.authToken}`,
      },
    });

    const data = await response.json();
    const passed =
      response.status === 200 &&
      data.email === testUser.email;

    logTest('有效token获取用户信息', passed, !passed ? JSON.stringify(data) : null);
  } catch (error) {
    logTest('有效token获取用户信息', false, error.message);
  }

  // 测试2: 无效token应该失败
  try {
    const response = await fetch(`${API_BASE_URL}/api/auth/me`, {
      method: 'GET',
      headers: {
        'Authorization': 'Bearer invalid_token_here',
      },
    });

    const passed = response.status === 401;
    logTest('拒绝无效token', passed);
  } catch (error) {
    logTest('拒绝无效token', false, error.message);
  }

  // 测试3: 没有token应该失败
  try {
    const response = await fetch(`${API_BASE_URL}/api/auth/me`, {
      method: 'GET',
    });

    const passed = response.status === 401;
    logTest('拒绝没有token的请求', passed);
  } catch (error) {
    logTest('拒绝没有token的请求', false, error.message);
  }
}

async function testFrontendConnection() {
  log('\n=== 测试前端连接 ===', 'blue');
  try {
    const response = await fetch(FRONTEND_URL);
    logTest('前端连接', response.status === 200);
  } catch (error) {
    logTest('前端连接', false, error.message);
  }
}

async function runAllTests() {
  log('\n╔════════════════════════════════════════════════════════╗', 'blue');
  log('║     前后端集成测试 - 注册和登陆功能                    ║', 'blue');
  log('╚════════════════════════════════════════════════════════╝', 'blue');

  await testBackendConnection();
  await testFrontendConnection();
  await testRegistration();
  await testLogin();
  await testTokenValidation();

  // 输出测试总结
  log('\n╔════════════════════════════════════════════════════════╗', 'blue');
  log('║                    测试总结                            ║', 'blue');
  log('╚════════════════════════════════════════════════════════╝', 'blue');

  log(`\n通过: ${testResults.passed}`, 'green');
  log(`失败: ${testResults.failed}`, testResults.failed > 0 ? 'red' : 'green');

  if (testResults.errors.length > 0) {
    log('\n错误详情:', 'red');
    testResults.errors.forEach((err) => {
      log(`  - ${err.test}: ${err.error}`, 'red');
    });
  }

  const totalTests = testResults.passed + testResults.failed;
  const passRate = ((testResults.passed / totalTests) * 100).toFixed(2);
  log(`\n通过率: ${passRate}%`, passRate === '100.00' ? 'green' : 'yellow');

  process.exit(testResults.failed > 0 ? 1 : 0);
}

runAllTests().catch((error) => {
  log(`\n致命错误: ${error.message}`, 'red');
  process.exit(1);
});
