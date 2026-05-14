import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { BrowserRouter } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';

import i18n from '@/i18n/config';
import Deposit from './Deposit';

vi.mock('qrcode', () => ({
  default: {
    toDataURL: vi.fn(async (text: string) => `data:image/png;base64,QR(${text})`),
  },
}));

const MOCK_COINS = {
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
    {
      symbol: 'USDC',
      name: 'USD Coin',
      isStable: true,
      networks: [
        {
          chainCode: 'ETHEREUM',
          chainName: 'Ethereum',
          networkFamily: 'EVM',
          shortName: 'ETH',
          tokenStandard: 'ERC20',
          isNative: false,
          tokenContract: '0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48',
          decimals: 6,
          minDeposit: '1',
          requiredConfirmations: 12,
          estimatedArrivalMinutes: 2,
          explorerUrl: 'https://etherscan.io',
        },
        {
          chainCode: 'BSC',
          chainName: 'BNB Smart Chain',
          networkFamily: 'EVM',
          shortName: 'BSC',
          tokenStandard: 'BEP20',
          isNative: false,
          tokenContract: '0x8AC76a51cc950d9822D68b83fE1Ad97B32Cd580d',
          decimals: 18,
          minDeposit: '1',
          requiredConfirmations: 15,
          estimatedArrivalMinutes: 1,
          explorerUrl: 'https://bscscan.com',
        },
      ],
    },
    {
      symbol: 'TRX',
      name: 'TRON',
      isStable: false,
      networks: [
        {
          chainCode: 'TRON',
          chainName: 'TRON',
          networkFamily: 'TRON',
          shortName: 'TRON',
          tokenStandard: 'Native',
          isNative: true,
          tokenContract: null,
          decimals: 6,
          minDeposit: '0.1',
          requiredConfirmations: 0,
          estimatedArrivalMinutes: 1,
          explorerUrl: 'https://tronscan.org',
        },
      ],
    },
  ],
};

const MOCK_DEPOSITS_EMPTY = { deposits: [] };

const SINGLE_ETH_COIN = {
  coins: [{
    symbol: 'ETH', name: 'Ether', isStable: false,
    networks: [{
      chainCode: 'ETHEREUM', chainName: 'Ethereum', networkFamily: 'EVM',
      shortName: 'ETH', tokenStandard: 'Native', isNative: true,
      tokenContract: null, decimals: 18, minDeposit: '0.001',
      requiredConfirmations: 0, estimatedArrivalMinutes: 2, explorerUrl: '',
    }],
  }],
};

function mockFetch(url: string | URL | Request) {
  const u = typeof url === 'string' ? url : url.toString();
  const parsed = new URL(u, 'http://localhost');

  if (parsed.pathname === '/api/wallet/deposit-coins') {
    return Promise.resolve({
      ok: true,
      status: 200,
      json: () => Promise.resolve(MOCK_COINS),
    } as unknown as Response);
  }

  if (parsed.pathname === '/api/wallet/deposit-address') {
    const family = parsed.searchParams.get('networkFamily');
    if (family === 'EVM') {
      return Promise.resolve({
        ok: true,
        status: 200,
        json: () => Promise.resolve({
          networkFamily: 'EVM',
          address: '0xEVM00000000000000000000000000000000000001',
          supportedCoins: [],
        }),
      } as unknown as Response);
    }
    if (family === 'TRON') {
      return Promise.resolve({
        ok: true,
        status: 200,
        json: () => Promise.resolve({
          networkFamily: 'TRON',
          address: 'TTRON00000000000000000000000000000001',
          supportedCoins: [],
        }),
      } as unknown as Response);
    }
  }

  if (parsed.pathname === '/api/deposits') {
    return Promise.resolve({
      ok: true,
      status: 200,
      json: () => Promise.resolve(MOCK_DEPOSITS_EMPTY),
    } as unknown as Response);
  }

  return Promise.reject(new Error(`Unexpected request to ${u}`));
}

const renderDeposit = () => {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <I18nextProvider i18n={i18n}>
          <Deposit />
        </I18nextProvider>
      </BrowserRouter>
    </QueryClientProvider>
  );
};

const mockClipboard = { writeText: vi.fn().mockResolvedValue(undefined) };

