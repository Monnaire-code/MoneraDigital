import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { BrowserRouter } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';

import i18n from '@/i18n/config';
import Deposit from './Deposit';

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

    global.fetch = vi.fn((url: string | URL | Request) => {
      const u = typeof url === 'string' ? url : url.toString();
      if (u.startsWith('/api/wallet/deposit-address?networkFamily=EVM')) {
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
      if (u.startsWith('/api/wallet/deposit-address?networkFamily=TRON')) {
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
