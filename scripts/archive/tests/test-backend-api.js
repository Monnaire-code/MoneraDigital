#!/usr/bin/env node

/**
 * 后端API诊断脚本
 * 测试Replit后端服务器的API端点
 */

import https from 'https';
import { URL } from 'url';

const backendUrl = 'https://monera-digital--gyc567.replit.app';

function makeRequest(method, path, body = null) {
  return new Promise((resolve, reject) => {
    const url = new URL(path, backendUrl);

    const options = {
      method: method,
      headers: {
        'Content-Type': 'application/json',
      },
      timeout: 10000,
    };

    const req = https.request(url, options, (res) => {
      let data = '';

      res.on('data', (chunk) => {
        data += chunk;
      });

      res.on('end', () => {
        resolve({
          status: res.statusCode,
          headers: res.headers,
          body: data,
        });
      });
    });

    req.on('error', (error) => {
      reject(error);
    });

    req.on('timeout', () => {
      req.destroy();
      reject(new Error('Request timeout'));
    });

    if (body) {
      req.write(JSON.stringify(body));
    }

    req.end();
  });
}

async function testBackendAPI() {
  console.log('🔍 后端API诊断\n');
  console.log(`后端地址: ${backendUrl}\n`);

  // 测试1: 注册端点
  console.log('📝 测试1: POST /api/auth/register');
  try {
    const response = await makeRequest('POST', '/api/auth/register', {
      email: `test-${Date.now()}@example.com`,
      password: 'TestPassword123!',
    });

    console.log(`状态码: ${response.status}`);
    console.log(`响应体: ${response.body}\n`);
  } catch (error) {
    console.log(`❌ 错误: ${error.message}\n`);
  }

  // 测试2: 登陆端点
  console.log('📝 测试2: POST /api/auth/login');
  try {
    const response = await makeRequest('POST', '/api/auth/login', {
      email: 'test@example.com',
      password: 'TestPassword123!',
    });

    console.log(`状态码: ${response.status}`);
    console.log(`响应体: ${response.body}\n`);
  } catch (error) {
    console.log(`❌ 错误: ${error.message}\n`);
  }

  // 测试3: 检查根路径
  console.log('📝 测试3: GET /');
  try {
    const response = await makeRequest('GET', '/');

    console.log(`状态码: ${response.status}`);
    console.log(`响应体长度: ${response.body.length} 字节\n`);
  } catch (error) {
    console.log(`❌ 错误: ${error.message}\n`);
  }
}

testBackendAPI().catch(console.error);
