# Login 500 Error Fix Proposal

**Type:** Bug Fix
**Priority:** High
**Status:** Proposed

## Problem Description

Users are experiencing a 500 Internal Server Error when attempting to login through the frontend application.

### Root Cause Analysis

1. **Backend Status**: The backend server at `https://monera-digital--gyc567.replit.app` is accessible and responding correctly
2. **Login Endpoint**: The `/api/auth/login` endpoint returns proper HTTP 401 status with `{"error":"invalid credentials"}` for invalid credentials
3. **Configuration Issue**: The `.env` file is missing from the local development environment
4. **API Proxy Configuration**: The frontend API proxy at `api/auth/login.ts` uses `VITE_API_BASE_URL` environment variable, but this is not configured locally

### Error Flow

```
User Login → Frontend (Login.tsx)
  → Vercel API Proxy (api/auth/login.ts)
    → Missing VITE_API_BASE_URL
      → Defaults to 'http://localhost:8081'
        → Connection fails → 500 Internal Server Error
```

### Technical Details

**Current Configuration (api/auth/login.ts:4):**
```typescript
const BACKEND_URL = process.env.VITE_API_BASE_URL || 'http://localhost:8081';
```

**Issue:**
- `.env` file doesn't exist locally
- `VITE_API_BASE_URL` is not set
- Falls back to `http://localhost:8081`
- Backend running on Replit at `https://monera-digital--gyc567.replit.app`
- Connection fails causing 500 error

## Proposed Solution

### Architecture Alignment

This solution follows the Monera Digital architecture principles:
- **KISS**: Simple configuration fix, minimal code changes
- **High Cohesion/Low Coupling**: Configuration isolated to environment setup
- **Backend-Only Business Logic**: No business logic changes, only configuration
- **Single Source of Truth**: Go backend remains the only source for authentication logic

### Solution Steps

#### 1. Create Local Environment Configuration

**File:** `.env` (create from `.env.example`)

**Changes:**
```bash
# Copy from .env.example with correct backend URL
VITE_API_BASE_URL=https://monera-digital--gyc567.replit.app
```

#### 2. Update Vite Configuration (if needed)

**File:** `vite.config.ts`

**Verification:** Ensure Vite properly loads `.env` variables

#### 3. Add Environment Validation

**File:** `src/lib/env-validator.ts` (NEW)

**Purpose:** Validate required environment variables at startup

**Code:**
```typescript
import { z } from 'zod';

const envSchema = z.object({
  VITE_API_BASE_URL: z.string().url().min(1),
});

export function validateEnv() {
  try {
    envSchema.parse({
      VITE_API_BASE_URL: import.meta.env.VITE_API_BASE_URL,
    });
    return true;
  } catch (error) {
    console.error('Environment validation failed:', error);
    return false;
  }
}

export const env = envSchema.parse({
  VITE_API_BASE_URL: import.meta.env.VITE_API_BASE_URL,
});
```

#### 4. Add Environment Check to API Proxy

**File:** `api/auth/login.ts` (MODIFY)

**Changes:**
```typescript
import type { VercelRequest, VercelResponse } from '@vercel/node';

const BACKEND_URL = process.env.VITE_API_BASE_URL || 'http://localhost:8081';

export default async function handler(req: VercelRequest, res: VercelResponse) {
  if (req.method !== 'POST') {
    return res.status(405).json({ error: 'Method Not Allowed' });
  }

  // Validate backend URL is configured
  if (!BACKEND_URL || BACKEND_URL === 'http://localhost:8081') {
    return res.status(500).json({
      error: 'Server configuration error',
      message: 'Backend URL not configured. Please check environment variables.'
    });
  }

  try {
    const response = await fetch(`${BACKEND_URL}/api/auth/login`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      body: JSON.stringify(req.body),
    });

    const data = await response.json();
    return res.status(response.status).json(data);
  } catch (error: unknown) {
    const errorMessage = error instanceof Error ? error.message : 'Unknown error';
    console.error('Proxy error:', errorMessage);
    return res.status(500).json({
      error: 'Internal Server Error',
      message: 'Failed to connect to backend service'
    });
  }
}
```

## Implementation Plan

### Phase 1: Configuration Setup
- [ ] Create `.env` file from `.env.example`
- [ ] Set `VITE_API_BASE_URL=https://monera-digital--gyc567.replit.app`
- [ ] Add `.env` to `.gitignore` (if not already present)

