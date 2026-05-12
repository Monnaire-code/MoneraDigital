import { describe, it, expect, vi, beforeEach } from 'vitest';

const localStorageMock = {
  getItem: vi.fn(),
  setItem: vi.fn(),
  clear: vi.fn(),
  removeItem: vi.fn(),
};
Object.defineProperty(global, 'localStorage', {
  value: localStorageMock,
  writable: true,
});

function jsonResponse(body: unknown, init: { ok?: boolean; status?: number } = {}): Response {
  const ok = init.ok ?? true;
  const status = init.status ?? (ok ? 200 : 500);
  return {
    ok,
    status,
    statusText: ok ? 'OK' : 'ERR',
    headers: new Headers(),
    redirected: false,
    json: () => Promise.resolve(body),
  } as unknown as Response;
}

describe('WalletService', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorageMock.getItem.mockReturnValue('mock-token');
  });

  describe('getDepositAddress', () => {
    it('returns address payload for EVM', async () => {
      const fetchMock = vi.fn().mockResolvedValue(
        jsonResponse({
          networkFamily: 'EVM',
          address: '0xabc',
          supportedCoins: [
            { chainCode: 'ETHEREUM', symbol: 'USDC', coinKey: 'k', minDeposit: '0.0001', decimals: 6 },
          ],
        })
      );
      global.fetch = fetchMock as unknown as typeof fetch;
      const { WalletService } = await import('./wallet-service');

      const result = await WalletService.getDepositAddress('EVM');

      expect(result.address).toBe('0xabc');
      expect(result.supportedCoins).toHaveLength(1);
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/wallet/deposit-address?networkFamily=EVM',
        expect.objectContaining({
          headers: expect.objectContaining({ Authorization: 'Bearer mock-token' }),
        })
      );
    });

    it('rejects invalid network family', async () => {
      const { WalletService } = await import('./wallet-service');
      // @ts-expect-error testing runtime validation
      await expect(WalletService.getDepositAddress('SOLANA')).rejects.toThrow();
    });

    it('throws backend error message on non-ok response', async () => {
      global.fetch = vi.fn().mockResolvedValue(
        jsonResponse({ error: 'ASSIGN_FAILED' }, { ok: false, status: 500 })
      ) as unknown as typeof fetch;
      const { WalletService } = await import('./wallet-service');

      await expect(WalletService.getDepositAddress('TRON')).rejects.toThrow('ASSIGN_FAILED');
    });

    it('falls back to default message when body is not JSON', async () => {
      global.fetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 503,
        json: () => Promise.reject(new Error('not json')),
      } as unknown as Response) as unknown as typeof fetch;
      const { WalletService } = await import('./wallet-service');

      await expect(WalletService.getDepositAddress('EVM')).rejects.toThrow('Failed to fetch deposit address');
    });

    it('throws when no token in localStorage', async () => {
      localStorageMock.getItem.mockReturnValue(null);
      const { WalletService } = await import('./wallet-service');
      await expect(WalletService.getDepositAddress('EVM')).rejects.toThrow('Not authenticated');
    });
  });

  describe('getRecentDeposits', () => {
    it('returns deposits array on success', async () => {
      const mockDeposits = {
        deposits: [
          { id: 1, amount: '0.5', asset: 'ETH', status: 'CONFIRMED' },
          { id: 2, amount: '100', asset: 'USDC', status: 'PENDING' },
        ],
      };
      global.fetch = vi.fn().mockResolvedValue(jsonResponse(mockDeposits)) as unknown as typeof fetch;
      const { WalletService } = await import('./wallet-service');

      const result = await WalletService.getRecentDeposits();

      expect(result).toHaveLength(2);
      expect(result[0].asset).toBe('ETH');
      expect(result[1].status).toBe('PENDING');
    });

    it('passes limit as query parameter', async () => {
      const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ deposits: [] }));
      global.fetch = fetchMock as unknown as typeof fetch;
      const { WalletService } = await import('./wallet-service');

      await WalletService.getRecentDeposits(3);

      expect(fetchMock).toHaveBeenCalledWith(
        '/api/deposits?limit=3',
        expect.objectContaining({
          headers: expect.objectContaining({ Authorization: 'Bearer mock-token' }),
        })
      );
    });

    it('throws on non-ok response', async () => {
      global.fetch = vi.fn().mockResolvedValue(
        jsonResponse({ error: 'INTERNAL' }, { ok: false, status: 500 })
      ) as unknown as typeof fetch;
      const { WalletService } = await import('./wallet-service');

      await expect(WalletService.getRecentDeposits()).rejects.toThrow('INTERNAL');
    });

    it('throws when no token in localStorage', async () => {
      localStorageMock.getItem.mockReturnValue(null);
      const { WalletService } = await import('./wallet-service');
      await expect(WalletService.getRecentDeposits()).rejects.toThrow('Not authenticated');
    });
  });

  describe('getDepositCoins', () => {
    it('returns coins list on success', async () => {
      const mockCoins = {
        coins: [
          {
            symbol: 'ETH',
            name: 'Ether',
            isStable: false,
            networks: [
              {
                chainCode: 'ETHEREUM',
                chainName: 'Ethereum',
                networkFamily: 'EVM',
                shortName: 'ETH',
                tokenStandard: 'Native',
                isNative: true,
                tokenContract: null,
                decimals: 18,
                minDeposit: '0.001',
                requiredConfirmations: 12,
                estimatedArrivalMinutes: 2,
                explorerUrl: 'https://etherscan.io',
              },
            ],
          },
        ],
      };
      global.fetch = vi.fn().mockResolvedValue(jsonResponse(mockCoins)) as unknown as typeof fetch;
      const { WalletService } = await import('./wallet-service');

      const result = await WalletService.getDepositCoins();

      expect(result.coins).toHaveLength(1);
      expect(result.coins[0].symbol).toBe('ETH');
      expect(result.coins[0].networks[0].chainCode).toBe('ETHEREUM');
    });

    it('throws on non-ok response', async () => {
      global.fetch = vi.fn().mockResolvedValue(
        jsonResponse({ error: 'REGISTRY_UNAVAILABLE' }, { ok: false, status: 503 })
      ) as unknown as typeof fetch;
      const { WalletService } = await import('./wallet-service');

      await expect(WalletService.getDepositCoins()).rejects.toThrow('REGISTRY_UNAVAILABLE');
    });

    it('throws when no token in localStorage', async () => {
      localStorageMock.getItem.mockReturnValue(null);
      const { WalletService } = await import('./wallet-service');
      await expect(WalletService.getDepositCoins()).rejects.toThrow('Not authenticated');
    });
  });
});
