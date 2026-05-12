import type { VercelRequest, VercelResponse } from '@vercel/node';
import { verifyToken } from '../src/lib/auth-middleware.js';
import logger from '../src/lib/logger.js';

const BACKEND_URL = process.env.BACKEND_URL;

/**
 * Unified API Router
 *
 * Routes all API requests through a single handler.
 * Replaces 11 individual endpoint files.
 *
 * Routing table maps [METHOD /path] to backend configuration.
 * Supports both exact matches and pattern matching for dynamic routes.
 */

interface RouteConfig {
  requiresAuth: boolean;
  backendPath: string;
}

// Route configuration: maps "METHOD /path" to backend endpoint
// Note: Keys must include /api prefix since frontend calls /api/xxx
const ROUTE_CONFIG: Record<string, RouteConfig> = {
  // Auth endpoints
  'POST /api/auth/login': { requiresAuth: false, backendPath: '/api/auth/login' },
  'POST /api/auth/register': { requiresAuth: false, backendPath: '/api/auth/register' },
  'GET /api/auth/me': { requiresAuth: true, backendPath: '/api/auth/me' },
  'POST /api/auth/refresh': { requiresAuth: false, backendPath: '/api/auth/refresh' },
  'POST /api/auth/logout': { requiresAuth: true, backendPath: '/api/auth/logout' },

  // 2FA endpoints
  'POST /api/auth/2fa/setup': { requiresAuth: true, backendPath: '/api/auth/2fa/setup' },
  'POST /api/auth/2fa/enable': { requiresAuth: true, backendPath: '/api/auth/2fa/enable' },
  'POST /api/auth/2fa/disable': { requiresAuth: true, backendPath: '/api/auth/2fa/disable' },
  'GET /api/auth/2fa/status': { requiresAuth: true, backendPath: '/api/auth/2fa/status' },
  'POST /api/auth/2fa/verify-login': { requiresAuth: false, backendPath: '/api/auth/2fa/verify-login' },
  'POST /api/auth/2fa/skip': { requiresAuth: false, backendPath: '/api/auth/2fa/skip' },

  // Activation endpoints
  'POST /api/auth/send-activation': { requiresAuth: false, backendPath: '/api/auth/send-activation' },
  'POST /api/auth/verify-activation': { requiresAuth: false, backendPath: '/api/auth/verify-activation' },

  // Contact info endpoint
  'POST /api/contact-info': { requiresAuth: true, backendPath: '/api/contact-info' },

  // Address endpoints
  'GET /api/addresses': { requiresAuth: true, backendPath: '/api/addresses' },
  'POST /api/addresses': { requiresAuth: true, backendPath: '/api/addresses' },

  // 2FA verify endpoint
  'POST /api/auth/2fa/verify': { requiresAuth: true, backendPath: '/api/auth/2fa/verify' },

  // Dynamic address routes: /api/addresses/123, /api/addresses/123/deactivate, /api/addresses/123/verify, etc.

  // Wallet endpoints — Safeheron Phase 1
  'GET /api/wallet/deposit-address': { requiresAuth: true, backendPath: '/api/wallet/deposit-address' },
  'GET /api/wallet/supported-chains': { requiresAuth: true, backendPath: '/api/wallet/supported-chains' },

  // Legacy wallet endpoints — backend returns 410 Gone. Kept here so the
  // frontend rollout window gets a clear migration message instead of 404.
  'POST /api/wallet/create': { requiresAuth: true, backendPath: '/api/wallet/create' },
  'GET /api/wallet/info': { requiresAuth: true, backendPath: '/api/wallet/info' },
  'POST /api/wallet/addresses': { requiresAuth: true, backendPath: '/api/wallet/addresses' },
  'POST /api/wallet/address/incomeHistory': { requiresAuth: true, backendPath: '/api/wallet/address/incomeHistory' },
  'POST /api/wallet/address/get': { requiresAuth: true, backendPath: '/api/wallet/address/get' },

  // Webhook endpoints (public, verified via Safeheron signature)
  'POST /api/webhooks/safeheron': { requiresAuth: false, backendPath: '/api/webhooks/safeheron' },

  // Assets endpoints
  'GET /api/assets': { requiresAuth: true, backendPath: '/api/assets' },
  'GET /api/assets/prices': { requiresAuth: true, backendPath: '/api/assets/prices' },
  'POST /api/assets/refresh-prices': { requiresAuth: true, backendPath: '/api/assets/refresh-prices' },

  // Wealth endpoints
  'GET /api/wealth/products': { requiresAuth: true, backendPath: '/api/wealth/products' },
  'POST /api/wealth/subscribe': { requiresAuth: true, backendPath: '/api/wealth/subscribe' },
  'GET /api/wealth/orders': { requiresAuth: true, backendPath: '/api/wealth/orders' },
  'POST /api/wealth/redeem': { requiresAuth: true, backendPath: '/api/wealth/redeem' },
  'GET /api/wealth/interest-history': { requiresAuth: true, backendPath: '/api/wealth/interest-history' },

  // Lending endpoints
  'GET /api/lending/positions': { requiresAuth: true, backendPath: '/api/lending/positions' },
  'POST /api/lending/apply': { requiresAuth: true, backendPath: '/api/lending/apply' },

  // Deposit endpoints
  'GET /api/deposits': { requiresAuth: true, backendPath: '/api/deposits' },

  // Withdrawal endpoints
  'GET /api/withdrawals': { requiresAuth: true, backendPath: '/api/withdrawals' },
  'POST /api/withdrawals': { requiresAuth: true, backendPath: '/api/withdrawals' },
  'GET /api/withdrawals/fees': { requiresAuth: true, backendPath: '/api/withdrawals/fees' },
  'GET /api/withdrawals/:id': { requiresAuth: true, backendPath: '' },
};

