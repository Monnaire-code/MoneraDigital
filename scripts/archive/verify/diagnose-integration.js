#!/usr/bin/env node

/**
 * 系统诊断脚本
 * 检查前后端集成的各个组件是否正常工作
 */

import { spawn } from 'child_process';
import fs from 'fs';
import path from 'path';

const colors = {
  reset: '\x1b[0m',
  green: '\x1b[32m',
  red: '\x1b[31m',
  yellow: '\x1b[33m',
  blue: '\x1b[34m',
  cyan: '\x1b[36m',
};

function log(message, color = 'reset') {
  console.log(`${colors[color]}${message}${colors.reset}`);
}

function checkEnvironmentVariables() {
  log('\n=== 检查环境变量 ===', 'blue');

  const requiredVars = ['DATABASE_URL', 'JWT_SECRET'];
  const envFile = '.env';

  if (!fs.existsSync(envFile)) {
    log('✗ .env 文件不存在', 'red');
    return false;
  }

  const envContent = fs.readFileSync(envFile, 'utf-8');
  let allPresent = true;

  requiredVars.forEach((varName) => {
    if (envContent.includes(varName)) {
      log(`✓ ${varName} 已配置`, 'green');
    } else {
      log(`✗ ${varName} 未配置`, 'red');
      allPresent = false;
    }
  });

  return allPresent;
}

function checkFileStructure() {
  log('\n=== 检查文件结构 ===', 'blue');

  const requiredFiles = [
    'src/pages/Register.tsx',
    'src/pages/Login.tsx',
    'api/auth/register.ts',
    'api/auth/login.ts',
    'src/lib/auth-service.ts',
    'vite.config.ts',
    'scripts/start-replit.sh',
  ];

  let allPresent = true;

  requiredFiles.forEach((file) => {
    if (fs.existsSync(file)) {
      log(`✓ ${file} 存在`, 'green');
    } else {
      log(`✗ ${file} 不存在`, 'red');
      allPresent = false;
    }
  });

  return allPresent;
}

