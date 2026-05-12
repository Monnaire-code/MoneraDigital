import { z } from 'zod';
import logger from './logger.js';

export type NetworkFamily = 'EVM' | 'TRON';

export interface DepositAddressResponse {
  networkFamily: NetworkFamily;
  address: string;
  supportedCoins: SupportedCoin[];
}

export interface SupportedCoin {
  chainCode: string;
  symbol: string;
  coinKey: string;
  minDeposit: string;
  decimals: number;
}

const networkFamilySchema = z.enum(['EVM', 'TRON']);

async function authHeaders(): Promise<HeadersInit> {
  const token = localStorage.getItem('token');
  if (!token) {
    throw new Error('Not authenticated');
  }
  return { Authorization: `Bearer ${token}` };
}

async function parseOrThrow<T>(response: Response, action: string): Promise<T> {
  if (!response.ok) {
    let message = `Failed to ${action}`;
    try {
      const body = await response.json();
      if (body?.error) message = body.error;
      else if (body?.message) message = body.message;
    } catch {
      // Non-JSON error body — fall through to default message.
    }
    throw new Error(message);
  }
  return (await response.json()) as T;
}

export class WalletService {
  /**
   * Get the deposit address for a network family (EVM or TRON).
   *
   * Backend returns the user's pool-assigned address; the first call lazily
   * assigns one. supportedCoins lists which on-chain coins map to the address.
   */
  static async getDepositAddress(networkFamily: NetworkFamily): Promise<DepositAddressResponse> {
    const family = networkFamilySchema.parse(networkFamily);
    logger.info({ networkFamily: family }, 'Fetching deposit address');

    const response = await fetch(
      `/api/wallet/deposit-address?networkFamily=${encodeURIComponent(family)}`,
      { headers: await authHeaders() }
    );
    return parseOrThrow<DepositAddressResponse>(response, 'fetch deposit address');
  }
}
