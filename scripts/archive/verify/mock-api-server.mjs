import http from 'http';
import { fileURLToPath } from 'url';
import { dirname, resolve } from 'path';
import fs from 'fs';
import path from 'path';

const __dirname = dirname(fileURLToPath(import.meta.url));
const projectDir = __dirname;

const PORT = 8081;

// Simple request handler to parse URL and body
async function handleRequest(req, res) {
  // Parse URL
  const urlParts = req.url.split('?')[0];
  const method = req.method;

  // Set CORS headers
  res.setHeader('Access-Control-Allow-Origin', '*');
  res.setHeader('Access-Control-Allow-Methods', 'GET, POST, PUT, DELETE, OPTIONS');
  res.setHeader('Access-Control-Allow-Headers', 'Content-Type, Authorization');
  res.setHeader('Content-Type', 'application/json');

  if (method === 'OPTIONS') {
    res.writeHead(200);
    res.end();
    return;
  }

  // Parse request body
  let body = '';
  await new Promise((resolve) => {
    req.on('data', chunk => {
      body += chunk.toString();
    });
    req.on('end', () => {
      resolve();
    });
  });

  req.body = body ? JSON.parse(body) : {};

  // Mock API responses for testing
  try {
    if (urlParts === '/api/auth/register' && method === 'POST') {
      // Simulate registration
      res.writeHead(200);
      res.end(JSON.stringify({
        success: true,
        message: 'User registered successfully',
        userId: 1,
        email: req.body.email
      }));
    } else if (urlParts === '/health' && method === 'GET') {
      res.writeHead(200);
      res.end('ok');
    } else if (urlParts === '/api/core/health' && method === 'GET') {
      res.writeHead(200);
      res.end('healthy');
    } else if (urlParts === '/api/core/accounts/create' && method === 'POST') {
      res.writeHead(200);
      res.end(JSON.stringify({
        data: { accountId: "mock_acc_1" }
      }));
    } else if (urlParts.startsWith('/api/core/accounts/') && method === 'GET') {
      res.writeHead(200);
      res.end(JSON.stringify({
        data: { status: "ACTIVE", kycStatus: "VERIFIED" }
      }));
    } else if (urlParts === '/api/auth/login' && method === 'POST') {
      // Simulate login
      res.writeHead(200);
      res.end(JSON.stringify({
        success: true,
        token: 'eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VySWQiOjEsImVtYWlsIjoitest@example.com"}',
        userId: 1,
        email: req.body.email
      }));
    } else if (urlParts === '/api/auth/2fa/setup' && method === 'POST') {
      // Simulate 2FA setup - return mock secret and QR code
      const secret = 'JBSWY3DPEBLW64TMMQ======';
      const qrCodeUrl = 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==';
      const backupCodes = Array.from({ length: 10 }, () => Math.random().toString(16).substring(2, 10).toUpperCase());

      res.writeHead(200);
      res.end(JSON.stringify({
        secret,
        qrCodeUrl,
        backupCodes,
        otpauth: 'otpauth://totp/Monera%20Digital:test@example.com?secret=' + secret
      }));
    } else if (urlParts === '/api/auth/2fa/enable' && method === 'POST') {
      // Simulate 2FA enable
      res.writeHead(200);
      res.end(JSON.stringify({ success: true }));
    } else if (urlParts === '/api/auth/me' && method === 'GET') {
      // Simulate get user info
      res.writeHead(200);
      res.end(JSON.stringify({
        userId: 1,
        email: 'test@example.com',
        twoFactorEnabled: true,
        createdAt: new Date().toISOString()
      }));
    } else {
      res.writeHead(404);
      res.end(JSON.stringify({ error: 'Not found' }));
    }
  } catch (error) {
    console.error('Error handling request:', error);
    res.writeHead(500);
    res.end(JSON.stringify({ error: 'Internal Server Error' }));
  }
}

const server = http.createServer(handleRequest);

server.listen(PORT, () => {
  console.log(`Mock API Server running on http://localhost:${PORT}`);
  console.log('Available endpoints:');
  console.log('  POST /api/auth/register');
  console.log('  POST /api/auth/login');
  console.log('  POST /api/auth/2fa/setup');
  console.log('  POST /api/auth/2fa/enable');
  console.log('  GET  /api/auth/me');
});
