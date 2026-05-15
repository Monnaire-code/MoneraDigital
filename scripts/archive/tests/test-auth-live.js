import { AuthService } from '../src/lib/auth-service.js';
import dotenv from 'dotenv';
import path from 'path';
import { fileURLToPath } from 'url';
import sql from '../src/lib/db.js';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
dotenv.config({ path: path.resolve(__dirname, '../.env') });

async function test() {
  const testEmail = `test-${Date.now()}@example.com`;
  const testPassword = 'password123';

  console.log('--- Starting Live Auth Test ---');
  console.log('Test Email:', testEmail);

  try {
    // 1. 测试注册
    console.log('\n1. Testing Registration...');
    const user = await AuthService.register(testEmail, testPassword);
    console.log('Registration successful:', user);

    // 2. 测试重复注册
    console.log('\n2. Testing Duplicate Registration...');
    try {
      await AuthService.register(testEmail, testPassword);
      console.error('Error: Duplicate registration should have failed!');
    } catch (e) {
      console.log('Success: Duplicate registration failed as expected:', e.message);
    }

    // 3. 测试登录
    console.log('\n3. Testing Login...');
    const loginResult = await AuthService.login(testEmail, testPassword);
    console.log('Login successful! Token generated.');
    console.log('User:', loginResult.user);

    // 4. 测试错误密码
    console.log('\n4. Testing Wrong Password...');
    try {
      await AuthService.login(testEmail, 'wrong-password');
      console.error('Error: Login with wrong password should have failed!');
    } catch (e) {
      console.log('Success: Login with wrong password failed as expected:', e.message);
    }

    // 清理测试数据 (可选)
    console.log('\n5. Cleaning up test user...');
    await sql`DELETE FROM users WHERE email = ${testEmail}`;
    console.log('Cleanup complete.');

    console.log('\n--- All Live Tests Passed! ---');
    process.exit(0);
  } catch (error) {
    console.error('\n--- Test Failed! ---');
    console.error(error);
    process.exit(1);
  }
}

test();
