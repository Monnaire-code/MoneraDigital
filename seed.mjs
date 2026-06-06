import { drizzle } from 'drizzle-orm/postgres-js';
import postgres from 'postgres';
import dotenv from 'dotenv';
import { accounts, wealthProducts, wealthOrders } from './src/db/schema.js';

dotenv.config();

const DATABASE_URL = process.env.DATABASE_URL;
if (!DATABASE_URL) {
  throw new Error(
    'DATABASE_URL environment variable is required. ' +
      'Set it in .env (copy from .env.example) or pass it inline: ' +
      'DATABASE_URL="postgresql://user:pass@host:port/db?sslmode=require" node seed.mjs'
  );
}

async function seed() {
  console.log('Connecting to database...');
  const client = postgres(DATABASE_URL);
  const db = drizzle(client);

  try {
    // Get existing users to find a user ID
    console.log('Checking for existing users...');
    const users = await db.query.users.findMany({ limit: 1 });
    const userId = users[0]?.id || 118; // Use 118 if exists, otherwise use test user

    console.log(`Using user ID: ${userId}`);

    // Insert wealth products
    console.log('\n=== Inserting Wealth Products ===');
    const productData = [
      {
        title: 'USDT 7日增值',
        currency: 'USDT',
        apy: '5.50',
        duration: 7,
        minAmount: '100',
        maxAmount: '50000',
        totalQuota: '1000000',
        soldQuota: '0',
        status: 2, // Open
        autoRenewAllowed: true,
      },
      {
        title: 'USDT 30日稳健',
        currency: 'USDT',
        apy: '8.00',
        duration: 30,
        minAmount: '500',
        maxAmount: '100000',
        totalQuota: '5000000',
        soldQuota: '0',
        status: 2,
        autoRenewAllowed: true,
      },
      {
        title: 'USDT 90日高息',
        currency: 'USDT',
        apy: '12.00',
        duration: 90,
        minAmount: '1000',
        maxAmount: '200000',
        totalQuota: '10000000',
        soldQuota: '0',
        status: 2,
        autoRenewAllowed: true,
      },
      {
        title: 'USDT 180日 Plus',
        currency: 'USDT',
        apy: '15.00',
        duration: 180,
        minAmount: '5000',
        maxAmount: '500000',
        totalQuota: '20000000',
        soldQuota: '0',
        status: 2,
        autoRenewAllowed: true,
      },
      {
        title: 'USDT 360日旗舰',
        currency: 'USDT',
        apy: '18.00',
        duration: 360,
        minAmount: '10000',
        maxAmount: '1000000',
        totalQuota: '50000000',
        soldQuota: '0',
        status: 2,
        autoRenewAllowed: true,
      },
      {
        title: 'USDC 30日稳健',
        currency: 'USDC',
        apy: '7.50',
        duration: 30,
        minAmount: '500',
        maxAmount: '100000',
        totalQuota: '5000000',
        soldQuota: '0',
        status: 2,
        autoRenewAllowed: true,
      },
    ];

    for (const p of productData) {
      await db.insert(wealthProducts).values(p).onConflictDoNothing();
      console.log(`Inserted: ${p.title}`);
    }

    // Insert user accounts
    console.log('\n=== Inserting User Accounts ===');
    const accountData = [
      { userId, type: 'FUND', currency: 'USDT', balance: '1000000', frozenBalance: '0' },
      { userId, type: 'FUND', currency: 'USDC', balance: '500000', frozenBalance: '0' },
      { userId, type: 'FUND', currency: 'BTC', balance: '10', frozenBalance: '0' },
      { userId, type: 'FUND', currency: 'ETH', balance: '100', frozenBalance: '0' },
    ];

    for (const a of accountData) {
      await db.insert(accounts).values(a).onConflictDoNothing();
      console.log(`Inserted account: ${a.currency} - ${a.balance}`);
    }

    // Insert sample orders
    console.log('\n=== Inserting Sample Orders ===');
    const orderData = [
      {
        userId,
        productId: 1,
        amount: '5000',
        interestExpected: '52.88',
        interestPaid: '0',
        interestAccrued: '18.21',
        startDate: new Date('2026-01-10'),
        endDate: new Date('2026-01-17'),
        autoRenew: true,
        status: 1, // Accruing
      },
      {
        userId,
        productId: 2,
        amount: '10000',
        interestExpected: '657.53',
        interestPaid: '0',
        interestAccrued: '215.34',
        startDate: new Date('2026-01-05'),
        endDate: new Date('2026-02-04'),
        autoRenew: false,
        status: 1,
      },
      {
        userId,
        productId: 3,
        amount: '20000',
        interestExpected: '5917.81',
        interestPaid: '0',
        interestAccrued: '1315.07',
        startDate: new Date('2025-12-20'),
        endDate: new Date('2026-03-20'),
        autoRenew: true,
        status: 1,
      },
    ];

    for (const o of orderData) {
      await db.insert(wealthOrders).values(o).onConflictDoNothing();
      console.log(`Inserted order: ${o.amount} ${o.productId}`);
    }

    console.log('\n=== Seed completed successfully! ===');

  } catch (error) {
    console.error('Seed failed:', error);
  } finally {
    await client.end();
  }
}

seed();