function checkViteConfig() {
  log('\n=== 检查Vite代理配置 ===', 'blue');

  const viteConfig = fs.readFileSync('vite.config.ts', 'utf-8');

  const checks = [
    {
      name: '代理配置存在',
      pattern: /proxy:\s*{/,
    },
    {
      name: '/api 代理配置',
      pattern: /"\s*\/api\s*":/,
    },
    {
      name: '代理目标为 localhost:8081',
      pattern: /target:\s*"http:\/\/localhost:8081"/,
    },
  ];

  let allPassed = true;

  checks.forEach((check) => {
    if (check.pattern.test(viteConfig)) {
      log(`✓ ${check.name}`, 'green');
    } else {
      log(`✗ ${check.name}`, 'red');
      allPassed = false;
    }
  });

  return allPassed;
}

function checkBackendConfig() {
  log('\n=== 检查后端配置 ===', 'blue');

  const startScript = fs.readFileSync('scripts/start-replit.sh', 'utf-8');

  const checks = [
    {
      name: '后端构建命令',
      pattern: /go build/,
    },
    {
      name: '后端启动命令',
      pattern: /\.\/server_bin/,
    },
    {
      name: '前端启动命令',
      pattern: /npm run dev/,
    },
    {
      name: 'PORT 设置为 8081',
      pattern: /PORT=8081/,
    },
  ];

  let allPassed = true;

  checks.forEach((check) => {
    if (check.pattern.test(startScript)) {
      log(`✓ ${check.name}`, 'green');
    } else {
      log(`✗ ${check.name}`, 'red');
      allPassed = false;
    }
  });

  return allPassed;
}

function checkFrontendCode() {
  log('\n=== 检查前端代码 ===', 'blue');

  const registerCode = fs.readFileSync('src/pages/Register.tsx', 'utf-8');
  const loginCode = fs.readFileSync('src/pages/Login.tsx', 'utf-8');

  const checks = [
    {
      name: 'Register.tsx 使用 /api/auth/register',
      pattern: /\/api\/auth\/register/,
      file: registerCode,
    },
    {
      name: 'Login.tsx 使用 /api/auth/login',
      pattern: /\/api\/auth\/login/,
      file: loginCode,
    },
    {
      name: 'Register.tsx 处理响应',
      pattern: /res\.json\(\)/,
      file: registerCode,
    },
    {
      name: 'Login.tsx 存储token',
      pattern: /localStorage\.setItem\("token"/,
      file: loginCode,
    },
  ];

  let allPassed = true;

  checks.forEach((check) => {
    if (check.pattern.test(check.file)) {
      log(`✓ ${check.name}`, 'green');
    } else {
      log(`✗ ${check.name}`, 'red');
      allPassed = false;
    }
  });

  return allPassed;
}

function checkBackendCode() {
  log('\n=== 检查后端代码 ===', 'blue');

  const registerApi = fs.readFileSync('api/auth/register.ts', 'utf-8');
  const loginApi = fs.readFileSync('api/auth/login.ts', 'utf-8');

  const checks = [
    {
      name: 'register.ts 导入 AuthService',
      pattern: /AuthService/,
      file: registerApi,
    },
    {
      name: 'login.ts 导入 AuthService',
      pattern: /AuthService/,
      file: loginApi,
    },
    {
      name: 'register.ts 返回 201 状态码',
      pattern: /status\(201\)/,
      file: registerApi,
    },
    {
      name: 'login.ts 返回 200 状态码',
      pattern: /status\(200\)/,
      file: loginApi,
    },
    {
      name: 'register.ts 有速率限制',
      pattern: /rateLimit/,
      file: registerApi,
    },
    {
      name: 'login.ts 有速率限制',
      pattern: /rateLimit/,
      file: loginApi,
    },
  ];

  let allPassed = true;

  checks.forEach((check) => {
    if (check.pattern.test(check.file)) {
      log(`✓ ${check.name}`, 'green');
    } else {
      log(`✗ ${check.name}`, 'red');
      allPassed = false;
    }
  });

  return allPassed;
}

function generateReport() {
  log('\n╔════════════════════════════════════════════════════════╗', 'blue');
  log('║           前后端集成诊断报告                          ║', 'blue');
  log('╚════════════════════════════════════════════════════════╝', 'blue');

  const results = {
    环境变量: checkEnvironmentVariables(),
    文件结构: checkFileStructure(),
    Vite配置: checkViteConfig(),
    后端配置: checkBackendConfig(),
    前端代码: checkFrontendCode(),
    后端代码: checkBackendCode(),
  };

  log('\n=== 诊断总结 ===', 'blue');

  Object.entries(results).forEach(([name, passed]) => {
    const status = passed ? '✓ 通过' : '✗ 失败';
    const color = passed ? 'green' : 'red';
    log(`${status}: ${name}`, color);
  });

  const allPassed = Object.values(results).every((r) => r);

  if (allPassed) {
    log('\n✓ 所有检查都通过了！系统配置正确。', 'green');
    log('\n下一步:', 'cyan');
    log('1. 运行: npm run dev', 'cyan');
    log('2. 打开浏览器访问: http://localhost:8080', 'cyan');
    log('3. 测试注册和登陆功能', 'cyan');
  } else {
    log('\n✗ 发现配置问题。请查看上面的错误信息。', 'red');
    log('\n修复步骤:', 'cyan');
    log('1. 检查 .env 文件中的环境变量', 'cyan');
    log('2. 确保所有必需的文件都存在', 'cyan');
    log('3. 检查 vite.config.ts 中的代理配置', 'cyan');
    log('4. 检查 scripts/start-replit.sh 中的启动脚本', 'cyan');
  }

  return allPassed;
}

generateReport();
