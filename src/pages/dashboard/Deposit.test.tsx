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

describe('Deposit page', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.setItem('token', 'mock-token');
    Object.defineProperty(navigator, 'clipboard', {
      value: mockClipboard,
      configurable: true,
    });

    // R2-S-3: parse the URL + query string instead of brittle prefix matching.
    // This way the mock survives query-order changes or extra params (e.g.
    // `?_=cacheBust`) without silently falling through to the reject branch.
    global.fetch = vi.fn((url: string | URL | Request) => {
      const u = typeof url === 'string' ? url : url.toString();
      const parsed = new URL(u, 'http://localhost');
      if (parsed.pathname === '/api/wallet/deposit-address') {
        const family = parsed.searchParams.get('networkFamily');
        if (family === 'EVM') {
          return Promise.resolve({
            ok: true,
            status: 200,
            json: () => Promise.resolve({
              networkFamily: 'EVM',
              address: '0xEVM00000000000000000000000000000000000001',
              supportedCoins: [
                { chainCode: 'ETHEREUM', symbol: 'USDC', coinKey: 'k1', minDeposit: '0.0001', decimals: 6 },
              ],
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
              supportedCoins: [
                { chainCode: 'TRON', symbol: 'USDT', coinKey: 'k2', minDeposit: '0.000001', decimals: 6 },
              ],
            }),
          } as unknown as Response);
        }
      }
      return Promise.reject(new Error(`Unexpected request to ${u}`));
    }) as unknown as typeof fetch;
  });

  it('renders the EVM tab address by default', async () => {
    renderDeposit();
    await waitFor(() => {
      expect(screen.getByTestId('deposit-address')).toHaveTextContent(
        '0xEVM00000000000000000000000000000000000001'
      );
    });
    expect(screen.getByText('USDC')).toBeInTheDocument();
    expect(screen.getByText('ETHEREUM')).toBeInTheDocument();
  });

  it('switches to TRON tab and loads the TRON address', async () => {
    renderDeposit();
    await waitFor(() => screen.getByTestId('deposit-address'));

    const tronTab = screen.getByRole('tab', { name: /TRON/i });
    await userEvent.click(tronTab);

    await waitFor(() => {
      expect(screen.getByTestId('deposit-address')).toHaveTextContent(
        'TTRON00000000000000000000000000000001'
      );
    });
    expect(screen.getByText('USDT')).toBeInTheDocument();
  });

  it('copies the address when the copy button is clicked', async () => {
    renderDeposit();
    await waitFor(() => screen.getByTestId('deposit-address'));

    const copyBtn = screen.getByRole('button', { name: /copy address/i });
    await userEvent.click(copyBtn);

    expect(mockClipboard.writeText).toHaveBeenCalledWith(
      '0xEVM00000000000000000000000000000000000001'
    );
  });

  // Regression: T8-I-2 — plan §3.7 locks QR code alongside the copy button.
  it('renders a QR code for the deposit address', async () => {
    renderDeposit();
    await waitFor(() => screen.getByTestId('deposit-address'));

    const qr = await screen.findByTestId('deposit-qr');
    expect(qr).toHaveAttribute(
      'src',
      expect.stringContaining('data:image/png;base64,QR(0xEVM00000000000000000000000000000000000001)'),
    );
  });

  it('updates the QR code when switching tabs', async () => {
    renderDeposit();
    await waitFor(() => screen.getByTestId('deposit-address'));

    const tronTab = screen.getByRole('tab', { name: /TRON/i });
    await userEvent.click(tronTab);

    await waitFor(() => {
      const qr = screen.getByTestId('deposit-qr');
      expect(qr).toHaveAttribute(
        'src',
        expect.stringContaining('data:image/png;base64,QR(TTRON00000000000000000000000000000001)'),
      );
    });
  });

  // Regression: T8-S-2 — confirms the Skeleton fallback renders before data
  // arrives, so users never stare at an empty card during the initial fetch.
  it('shows skeletons while the deposit address is loading', () => {
    // Use a never-resolving fetch so the query stays in its loading state.
    global.fetch = vi.fn(() => new Promise(() => {})) as unknown as typeof fetch;

    const { container } = renderDeposit();

    // Skeleton from shadcn renders with class "animate-pulse" on its inner div.
    const skeletons = container.querySelectorAll('.animate-pulse');
    expect(skeletons.length).toBeGreaterThan(0);
    expect(screen.queryByTestId('deposit-address')).toBeNull();
  });

  it('shows an error card when the backend returns an error', async () => {
    global.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 503,
      json: () => Promise.resolve({ error: 'POOL_UNAVAILABLE' }),
    } as unknown as Response) as unknown as typeof fetch;

    renderDeposit();

    await waitFor(() => {
      expect(screen.getByText(/POOL_UNAVAILABLE/i)).toBeInTheDocument();
    });
  });
});