describe('Deposit page — three-step flow', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.setItem('token', 'mock-token');
    Object.defineProperty(navigator, 'clipboard', {
      value: mockClipboard,
      configurable: true,
    });
    global.fetch = vi.fn(mockFetch) as unknown as typeof fetch;
  });

  it('renders step indicator with step 1 highlighted initially', async () => {
    renderDeposit();
    await waitFor(() => screen.getByTestId('coin-chip-ETH'));

    const step1 = screen.getByTestId('step-1');
    const step2 = screen.getByTestId('step-2');
    const step3 = screen.getByTestId('step-3');
    expect(step1.className).toContain('bg-primary');
    expect(step2.className).not.toContain('bg-primary');
    expect(step3.className).not.toContain('bg-primary');
  });

  it('lists deposit coins after load', async () => {
    renderDeposit();
    await waitFor(() => {
      expect(screen.getByTestId('coin-chip-ETH')).toBeInTheDocument();
      expect(screen.getByTestId('coin-chip-USDC')).toBeInTheDocument();
      expect(screen.getByTestId('coin-chip-TRX')).toBeInTheDocument();
    });
  });

  it('selecting a coin activates step 2 and shows its networks', async () => {
    renderDeposit();
    await waitFor(() => screen.getByTestId('coin-chip-USDC'));

    await userEvent.click(screen.getByTestId('coin-chip-USDC'));

    const step2 = screen.getByTestId('step-2');
    expect(step2.className).toContain('bg-primary');

    expect(screen.getByTestId('network-select')).toBeInTheDocument();
  });

  it('auto-selects the only network, shows badge and address', async () => {
    renderDeposit();
    await waitFor(() => screen.getByTestId('coin-chip-ETH'));

    await userEvent.click(screen.getByTestId('coin-chip-ETH'));

    await waitFor(() => {
      expect(screen.getByTestId('deposit-address')).toHaveTextContent(
        '0xEVM00000000000000000000000000000000000001'
      );
    });

    // Single-network coin shows a non-interactive badge with network info
    const badge = screen.getByTestId('network-badge');
    expect(badge).toHaveTextContent('ETH (Native)');

    const step3 = screen.getByTestId('step-3');
    expect(step3.className).toContain('bg-primary');
  });

  it('renders the QR code for the selected network address', async () => {
    renderDeposit();
    await waitFor(() => screen.getByTestId('coin-chip-ETH'));
    await userEvent.click(screen.getByTestId('coin-chip-ETH'));

    const qr = await screen.findByTestId('deposit-qr');
    expect(qr).toHaveAttribute(
      'src',
      expect.stringContaining('data:image/png;base64,QR(0xEVM00000000000000000000000000000000000001)'),
    );
  });

  it('non-native coin shows contract address suffix and explorer link', async () => {
    // Override with single-network USDC to auto-select (avoids Radix Select JSDOM issues)
    const singleNetworkCoins = {
      coins: [
        {
          symbol: 'USDC', name: 'USD Coin', isStable: true,
          networks: [{
            chainCode: 'ETHEREUM', chainName: 'Ethereum', networkFamily: 'EVM',
            shortName: 'ETH', tokenStandard: 'ERC20', isNative: false,
            tokenContract: '0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48',
            decimals: 6, minDeposit: '1', requiredConfirmations: 12,
            estimatedArrivalMinutes: 2, explorerUrl: 'https://etherscan.io',
          }],
        },
      ],
    };
    global.fetch = vi.fn((url: string | URL | Request) => {
      const u = typeof url === 'string' ? url : url.toString();
      const parsed = new URL(u, 'http://localhost');
      if (parsed.pathname === '/api/wallet/deposit-coins') {
        return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(singleNetworkCoins) } as unknown as Response);
      }
      if (parsed.pathname === '/api/wallet/deposit-address') {
        return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve({ networkFamily: 'EVM', address: '0xABC', supportedCoins: [] }) } as unknown as Response);
      }
      if (parsed.pathname === '/api/deposits') {
        return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(MOCK_DEPOSITS_EMPTY) } as unknown as Response);
      }
      return Promise.reject(new Error(`Unexpected: ${u}`));
    }) as unknown as typeof fetch;

    renderDeposit();
    await waitFor(() => screen.getByTestId('coin-chip-USDC'));
    await userEvent.click(screen.getByTestId('coin-chip-USDC'));

    await waitFor(() => screen.getByTestId('deposit-address'));

    expect(screen.getByText('eB48')).toBeInTheDocument();
    const link = screen.getByTestId('contract-link');
    expect(link).toHaveAttribute(
      'href',
      'https://etherscan.io/token/0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48',
    );
  });

  it('native coin hides contract address row', async () => {
    renderDeposit();
    await waitFor(() => screen.getByTestId('coin-chip-ETH'));
    await userEvent.click(screen.getByTestId('coin-chip-ETH'));

    await waitFor(() => screen.getByTestId('deposit-address'));
    expect(screen.queryByTestId('contract-link')).toBeNull();
  });

  it('switching coin resets the network selection', async () => {
    renderDeposit();
    await waitFor(() => screen.getByTestId('coin-chip-ETH'));

    // Select ETH (auto-selects single network → address shows)
    await userEvent.click(screen.getByTestId('coin-chip-ETH'));
    await waitFor(() => screen.getByTestId('deposit-address'));

    // Switch to USDC (has 2 networks → address should disappear)
    await userEvent.click(screen.getByTestId('coin-chip-USDC'));

    expect(screen.queryByTestId('deposit-address')).toBeNull();
    expect(screen.getByTestId('network-select')).toBeInTheDocument();
  });

  it('copies the address to clipboard', async () => {
    renderDeposit();
    await waitFor(() => screen.getByTestId('coin-chip-ETH'));
    await userEvent.click(screen.getByTestId('coin-chip-ETH'));

    await waitFor(() => screen.getByTestId('deposit-address'));
    const copyBtn = screen.getByRole('button', { name: /copy address/i });
    await userEvent.click(copyBtn);

    expect(mockClipboard.writeText).toHaveBeenCalledWith(
      '0xEVM00000000000000000000000000000000000001'
    );
  });

  it('renders empty state when there are no recent deposits', async () => {
    renderDeposit();
    await waitFor(() => screen.getByTestId('coin-chip-ETH'));

    expect(screen.getByText(/no recent deposits/i)).toBeInTheDocument();
  });

  it('shows skeletons while initial coin list is loading', () => {
    global.fetch = vi.fn(() => new Promise(() => {})) as unknown as typeof fetch;

    const { container } = renderDeposit();

    const skeletons = container.querySelectorAll('.animate-pulse');
    expect(skeletons.length).toBeGreaterThan(0);
    expect(screen.queryByTestId('coin-chip-ETH')).toBeNull();
  });

  it('shows error state with retry button when deposit-coins endpoint fails (F-2)', async () => {
    let coinsCallCount = 0;
    const coinsResponses: Response[] = [
      { ok: false, status: 503, json: () => Promise.resolve({ error: 'REGISTRY_UNAVAILABLE' }) } as unknown as Response,
      { ok: true, status: 200, json: () => Promise.resolve(MOCK_COINS) } as unknown as Response,
    ];
    global.fetch = vi.fn((url: string | URL | Request) => {
      const u = typeof url === 'string' ? url : url.toString();
      const parsed = new URL(u, 'http://localhost');
      if (parsed.pathname === '/api/wallet/deposit-coins') {
        const r = coinsResponses[coinsCallCount] ?? coinsResponses[coinsResponses.length - 1];
        coinsCallCount += 1;
        return Promise.resolve(r);
      }
      if (parsed.pathname === '/api/deposits') {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: () => Promise.resolve(MOCK_DEPOSITS_EMPTY),
        } as unknown as Response);
      }
      return Promise.reject(new Error(`Unexpected: ${u}`));
    }) as unknown as typeof fetch;

    renderDeposit();

    await waitFor(() => screen.getByText(/unable to load/i));
    // F-2: retry button should be present alongside the error so users can recover
    // without navigating away.
    await userEvent.click(screen.getByTestId('retry-deposit-coins'));
    await waitFor(() => screen.getByTestId('coin-chip-ETH'));
    expect(coinsCallCount).toBeGreaterThanOrEqual(2);
  });

  it('shows address error state when deposit-address fails', async () => {
    global.fetch = vi.fn((url: string | URL | Request) => {
      const u = typeof url === 'string' ? url : url.toString();
      const parsed = new URL(u, 'http://localhost');
      if (parsed.pathname === '/api/wallet/deposit-coins') {
        return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(SINGLE_ETH_COIN) } as unknown as Response);
      }
      if (parsed.pathname === '/api/wallet/deposit-address') {
        return Promise.resolve({ ok: false, status: 500, json: () => Promise.resolve({ error: 'ASSIGN_FAILED' }) } as unknown as Response);
      }
      if (parsed.pathname === '/api/deposits') {
        return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(MOCK_DEPOSITS_EMPTY) } as unknown as Response);
      }
      return Promise.reject(new Error(`Unexpected: ${u}`));
    }) as unknown as typeof fetch;

    renderDeposit();
    await waitFor(() => screen.getByTestId('coin-chip-ETH'));
    await userEvent.click(screen.getByTestId('coin-chip-ETH'));

    await waitFor(() => {
      expect(screen.getByText(/unable to load deposit address/i)).toBeInTheDocument();
    });
  });

  it('renders recent deposits in a table with all columns', async () => {
    const depositsWithData = {
      deposits: [
        {
          id: 1, amount: '0.5', asset: 'ETH', status: 'CREDITED',
          txHash: '0xabc123def456', chain: 'ETHEREUM',
          toAddress: '0x1234567890abcdef1234567890abcdef12345678',
          createdAt: '2026-05-13T10:00:00Z',
        },
        {
          id: 2, amount: '100', asset: 'USDC', status: 'PENDING',
          createdAt: '2026-05-13T09:00:00Z',
        },
      ],
    };
    global.fetch = vi.fn((url: string | URL | Request) => {
      const u = typeof url === 'string' ? url : url.toString();
      const parsed = new URL(u, 'http://localhost');
      if (parsed.pathname === '/api/wallet/deposit-coins') {
        return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(MOCK_COINS) } as unknown as Response);
      }
      if (parsed.pathname === '/api/deposits') {
        return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(depositsWithData) } as unknown as Response);
      }
      return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve({}) } as unknown as Response);
    }) as unknown as typeof fetch;

    renderDeposit();
    await waitFor(() => {
      expect(screen.getByTestId('deposits-table')).toBeInTheDocument();
    });

    const table = screen.getByTestId('deposits-table');
    const rows = table.querySelectorAll('tbody tr');
    expect(rows).toHaveLength(2);
    expect(rows[0].textContent).toContain('0.5');
    expect(rows[0].textContent).toContain('ETH');
    expect(rows[1].textContent).toContain('100');
    expect(rows[1].textContent).toContain('USDC');
  });

  it('suppresses contract link for disallowed explorer origin', async () => {
    const evilCoins = {
      coins: [{
        symbol: 'USDC', name: 'USD Coin', isStable: true,
        networks: [{
          chainCode: 'EVIL', chainName: 'Evil Chain', networkFamily: 'EVM',
          shortName: 'EVIL', tokenStandard: 'ERC20', isNative: false,
          tokenContract: '0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48',
          decimals: 6, minDeposit: '1', requiredConfirmations: 12,
          estimatedArrivalMinutes: 2, explorerUrl: 'https://evil.com',
        }],
      }],
    };
    global.fetch = vi.fn((url: string | URL | Request) => {
      const u = typeof url === 'string' ? url : url.toString();
      const parsed = new URL(u, 'http://localhost');
      if (parsed.pathname === '/api/wallet/deposit-coins') {
        return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(evilCoins) } as unknown as Response);
      }
      if (parsed.pathname === '/api/wallet/deposit-address') {
        return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve({ networkFamily: 'EVM', address: '0xABC', supportedCoins: [] }) } as unknown as Response);
      }
      if (parsed.pathname === '/api/deposits') {
        return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(MOCK_DEPOSITS_EMPTY) } as unknown as Response);
      }
      return Promise.reject(new Error(`Unexpected: ${u}`));
    }) as unknown as typeof fetch;

    renderDeposit();
    await waitFor(() => screen.getByTestId('coin-chip-USDC'));
    await userEvent.click(screen.getByTestId('coin-chip-USDC'));
    await waitFor(() => screen.getByTestId('deposit-address'));

    // Contract suffix should still render, but the link must NOT appear
    expect(screen.getByText('eB48')).toBeInTheDocument();
    expect(screen.queryByTestId('contract-link')).toBeNull();
  });

  it('suppresses contract link for malformed explorer URL', async () => {
    const badUrlCoins = {
      coins: [{
        symbol: 'USDC', name: 'USD Coin', isStable: true,
        networks: [{
          chainCode: 'BAD', chainName: 'Bad Chain', networkFamily: 'EVM',
          shortName: 'BAD', tokenStandard: 'ERC20', isNative: false,
          tokenContract: '0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48',
          decimals: 6, minDeposit: '1', requiredConfirmations: 12,
          estimatedArrivalMinutes: 2, explorerUrl: 'not-a-url',
        }],
      }],
    };
    global.fetch = vi.fn((url: string | URL | Request) => {
      const u = typeof url === 'string' ? url : url.toString();
      const parsed = new URL(u, 'http://localhost');
      if (parsed.pathname === '/api/wallet/deposit-coins') {
        return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(badUrlCoins) } as unknown as Response);
      }
      if (parsed.pathname === '/api/wallet/deposit-address') {
        return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve({ networkFamily: 'EVM', address: '0xABC', supportedCoins: [] }) } as unknown as Response);
      }
      if (parsed.pathname === '/api/deposits') {
        return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(MOCK_DEPOSITS_EMPTY) } as unknown as Response);
      }
      return Promise.reject(new Error(`Unexpected: ${u}`));
    }) as unknown as typeof fetch;

    renderDeposit();
    await waitFor(() => screen.getByTestId('coin-chip-USDC'));
    await userEvent.click(screen.getByTestId('coin-chip-USDC'));
    await waitFor(() => screen.getByTestId('deposit-address'));

    expect(screen.getByText('eB48')).toBeInTheDocument();
    expect(screen.queryByTestId('contract-link')).toBeNull();
  });

  it('surfaces error UI with retry when /api/deposits fails (F-1)', async () => {
    let depositsCallCount = 0;
    const depositsResponses: Response[] = [
      { ok: false, status: 500, json: () => Promise.resolve({ error: 'INTERNAL' }) } as unknown as Response,
      { ok: true, status: 200, json: () => Promise.resolve({ deposits: [{ id: 1, amount: '1', asset: 'ETH', status: 'CREDITED' }] }) } as unknown as Response,
    ];
    global.fetch = vi.fn((url: string | URL | Request) => {
      const u = typeof url === 'string' ? url : url.toString();
      const parsed = new URL(u, 'http://localhost');
      if (parsed.pathname === '/api/wallet/deposit-coins') {
        return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(MOCK_COINS) } as unknown as Response);
      }
      if (parsed.pathname === '/api/deposits') {
        const r = depositsResponses[depositsCallCount] ?? depositsResponses[depositsResponses.length - 1];
        depositsCallCount += 1;
        return Promise.resolve(r);
      }
      return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve({}) } as unknown as Response);
    }) as unknown as typeof fetch;

    renderDeposit();
    await waitFor(() => screen.getByTestId('coin-chip-ETH'));

    // F-1: must surface the failure (not silently render empty state) so users
    // don't think their deposit was lost.
    await waitFor(() => screen.getByTestId('retry-recent-deposits'));
    expect(screen.queryByText(/no recent deposits/i)).toBeNull();

    // Retry should re-invoke the endpoint and reveal the table.
    await userEvent.click(screen.getByTestId('retry-recent-deposits'));
    await waitFor(() => screen.getByTestId('deposits-table'));
    expect(depositsCallCount).toBeGreaterThanOrEqual(2);
  });

  it('hides QR code gracefully when QRCode.toDataURL fails', async () => {
    const qrcode = await import('qrcode');
    vi.mocked(qrcode.default.toDataURL).mockRejectedValueOnce(new Error('canvas error'));

    renderDeposit();
    await waitFor(() => screen.getByTestId('coin-chip-ETH'));
    await userEvent.click(screen.getByTestId('coin-chip-ETH'));

    await waitFor(() => screen.getByTestId('deposit-address'));
    // QR should not render when generation fails, but page shouldn't crash
    expect(screen.queryByTestId('deposit-qr')).toBeNull();
  });

  it('retry button refetches deposit address', async () => {
    let addressCallCount = 0;
    global.fetch = vi.fn((url: string | URL | Request) => {
      const u = typeof url === 'string' ? url : url.toString();
      const parsed = new URL(u, 'http://localhost');
      if (parsed.pathname === '/api/wallet/deposit-coins') {
        return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(SINGLE_ETH_COIN) } as unknown as Response);
      }
      if (parsed.pathname === '/api/wallet/deposit-address') {
        addressCallCount++;
        if (addressCallCount === 1) {
          return Promise.resolve({ ok: false, status: 500, json: () => Promise.resolve({ error: 'ASSIGN_FAILED' }) } as unknown as Response);
        }
        return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve({ networkFamily: 'EVM', address: '0xRetried', supportedCoins: [] }) } as unknown as Response);
      }
      if (parsed.pathname === '/api/deposits') {
        return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(MOCK_DEPOSITS_EMPTY) } as unknown as Response);
      }
      return Promise.reject(new Error(`Unexpected: ${u}`));
    }) as unknown as typeof fetch;

    renderDeposit();
    await waitFor(() => screen.getByTestId('coin-chip-ETH'));
    await userEvent.click(screen.getByTestId('coin-chip-ETH'));

    // First call fails — shows error with retry button
    await waitFor(() => screen.getByTestId('retry-address'));
    await userEvent.click(screen.getByTestId('retry-address'));

    // Second call succeeds — shows address
    await waitFor(() => {
      expect(screen.getByTestId('deposit-address')).toHaveTextContent('0xRetried');
    });
  });

  it('recent deposits renders explorer link when chain matches explorerUrlMap', async () => {
    const depositsWithChain = {
      deposits: [
        { id: 1, amount: '0.5', asset: 'ETH', status: 'CREDITED', txHash: '0xabc123deadbeef', chain: 'ETHEREUM', createdAt: '2026-05-13T10:00:00Z' },
      ],
    };
    global.fetch = vi.fn((url: string | URL | Request) => {
      const u = typeof url === 'string' ? url : url.toString();
      const parsed = new URL(u, 'http://localhost');
      if (parsed.pathname === '/api/wallet/deposit-coins') {
        return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(MOCK_COINS) } as unknown as Response);
      }
      if (parsed.pathname === '/api/deposits') {
        return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(depositsWithChain) } as unknown as Response);
      }
      return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve({}) } as unknown as Response);
    }) as unknown as typeof fetch;

    renderDeposit();
    await waitFor(() => {
      expect(screen.getByText('0.5')).toBeInTheDocument();
    });

    // ETHEREUM chain maps to https://etherscan.io from MOCK_COINS
    // txHash '0xabc123deadbeef' truncated to '0xabc123...beef'
    const links = screen.getAllByRole('link');
    const explorerLink = links.find(l => l.getAttribute('href')?.includes('etherscan.io/tx/0xabc123deadbeef'));
    expect(explorerLink).toBeDefined();
    expect(explorerLink).toHaveAttribute('target', '_blank');
    expect(explorerLink).toHaveAttribute('rel', 'noopener noreferrer');
  });

  it('shows empty coin list message when no coins available', async () => {
    global.fetch = vi.fn((url: string | URL | Request) => {
      const u = typeof url === 'string' ? url : url.toString();
      const parsed = new URL(u, 'http://localhost');
      if (parsed.pathname === '/api/wallet/deposit-coins') {
        return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve({ coins: [] }) } as unknown as Response);
      }
      if (parsed.pathname === '/api/deposits') {
        return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(MOCK_DEPOSITS_EMPTY) } as unknown as Response);
      }
      return Promise.reject(new Error(`Unexpected: ${u}`));
    }) as unknown as typeof fetch;

    renderDeposit();
    await waitFor(() => {
      expect(screen.getByText(/no coins available/i)).toBeInTheDocument();
    });
  });

  it('handles clipboard write failure gracefully', async () => {
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: vi.fn().mockRejectedValue(new Error('denied')) },
      configurable: true,
    });

    renderDeposit();
    await waitFor(() => screen.getByTestId('coin-chip-ETH'));
    await userEvent.click(screen.getByTestId('coin-chip-ETH'));

    await waitFor(() => screen.getByTestId('deposit-address'));
    const copyBtn = screen.getByRole('button', { name: /copy address/i });
    await userEvent.click(copyBtn);

    // Should not throw — error is caught and displayed as toast
    expect(navigator.clipboard.writeText).toHaveBeenCalled();
  });
});
