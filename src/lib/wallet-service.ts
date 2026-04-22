import { z } from 'zod';
import logger from './logger.js';

/**
 * Supported currencies in token_network format
 * Note: USDC_BEP20 uses full backend format for BNB Smart Chain (特例)
 */
export const SUPPORTED_CURRENCIES = [
  'USDT_ERC20',
  'USDT_TRC20',
  'USDT_BEP20',
  'USDC_ERC20',
  'USDC_TRC20',
  'USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET', // 特例：长格式
] as const;

export type SupportedCurrency = typeof SUPPORTED_CURRENCIES[number];

/**
 * Validate if a currency string is valid
 */
export function isValidCurrency(currency: string): currency is SupportedCurrency {
  return SUPPORTED_CURRENCIES.includes(currency as SupportedCurrency);
}

const createWalletSchema = z.object({
  userId: z.number().int().positive(),
  productCode: z.string().min(1),
  currency: z.string().min(1).refine(isValidCurrency, {
    message: `Currency must be one of: ${SUPPORTED_CURRENCIES.join(', ')}`,
  }),
});

export class WalletService {
  /**
   * Create wallet via API
   */
  static async createWallet(userId: number, productCode: string, currency: string) {
    // Debug logging for account opening flow
    console.log(`[DEBUG-ACCOUNT-OPENING] WalletService.createWallet called`, {
      timestamp: new Date().toISOString(),
      userId,
      productCode,
      currency,
    });

    const validated = createWalletSchema.parse({
      userId,
      productCode,
      currency,
    });

    logger.info({ userId, productCode, currency }, 'Creating wallet');

    const token = localStorage.getItem('token');
    if (!token) {
      const error = new Error('Not authenticated');
      console.log(`[DEBUG-ACCOUNT-OPENING] WalletService.createWallet: No token found`);
      throw error;
    }

    const response = await fetch('/api/wallet/create', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${token}`,
      },
      body: JSON.stringify(validated),
    });

    if (!response.ok) {
      const error = await response.json();
      console.log(`[DEBUG-ACCOUNT-OPENING] WalletService.createWallet: API error`, {
        status: response.status,
        error: error,
      });
      throw new Error(error.error || 'Failed to create wallet');
    }

    const data = await response.json();
    console.log(`[DEBUG-ACCOUNT-OPENING] WalletService.createWallet completed`, {
      timestamp: new Date().toISOString(),
      walletId: data.wallet?.id,
      status: data.status,
    });

    logger.info({ walletId: data.wallet?.id }, 'Wallet created successfully');

    return data;
  }

  /**
   * Get wallet info via API
   * Note: Backend returns wallet info based on JWT token, walletId parameter is ignored
   */
  static async getWalletInfo(walletId?: string) {
    // Debug logging for account opening flow
    console.log(`[DEBUG-ACCOUNT-OPENING] WalletService.getWalletInfo called`, {
      timestamp: new Date().toISOString(),
      walletId,
    });

    const token = localStorage.getItem('token');
    if (!token) {
      const error = new Error('Not authenticated');
      console.log(`[DEBUG-ACCOUNT-OPENING] WalletService.getWalletInfo: No token found`);
      throw error;
    }

    const response = await fetch('/api/wallet/info', {
      headers: {
        'Authorization': `Bearer ${token}`,
      },
    });

    if (!response.ok) {
      const error = await response.json();
      console.log(`[DEBUG-ACCOUNT-OPENING] WalletService.getWalletInfo: API error`, {
        status: response.status,
        error: error,
      });
      throw new Error('Failed to fetch wallet info');
    }

    const data = await response.json();
    console.log(`[DEBUG-ACCOUNT-OPENING] WalletService.getWalletInfo completed`, {
      timestamp: new Date().toISOString(),
      walletId,
      status: data.status,
    });

    return data;
  }
}
