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

export interface DepositCoinNetwork {
  chainCode: string;
  chainName: string;
  networkFamily: NetworkFamily;
  shortName: string;
  tokenStandard: string;
  isNative: boolean;
  tokenContract: string | null;
  decimals: number;
  minDeposit: string;
  requiredConfirmations: number;
  estimatedArrivalMinutes: number;
  explorerUrl: string;
}

export interface DepositCoin {
  symbol: string;
  name: string;
  isStable: boolean;
  networks: DepositCoinNetwork[];
}

export interface DepositCoinsResponse {
  coins: DepositCoin[];
}

const networkFamilySchema = z.enum(['EVM', 'TRON']);

// F-3: response schema for the deposit address endpoint. Backend regressions
// that drop or null `address` would otherwise render an empty address box —
// users could mis-send funds to a void destination. Hard-fail at the boundary
// so the UI shows an error state instead of a silent empty input.
const supportedCoinSchema = z.object({
  chainCode: z.string(),
  symbol: z.string(),
  coinKey: z.string(),
  minDeposit: z.string(),
  decimals: z.number(),
});

const depositAddressResponseSchema = z.object({
  networkFamily: networkFamilySchema,
  address: z.string().min(1, 'address must not be empty'),
  supportedCoins: z.array(supportedCoinSchema),
});

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

export interface DepositRecord {
  id: number;
  amount: string;
  asset: string;
  status: string;
  txHash?: string;
  chain?: string;
  fromAddress?: string;
  toAddress?: string;
  createdAt?: string;
  creditedAt?: string;
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
    const raw = await parseOrThrow<unknown>(response, 'fetch deposit address');
    return depositAddressResponseSchema.parse(raw);
  }

  static async getRecentDeposits(limit = 5): Promise<DepositRecord[]> {
    const response = await fetch(`/api/deposits?limit=${limit}`, {
      headers: await authHeaders(),
    });
    const data = await parseOrThrow<{ deposits: DepositRecord[] }>(response, 'fetch deposits');
    return data.deposits;
  }

  static async getDepositCoins(): Promise<DepositCoinsResponse> {
    logger.info('Fetching deposit coins');
    const response = await fetch('/api/wallet/deposit-coins', {
      headers: await authHeaders(),
    });
    return parseOrThrow<DepositCoinsResponse>(response, 'fetch deposit coins');
  }
}