/**
 * Parse incoming request to extract method and path
 */
function parseRoute(req: VercelRequest): { method: string; path: string; query: string } {
  const method = req.method || 'GET';
  const routePath = Array.isArray(req.query.route)
    ? req.query.route.join('/')
    : req.query.route || '';
  const path = `/api/${routePath}`;

  // Extract query string from full URL
  const url = req.url || '';
  const queryIndex = url.indexOf('?');
  const query = queryIndex >= 0 ? url.substring(queryIndex) : '';

  return { method, path, query };
}

/**
 * Find matching route in configuration
 */
function findRoute(method: string, path: string): { found: boolean; config?: RouteConfig; backendPath?: string } {
  // Check exact match first
  const exactKey = `${method} ${path}`;
  if (ROUTE_CONFIG[exactKey]) {
    return { found: true, config: ROUTE_CONFIG[exactKey], backendPath: ROUTE_CONFIG[exactKey].backendPath };
  }

  // Handle dynamic withdrawal routes: /api/withdrawals/123
  if (path.match(/^\/api\/withdrawals\/\d+$/)) {
    return {
      found: true,
      config: { requiresAuth: true, backendPath: '' },
      backendPath: `/api/withdrawals/${path.split('/').pop()}`,
    };
  }

  // Handle dynamic address routes: /api/addresses/123, /api/addresses/123/verify, etc.
  if (path.startsWith('/api/addresses/')) {
    return {
      found: true,
      config: { requiresAuth: true, backendPath: '' },
      backendPath: `/api${path.replace('/api/addresses/', '/addresses/')}`,
    };
  }

  return { found: false };
}

/**
 * Main API router handler
 */
