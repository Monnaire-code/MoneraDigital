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

  describe('getSupportedChains', () => {
    it('returns chain list', async () => {
      const fetchMock = vi.fn().mockResolvedValue(
        jsonResponse({
          chains: [
            { chainCode: 'ETHEREUM', symbol: 'USDC', coinKey: 'k', minDeposit: '0.0001', decimals: 6 },
            { chainCode: 'TRON', symbol: 'USDT', coinKey: 'k2', minDeposit: '0.000001', decimals: 6 },
          ],
        })
      );
      global.fetch = fetchMock as unknown as typeof fetch;
      const { WalletService } = await import('./wallet-service');

      const result = await WalletService.getSupportedChains();

      expect(result.chains).toHaveLength(2);
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/wallet/supported-chains',
        expect.objectContaining({
          headers: expect.objectContaining({ Authorization: 'Bearer mock-token' }),
        })
      );
    });

    it('propagates backend error', async () => {
      global.fetch = vi.fn().mockResolvedValue(
        jsonResponse({ message: 'REGISTRY_UNAVAILABLE' }, { ok: false, status: 503 })
      ) as unknown as typeof fetch;
      const { WalletService } = await import('./wallet-service');

      await expect(WalletService.getSupportedChains()).rejects.toThrow('REGISTRY_UNAVAILABLE');
    });
  });
});
