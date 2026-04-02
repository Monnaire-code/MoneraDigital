/**
 * API Client for Monera Digital
 *
 * This client provides a unified way to make API requests with proper base URL configuration.
 * It supports both development (Vite proxy) and production (direct backend URL) environments.
 * It also handles automatic token refresh on 401 responses.
 */

import { tokenManager, type TokenPair } from './token-manager.js';

// API base URL configuration
// Development: Use Vite proxy (empty string means relative paths)
// Production: Set VITE_API_BASE_URL to your backend URL
const API_BASE_URL = import.meta.env.VITE_API_BASE_URL || '';

// Helper function to build full API URLs
export function getApiUrl(path: string): string {
  // If base URL is configured (production), use it
  if (API_BASE_URL) {
    // Remove leading slash from path if present
    const cleanPath = path.startsWith('/') ? path.slice(1) : path;
    // Ensure base URL doesn't end with slash
    const cleanBase = API_BASE_URL.endsWith('/') ? API_BASE_URL.slice(0, -1) : API_BASE_URL;
    return `${cleanBase}/${cleanPath}`;
  }

  // Development: use relative path (Vite proxy handles it)
  return path;
}

/**
 * API Error class with structured error information
 */
export class ApiError extends Error {
  constructor(
    message: string,
    public readonly code: string,
    public readonly status: number,
    public readonly originalResponse?: unknown
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

interface PendingRequest {
  resolve: (token: TokenPair) => void;
  reject: (error: Error) => void;
}

let pendingRefreshRequest: Promise<TokenPair> | null = null;

/**
 * Get or create a shared refresh token promise
 * This ensures concurrent 401 requests share the same refresh attempt
 */
function getOrCreateRefreshPromise(refreshToken: string): Promise<TokenPair> {
  if (pendingRefreshRequest) {
    return pendingRefreshRequest;
  }

  pendingRefreshRequest = tokenManager.refreshToken().finally(() => {
    pendingRefreshRequest = null;
  });

  return pendingRefreshRequest;
}

/**
 * Refresh token and retry the original request
 */
async function handle401AndRetry<T>(
  url: string,
  options: RequestInit,
  originalToken: string
): Promise<T> {
  const refreshToken = tokenManager.getRefreshToken();

  if (!refreshToken) {
    tokenManager.clearTokens();
    if (typeof window !== 'undefined') {
      window.location.href = '/login';
    }
    throw new ApiError('Session expired. Please log in again.', 'SESSION_EXPIRED', 401);
  }

  try {
    const newTokens = await getOrCreateRefreshPromise(refreshToken);

    const newOptions: RequestInit = {
      ...options,
      headers: {
        ...options.headers,
        'Authorization': `Bearer ${newTokens.accessToken}`,
      },
    };

    const response = await fetch(url, newOptions);

    if (!response.ok) {
      const errorData = await parseErrorResponse(response);
      throw new ApiError(errorData.message, errorData.code, response.status, errorData);
    }

    return await parseSuccessResponse<T>(response);
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) {
      tokenManager.clearTokens();
      if (typeof window !== 'undefined') {
        window.location.href = '/login';
      }
    }
    throw error;
  }
}

/**
 * Parse error response from API
 */
async function parseErrorResponse(response: Response): Promise<{ message: string; code: string }> {
  const contentType = response.headers.get('content-type');
  const isJson = contentType?.includes('application/json');

  if (isJson) {
    try {
      const data = await response.json();
      return {
        message: data.error || data.message || response.statusText,
        code: data.code || `HTTP_${response.status}`,
      };
    } catch {
      return {
        message: response.statusText,
        code: `HTTP_${response.status}`,
      };
    }
  }

  return {
    message: response.statusText,
    code: `HTTP_${response.status}`,
  };
}

/**
 * Parse success response from API
 */
async function parseSuccessResponse<T>(response: Response): Promise<T> {
  const contentType = response.headers.get('content-type');
  const isJson = contentType?.includes('application/json') || contentType?.includes('text/json');

  if (isJson) {
    return await response.json() as T;
  }

  return {} as T;
}

/**
 * Type-safe fetch wrapper for API requests with automatic 401 handling
 */
export async function apiRequest<T>(
  path: string,
  options: RequestInit = {},
  _retryOn401: boolean = true
): Promise<T> {
  const url = getApiUrl(path);
  const method = options.method || 'GET';

  // Debug logging for account opening flow
  console.log(`[DEBUG-ACCOUNT-OPENING] API Request: ${method} ${path}`, {
    timestamp: new Date().toISOString(),
    hasBody: !!options.body,
    headers: options.headers ? 'present' : 'absent',
  });

  const token = tokenManager.getAccessToken();

  const defaultOptions: RequestInit = {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { 'Authorization': `Bearer ${token}` } : {}),
      ...options.headers,
    },
  };

  const response = await fetch(url, defaultOptions);

  // Debug logging for response
  console.log(`[DEBUG-ACCOUNT-OPENING] API Response: ${method} ${path}`, {
    status: response.status,
    statusText: response.statusText,
    timestamp: new Date().toISOString(),
  });

  // Handle error responses
  if (!response.ok) {
    const status = response.status;

    // Handle 401 Unauthorized specifically with auto-refresh
    if (status === 401 && _retryOn401) {
      console.log(`[DEBUG-ACCOUNT-OPENING] API 401 detected: ${method} ${path}, attempting retry`);
      return handle401AndRetry<T>(url, defaultOptions, token || '');
    }

    const errorData = await parseErrorResponse(response);
    console.log(`[DEBUG-ACCOUNT-OPENING] API Error: ${method} ${path}`, {
      status,
      code: errorData.code,
      message: errorData.message,
    });

    // Handle 401 without retry (e.g., no refresh token)
    if (status === 401) {
      tokenManager.clearTokens();
      if (typeof window !== 'undefined') {
        window.location.href = '/login';
      }
      throw new ApiError(errorData.message, errorData.code, status, errorData);
    }

    throw new ApiError(errorData.message, errorData.code, status, errorData);
  }

  return await parseSuccessResponse<T>(response);
}

/**
 * Get API base URL (useful for debugging)
 */
export function getApiBaseUrl(): string {
  return API_BASE_URL || '(Vite Proxy - /api)';
}

/**
 * Check if user is authenticated
 */
export function isAuthenticated(): boolean {
  return tokenManager.isAuthenticated();
}

/**
 * Get current access token
 */
export function getAccessToken(): string | null {
  return tokenManager.getAccessToken();
}
