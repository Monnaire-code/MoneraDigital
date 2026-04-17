import { z } from 'zod';
import logger from './logger.js';

/**
 * Address Whitelist Service
 * 
 * KISS: Simple API client for address whitelist operations
 * All database operations are handled by Go backend
 */

const createAddressSchema = z.object({
  walletAddress: z.string().min(1, 'Address is required'),
  chainType: z.string().min(1, 'Chain type is required'),
  addressAlias: z.string().min(1, 'Label is required'),
});

const verifyAddressSchema = z.object({
  addressId: z.number().int().positive(),
  token: z.string().length(6, 'Verification code must be 6 digits'),
});

export class AddressWhitelistService {
  /**
   * Create address via API
   */
  static async createAddress(walletAddress: string, chainType: string, addressAlias: string) {
    const validated = createAddressSchema.parse({
      walletAddress,
      chainType,
      addressAlias,
    });

    logger.info({ chainType, addressAlias }, 'Creating address');

    const token = localStorage.getItem('token');
    if (!token) {
      throw new Error('Not authenticated');
    }

    const response = await fetch('/api/addresses', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${token}`,
      },
      body: JSON.stringify({
        wallet_address: validated.walletAddress,
        chain_type: validated.chainType,
        address_alias: validated.addressAlias,
      }),
    });

    if (!response.ok) {
      const error = await response.json();
      throw new Error(error.error || 'Failed to create address');
    }

    const data = await response.json();
    logger.info({ addressId: data.address?.id }, 'Address created successfully');

    return data;
  }

  /**
   * Get user addresses via API
   */
  static async getUserAddresses() {
    const token = localStorage.getItem('token');
    if (!token) {
      throw new Error('Not authenticated');
    }

    const response = await fetch('/api/addresses', {
      headers: {
        'Authorization': `Bearer ${token}`,
      },
    });

    if (!response.ok) {
      throw new Error('Failed to fetch addresses');
    }

    return response.json();
  }

  /**
   * Verify address via API
   */
  static async verifyAddress(addressId: number, token: string) {
    const validated = verifyAddressSchema.parse({ addressId, token });

    logger.info({ addressId }, 'Verifying address');

    const authToken = localStorage.getItem('token');
    if (!authToken) {
      throw new Error('Not authenticated');
    }

    const response = await fetch(`/api/addresses/${validated.addressId}/verify`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${authToken}`,
      },
      body: JSON.stringify({ token: validated.token }),
    });

    if (!response.ok) {
      const error = await response.json();
      throw new Error(error.error || 'Failed to verify address');
    }

    logger.info({ addressId }, 'Address verified successfully');
    return response.json();
  }

  /**
   * Delete address via API
   */
  static async deleteAddress(addressId: number) {
    logger.info({ addressId }, 'Deleting address');

    const token = localStorage.getItem('token');
    if (!token) {
      throw new Error('Not authenticated');
    }

    const response = await fetch(`/api/addresses/${addressId}/deactivate`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${token}`,
      },
      body: JSON.stringify({ addressId }),
    });

    if (!response.ok) {
      const error = await response.json();
      throw new Error(error.error || 'Failed to delete address');
    }

    logger.info({ addressId }, 'Address deleted successfully');
    return response.json();
  }
}
