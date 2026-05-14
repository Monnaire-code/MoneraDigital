#!/usr/bin/env node

/**
 * Vercel API代理诊断脚本
 * 测试Vercel到后端的API代理是否正常工作
 */

import https from 'https';
import { URL } from 'url';

const vercelUrl = 'https://www.moneradigital.com';
const backendUrl = 'https://monera-digital--gyc567.replit.app';

function makeRequest(url, method, path, body = null) {
  return new Promise((resolve, reject) => {
    const fullUrl = new URL(path, url);

    const options = {
      method: method,
      headers: {
        'Content-Type': 'application/json',
      },
      timeout: 15000,
    };

    const req = https.request(fullUrl, options, (res) => {
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

async function testAPIs() {
  console.log('🔍 Vercel API代理诊断\n');

  const testEmail = `test-${Date.now()}@example.com`;
  const testPassword = 'TestPassword123!';

  // 测试1: 直接测试后端
  console.log('📝 测试1: 直接测试后端 POST /api/auth/register');
  try {
    const response = await makeRequest(backendUrl, 'POST', '/api/auth/register', {
      email: testEmail,
      password: testPassword,
    });

    console.log(`状态码: ${response.status}`);
    console.log(`响应体: ${response.body.substring(0, 200)}\n`);
  } catch (error) {
    console.log(`❌ 错误: ${error.message}\n`);
  }

  // 测试2: 通过Vercel代理测试
  console.log('📝 测试2: 通过Vercel代理 POST /api/auth/register');
  try {
    const response = await makeRequest(vercelUrl, 'POST', '/api/auth/register', {
      email: testEmail,
      password: testPassword,
    });

    console.log(`状态码: ${response.status}`);
    console.log(`响应体: ${response.body.substring(0, 200)}\n`);
  } catch (error) {
    console.log(`❌ 错误: ${error.message}\n`);
  }

  // 测试3: 测试Vercel前端
  console.log('📝 测试3: 测试Vercel前端 GET /');
  try {
    const response = await makeRequest(vercelUrl, 'GET', '/');

    console.log(`状态码: ${response.status}`);
    console.log(`响应体长度: ${response.body.length} 字节\n`);
  } catch (error) {
    console.log(`❌ 错误: ${error.message}\n`);
  }
}

testAPIs().catch(console.error);