export default async function handler(req: VercelRequest, res: VercelResponse) {
  try {
    // Validate backend URL
    if (!BACKEND_URL) {
      logger.error({}, 'BACKEND_URL not configured');
      return res.status(500).json({
        error: 'Server configuration error',
        message: 'Backend URL not configured',
        code: 'BACKEND_URL_MISSING',
      });
    }

    // Parse request
    const { method, path, query } = parseRoute(req);

    // Log incoming request for debugging
    logger.debug({
      method,
      path,
      query,
      hasAuth: !!req.headers.authorization,
    }, 'Handling API request');

    // Find matching route
    const routeMatch = findRoute(method, path);
    if (!routeMatch.found) {
      logger.warn({ method, path }, `Route not found for ${method} ${path}`);
      return res.status(404).json({
        error: 'Not Found',
        message: `No route found for ${method} ${path}`,
        code: 'ROUTE_NOT_FOUND',
      });
    }

    const routeConfig = routeMatch.config!;
    const backendPath = routeMatch.backendPath || routeConfig.backendPath;

    // Construct backend URL with query parameters
    const backendUrl = `${BACKEND_URL}${backendPath}${query}`;

    // Check authentication if required
    if (routeConfig.requiresAuth) {
      const user = verifyToken(req);
      if (!user) {
        logger.warn({ path }, 'Authentication required but token missing');
        return res.status(401).json({
          code: 'MISSING_TOKEN',
          message: 'Authentication required',
          error: 'Unauthorized',
        });
      }
    }

    // Validate HTTP method
    if (!['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS'].includes(method)) {
      logger.error({ method, path }, `Method not allowed: ${method}`);
      return res.status(405).json({
        error: 'Method Not Allowed',
        code: 'METHOD_NOT_ALLOWED',
        message: `HTTP method ${method} not allowed for ${path}`,
        allowedMethods: ['GET', 'POST', 'PUT', 'PATCH', 'DELETE'],
      });
    }

    // Extract all custom headers (excluding host and standard headers)
    const customHeaders: Record<string, string> = {};
    if (req.headers.authorization) {
      customHeaders['Authorization'] = req.headers.authorization;
    }
    if (req.headers['idempotency-key']) {
      customHeaders['Idempotency-Key'] = req.headers['idempotency-key'] as string;
    }
    if (req.headers['x-client-timestamp']) {
      customHeaders['X-Client-Timestamp'] = req.headers['x-client-timestamp'] as string;
    }
    // Forward other custom headers that may be needed
    const headerWhitelist = ['x-request-id', 'x-correlation-id', 'x-forwarded-for', 'x-real-ip'];
    headerWhitelist.forEach(key => {
      const value = req.headers[key];
      if (value) {
        customHeaders[key] = value as string;
      }
    });

    // Prepare request options
    const options: RequestInit = {
      method,
      headers: {
        'Content-Type': 'application/json',
        ...customHeaders,
      },
    };

    // Add body for methods that support it
    if (['POST', 'PUT', 'PATCH'].includes(method) && req.body) {
      options.body = JSON.stringify(req.body);
    }

    // Call backend
    logger.debug({ backendUrl, method }, 'Calling backend');
    const response = await fetch(backendUrl, options);

    // Parse response JSON with error handling
    let data = {};
    try {
      data = await response.json();
    } catch (parseError) {
      logger.warn(
        { status: response.status, statusText: response.statusText },
        'Failed to parse response as JSON'
      );
      // For non-2xx responses with invalid JSON, return status with error message
      if (!response.ok) {
        data = {
          error: response.statusText || 'Backend error',
          status: response.status,
          message: `Backend returned status ${response.status} with invalid response body`,
          code: 'BACKEND_ERROR',
        };
      }
    }

    // Log audit trail for sensitive operations
    if (method === 'POST' && path === '/api/auth/2fa/skip') {
      logger.info({ userId: req.body?.userId }, '2FA verification skipped during login');
    }

    // Return backend response
    logger.debug({ status: response.status, path }, 'Returning response');
    return res.status(response.status).json(data);
  } catch (error: unknown) {
    const errorMessage = error instanceof Error ? error.message : 'Unknown error';
    logger.error({ error: errorMessage }, 'API router error');
    return res.status(500).json({
      error: 'Internal Server Error',
      message: 'Failed to process request',
      code: 'INTERNAL_ERROR',
      details: process.env.NODE_ENV === 'development' ? errorMessage : undefined,
    });
  }
}
