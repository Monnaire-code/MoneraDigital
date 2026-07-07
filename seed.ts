import postgres from 'postgres';
import dotenv from 'dotenv';

dotenv.config();

const DATABASE_URL = process.env.DATABASE_URL;
if (!DATABASE_URL) {
  throw new Error(
    'DATABASE_URL environment variable is required. ' +
      'Set it in .env (copy from .env.example) or pass it inline: ' +
      'DATABASE_URL="postgresql://user:pass@host:port/db?sslmode=require" npx tsx seed.ts'
  );
}

async function seed() {
  console.log('Connecting to database...');
  const sql = postgres(DATABASE_URL);

  try {
    // Get existing users
    console.log('Checking for existing users...');
    const users = await sql`SELECT id FROM users LIMIT 1`;
    const userId = users[0]?.id || 118;
    console.log(`Using user ID: ${userId}`);

    // Insert wealth products
    console.log('\n=== Inserting Wealth Products ===');
    await sql`
      INSERT INTO wealth_product (title, currency, apy, duration, min_amount, max_amount, total_quota, sold_quota, status, auto_renew_allowed, created_at, updated_at)
      VALUES 
        ('USDT 7日增值', 'USDT', '5.50', 7, '100', '50000', '1000000', '0', 2, true, NOW(), NOW()),
        ('USDT 30日稳健', 'USDT', '8.00', 30, '500', '100000', '5000000', '0', 2, true, NOW(), NOW()),
        ('USDT 90日高息', 'USDT', '12.00', 90, '1000', '200000', '10000000', '0', 2, true, NOW(), NOW()),
        ('USDT 180日 Plus', 'USDT', '15.00', 180, '5000', '500000', '20000000', '0', 2, true, NOW(), NOW()),
        ('USDT 360日旗舰', 'USDT', '18.00', 360, '10000', '1000000', '50000000', '0', 2, true, NOW(), NOW()),
        ('USDC 30日稳健', 'USDC', '7.50', 30, '500', '100000', '5000000', '0', 2, true, NOW(), NOW())
      ON CONFLICT DO NOTHING
    `;
    console.log('✓ 6 products inserted');

    // Insert user accounts
    console.log('\n=== Inserting User Accounts ===');
    await sql`
      INSERT INTO account (user_id, type, currency, balance, frozen_balance, version, created_at, updated_at)
      VALUES 
        (${userId}, 'FUND', 'USDT', '1000000', '0', 1, NOW(), NOW()),
        (${userId}, 'FUND', 'USDC', '500000', '0', 1, NOW(), NOW()),
        (${userId}, 'FUND', 'BTC', '10', '0', 1, NOW(), NOW()),
        (${userId}, 'FUND', 'ETH', '100', '0', 1, NOW(), NOW())
      ON CONFLICT DO NOTHING
    `;
    console.log('✓ 4 accounts inserted');

    // Insert sample orders
    console.log('\n=== Inserting Sample Orders ===');
    await sql`
      INSERT INTO wealth_order (user_id, product_id, amount, interest_expected, interest_paid, interest_accrued, start_date, end_date, auto_renew, status, created_at, updated_at)
      VALUES 
        (${userId}, 1, '5000', '52.88', '0', '18.21', '2026-01-10', '2026-01-17', true, 1, NOW(), NOW()),
        (${userId}, 2, '10000', '657.53', '0', '215.34', '2026-01-05', '2026-02-04', false, 1, NOW(), NOW()),
        (${userId}, 3, '20000', '5917.81', '0', '1315.07', '2025-12-20', '2026-03-20', true, 1, NOW(), NOW())
      ON CONFLICT DO NOTHING
    `;
    console.log('✓ 3 orders inserted');

    console.log('\n✅ Seed completed successfully!');

    // Verify data
    console.log('\n=== Verification ===');
    const products = await sql`SELECT COUNT(*) as count FROM wealth_product`;
    const accounts = await sql`SELECT COUNT(*) as count FROM account`;
    const orders = await sql`SELECT COUNT(*) as count FROM wealth_order`;
    console.log(`Products: ${products[0].count}`);
    console.log(`Accounts: ${accounts[0].count}`);
    console.log(`Orders: ${orders[0].count}`);

  } catch (error) {
    console.error('Seed failed:', error);
  } finally {
    await sql.end();
  }
}

seed();