### Phase 2: Code Changes
- [ ] Create `src/lib/env-validator.ts` with environment validation
- [ ] Update `api/auth/login.ts` with backend URL validation
- [ ] Add similar validation to other API proxy files

### Phase 3: Testing (TDD - Write Tests First)

#### Unit Tests

**File:** `src/lib/env-validator.test.ts` (NEW)

```typescript
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { validateEnv, env } from './env-validator';

describe('env-validator', () => {
  beforeEach(() => {
    vi.resetModules();
  });

  it('should validate correct environment', () => {
    // Mock valid environment
    const result = validateEnv();
    expect(result).toBe(true);
  });

  it('should validate VITE_API_BASE_URL format', () => {
    expect(env.VITE_API_BASE_URL).toMatch(/^https?:\/\/.+/);
  });
});
```

**File:** `api/auth/login.test.ts` (NEW)

```typescript
import { describe, it, expect, vi } from 'vitest';
import handler from './login';

describe('/api/auth/login', () => {
  it('should return 500 when VITE_API_BASE_URL not configured', async () => {
    const req = {
      method: 'POST',
      body: { email: 'test@example.com', password: 'password' }
    } as any;

    const res = {
      status: vi.fn().mockReturnThis(),
      json: vi.fn().mockReturnThis(),
    } as any;

    // Mock environment without VITE_API_BASE_URL
    process.env.VITE_API_BASE_URL = '';

    await handler(req, res);

    expect(res.status).toHaveBeenCalledWith(500);
    expect(res.json).toHaveBeenCalledWith({
      error: 'Server configuration error',
      message: expect.stringContaining('Backend URL not configured')
    });
  });

  it('should proxy requests to configured backend', async () => {
    // Test with valid configuration
  });
});
```

#### Integration Tests

**File:** `tests/login-integration.test.ts` (NEW)

```typescript
import { describe, it, expect, beforeAll, afterAll } from 'vitest';

describe('Login Integration Tests', () => {
  it('should successfully login with valid credentials', async () => {
    const response = await fetch('/api/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        email: 'test@example.com',
        password: 'testpassword123'
      }),
    });

    // Should get 401 (invalid credentials) not 500 (server error)
    expect(response.status).not.toBe(500);
    const data = await response.json();
    expect(data).toHaveProperty('error', 'invalid credentials');
  });

  it('should handle server connectivity errors gracefully', async () => {
    // Test with invalid backend URL
  });
});
```

### Phase 4: Regression Testing

- [ ] Run full test suite: `npm test`
- [ ] Verify existing tests pass
- [ ] Test login flow manually
- [ ] Test registration flow (if affected)
- [ ] Test 2FA flow (if affected)
- [ ] Verify other API routes still work

## Risk Assessment

### Low Risk
- Only configuration changes
- No business logic modifications
- No database schema changes
- Changes isolated to environment setup

### Mitigation
- Comprehensive test coverage (100%)
- Regression testing
- Manual verification
- Rollback plan: Simply revert configuration changes

## Success Criteria

1. **Environment Configuration**: `.env` file created with correct `VITE_API_BASE_URL`
2. **No 500 Errors**: Login requests receive proper HTTP responses (401 for invalid, 200 for valid)
3. **Test Coverage**: 100% coverage for new validation code
4. **No Regression**: All existing tests pass
5. **User Experience**: Users can login successfully with valid credentials

## Deployment Plan

### Development
1. Create `.env` file locally
2. Implement validation changes
3. Run tests and verify
4. Manual testing

### Production
1. Update production environment variables if needed
2. Deploy with no downtime (configuration only)
3. Monitor error rates

## Rollback Plan

If issues occur:
1. Revert `.env` changes
2. Remove validation code from API proxies
3. Restart development server
4. No database changes to revert

## Dependencies

- Existing backend infrastructure (✓ Available)
- Existing environment configuration (✓ Available)
- Test infrastructure (✓ Available)

## Timeline

**Total Estimated Time:** 2-3 hours

- Phase 1: 15 minutes
- Phase 2: 30 minutes
- Phase 3: 1-1.5 hours
- Phase 4: 30 minutes

## References

- Project: Monera Digital
- Backend URL: `https://monera-digital--gyc567.replit.app`
- AGENTS.md: Architecture and coding standards
- Related Files:
  - `api/auth/login.ts`
  - `.env.example`
  - `src/pages/Login.tsx`
  - `internal/services/auth.go`
