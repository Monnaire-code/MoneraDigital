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
 * Routing table maps [METHOD PATH] to backend configuration.
 * Supports both exact matches and pattern matching for dynamic routes.
 */

interface RouteConfig {
  requiresAuth: boolean;
  backendPath: string;
}

// Route configuration: maps "METHOD /path" to backend endpoint
const ROUTE_CONFIG: Record<string, RouteConfig> = {
  // Auth endpoints
  'POST /auth/login': { requiresAuth: false, backendPath: '/api/auth/login' },
  'POST /auth/register': { requiresAuth: false, backendPath: '/api/auth/register' },
  'GET /auth/me': { requiresAuth: true, backendPath: '/api/auth/me' },
  'POST /auth/refresh': { requiresAuth: false, backendPath: '/api/auth/refresh' },
  'POST /auth/logout': { requiresAuth: true, backendPath: '/api/auth/logout' },

  // 2FA endpoints
  'POST /auth/2fa/setup': { requiresAuth: true, backendPath: '/api/auth/2fa/setup' },
  'POST /auth/2fa/enable': { requiresAuth: true, backendPath: '/api/auth/2fa/enable' },
  'POST /auth/2fa/disable': { requiresAuth: true, backendPath: '/api/auth/2fa/disable' },
  'GET /auth/2fa/status': { requiresAuth: true, backendPath: '/api/auth/2fa/status' },
  'POST /auth/2fa/verify-login': { requiresAuth: false, backendPath: '/api/auth/2fa/verify-login' },
  'POST /auth/2fa/skip': { requiresAuth: false, backendPath: '/api/auth/2fa/skip' },

  // Address endpoints (base)
  'GET /addresses': { requiresAuth: true, backendPath: '/api/addresses' },
  'POST /addresses': { requiresAuth: true, backendPath: '/api/addresses' },

  'POST /wallet/create': { requiresAuth: true, backendPath: '/api/wallet/create' },
  'GET /wallet/info': { requiresAuth: true, backendPath: '/api/wallet/info' },
  'POST /wallet/addresses': { requiresAuth: true, backendPath: '/api/wallet/addresses' },
  'POST /wallet/address/incomeHistory': { requiresAuth: true, backendPath: '/api/wallet/address/incomeHistory' },
  'POST /wallet/address/get': { requiresAuth: true, backendPath: '/api/wallet/address/get' },

  // Assets endpoints
  'GET /assets': { requiresAuth: true, backendPath: '/api/assets' },
  'POST /assets/refresh-prices': { requiresAuth: true, backendPath: '/api/assets/refresh-prices' },

  // Wealth endpoints
  'GET /wealth/products': { requiresAuth: true, backendPath: '/api/wealth/products' },
  'POST /wealth/subscribe': { requiresAuth: true, backendPath: '/api/wealth/subscribe' },
  'GET /wealth/orders': { requiresAuth: true, backendPath: '/api/wealth/orders' },
  'POST /wealth/redeem': { requiresAuth: true, backendPath: '/api/wealth/redeem' },

  // Lending endpoints
  'GET /lending/positions': { requiresAuth: true, backendPath: '/api/lending/positions' },
  'POST /lending/apply': { requiresAuth: true, backendPath: '/api/lending/apply' },

  // Deposit endpoints
  'GET /deposits': { requiresAuth: true, backendPath: '/api/deposits' },

  // Withdrawal endpoints
  'GET /withdrawals': { requiresAuth: true, backendPath: '/api/withdrawals' },
  'POST /withdrawals': { requiresAuth: true, backendPath: '/api/withdrawals' },
  'GET /withdrawals/fees': { requiresAuth: true, backendPath: '/api/withdrawals/fees' },
  'GET /withdrawals/:id': { requiresAuth: true, backendPath: '' },
};

/**
 * Parse incoming request to extract method and path
 */
function parseRoute(req: VercelRequest): { method: string; path: string } {
  const method = req.method || 'GET';
  const routePath = Array.isArray(req.query.route)
    ? req.query.route.join('/')
    : req.query.route || '';
  const path = `/${routePath}`;

  return { method, path };
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

  // Handle dynamic address routes: /addresses/123, /addresses/123/verify, etc.
  if (path.startsWith('/addresses/')) {
    const isValidAddressRoute =
      /^\/addresses\/[\w-]+(\/verify|\/primary)?$/.test(path) &&
      (method === 'DELETE' || method === 'POST' || method === 'PUT' || method === 'PATCH');

    if (isValidAddressRoute) {
      return {
        found: true,
        config: { requiresAuth: true, backendPath: '' },
        backendPath: `/api${path}`,
      };
    }
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
    const { method, path } = parseRoute(req);

    // Log incoming request for debugging
    logger.debug({
      method,
      path,
      hasAuth: !!req.headers.authorization,
      query: req.query,
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

    // Debug logging for account opening flow
    if (path === '/wallet/create') {
      console.log(`[DEBUG-ACCOUNT-OPENING] Vercel Route matched: ${method} ${path} -> ${backendPath}`);
      console.log(`[DEBUG-ACCOUNT-OPENING] Vercel Request body:`, req.body);
    }
    
    // Debug logging for add address flow
    if (path === '/wallet/addresses' && method === 'POST') {
      console.log(`[DEBUG-ADDRESS] Vercel Route matched: ${method} ${path} -> ${backendPath}`);
      console.log(`[DEBUG-ADDRESS] Vercel Request body:`, JSON.stringify(req.body, null, 2));
    }
    if (path === '/wallet/create') {
      console.log(`[DEBUG-ACCOUNT-OPENING] Vercel Route matched: ${method} ${path} -> ${backendPath}`);
      console.log(`[DEBUG-ACCOUNT-OPENING] Vercel Request body:`, req.body);
    }

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

    // Construct backend URL
    const backendUrl = `${BACKEND_URL}${backendPath}`;

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
    if (method === 'POST' && path === '/auth/2fa/skip') {
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
