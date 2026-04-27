import { z } from 'zod';
import logger from './logger.js';
import { tokenManager, type TokenPair } from './token-manager.js';

const sendActivationSchema = z.object({
  email: z.string().email('Invalid email format'),
});

const verifyActivationSchema = z.object({
  email: z.string().email('Invalid email format'),
  code: z.string().length(6, 'Code must be 6 digits'),
});

export interface SendActivationResponse {
  success: boolean;
  message: string;
  retryAfter?: number;
}

export interface VerifyActivationResponse {
  success: boolean;
  message: string;
  status?: string;
  redirectUrl?: string;
  userId?: number;
  token?: string;
  accessToken?: string;
}

export interface LoginResponse {
  success: boolean;
  requiresActivation?: boolean;
  user?: {
    id: number;
    email: string;
    status: string;
    twoFactorEnabled: boolean;
  };
  token?: string;
  accessToken?: string;
  refreshToken?: string;
  tokenType?: string;
  expiresIn?: number;
  expiresAt?: string;
  error?: string;
  code?: string;
  retryAfter?: number;
}

export class ActivationService {
  static async sendActivationCode(email: string): Promise<SendActivationResponse> {
    const validated = sendActivationSchema.parse({ email });

    logger.info({ email }, 'Sending activation code');

    const response = await fetch('/api/auth/send-activation', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      body: JSON.stringify(validated),
    });

    const data = await response.json();

    if (response.status === 429) {
      return {
        success: false,
        message: data.error || 'too many requests',
        retryAfter: data.retryAfter,
      };
    }

    if (!response.ok) {
      throw new Error(data.error || 'Failed to send activation code');
    }

    logger.info({ email }, 'Activation code sent');
    return data;
  }

  static async verifyActivationCode(email: string, code: string): Promise<VerifyActivationResponse> {
    const validated = verifyActivationSchema.parse({ email, code });

    logger.info({ email }, 'Verifying activation code');

    const response = await fetch('/api/auth/verify-activation', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      body: JSON.stringify(validated),
    });

    const data = await response.json();

    if (!response.ok) {
      const errorMessage = data.error || data.message || 'Verification failed';
      const error: Error & { code?: string; retryAfter?: number } = new Error(errorMessage);
      error.code = data.code;
      error.retryAfter = data.retryAfter;
      throw error;
    }

    if (data.token || data.accessToken) {
      const tokenPair: TokenPair = {
        accessToken: data.accessToken || data.token || '',
        refreshToken: data.refreshToken || '',
        tokenType: data.tokenType || 'Bearer',
        expiresIn: data.expiresIn || 86400,
      };

      tokenManager.setTokens(tokenPair);
      logger.info({ email }, 'Account activated and logged in');
    }

    return data;
  }
}
