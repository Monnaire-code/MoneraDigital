# OpenSpec: Fix 401 Unauthorized Error in Addresses API

**Date:** 2026-01-27
**Status:** PROPOSAL
**Priority:** HIGH
**Author:** Claude

---

## 1. Problem Statement

Users experience `401 Unauthorized` errors when accessing `/dashboard/addresses` after successful login. The frontend receives 401 responses from both:
- `GET /api/auth/me` - token verification failure
- `GET /api/addresses` - token verification failure

Error appears in browser console:
```
GET https://www.moneradigital.com/api/auth/me 401 (Unauthorized)
GET https://www.moneradigital.com/api/addresses 401 (Unauthorized)
```

**Impact:** Users cannot access the address management dashboard even with valid authentication credentials.

---

## 2. Root Cause Analysis

### Finding 1: Inconsistent Token Verification Pattern
- **`/api/auth/me` (api/auth/me.ts:20):** Verifies JWT token via `verifyToken(req)` ✓
- **`/api/addresses/index.ts` (api/addresses/index.ts:35):** Only forwards Authorization header, NO verification ✗
- **`/api/addresses/[...path].ts` (api/addresses/[...path].ts:34):** Only forwards Authorization header, NO verification ✗

### Finding 2: Token Verification Sequence
1. Frontend stores JWT token in localStorage after login ✓
2. Frontend retrieves token and includes `Authorization: Bearer <token>` header ✓
3. Vercel proxy handlers receive request with Authorization header ✓
4. **MISSING STEP:** `/api/addresses` handlers do NOT verify token before proxying
5. Go backend receives request but may reject if header is missing/invalid
6. Go backend returns 401 Unauthorized
7. Frontend receives 401 error → address loading fails

### Finding 3: Architectural Inconsistency
Two patterns exist in the codebase:
- **Pattern A** (`/api/auth/me`): Verify token → Proxy request
- **Pattern B** (`/api/addresses`): Proxy request → Let backend handle auth

**Result:** Pattern B fails when Go backend enforces strict authentication.

---

## 3. Proposed Solution

### Strategy: Apply Consistent Token Verification Pattern

Add `verifyToken()` check to ALL protected endpoints before proxying to Go backend.

### Implementation Details

#### File 1: `api/addresses/index.ts`

**Change:** Add token verification at the start

```typescript
import type { VercelRequest, VercelResponse } from '@vercel/node';
import { verifyToken } from '../../src/lib/auth-middleware.js';
import logger from '../../src/lib/logger.js';

export default async function handler(req: VercelRequest, res: VercelResponse) {
  const backendUrl = process.env.BACKEND_URL;

  if (!backendUrl) {
    return res.status(500).json({
      error: 'Server configuration error',
      message: 'Backend URL not configured',
    });
  }

  // Verify authentication token
  const user = verifyToken(req);
  if (!user) {
    return res.status(401).json({
      code: 'MISSING_TOKEN',
      message: 'Authentication required',
    });
  }

  // Allowed methods
  if (req.method !== 'GET' && req.method !== 'POST') {
    return res.status(405).json({ error: 'Method Not Allowed' });
  }

  try {
    const url = `${backendUrl}/api/addresses`;

    const options: RequestInit = {
      method: req.method,
      headers: {
        'Content-Type': 'application/json',
        'Authorization': req.headers.authorization || '',
      },
    };

    if (req.method === 'POST') {
      options.body = JSON.stringify(req.body);
    }

    const response = await fetch(url, options);
    const data = await response.json().catch(() => ({}));

    return res.status(response.status).json(data);
  } catch (error: unknown) {
    const errorMessage = error instanceof Error ? error.message : 'Unknown error';
    logger.error({ error: errorMessage }, 'Addresses proxy error');
    return res.status(500).json({
      error: 'Internal Server Error',
      message: 'Failed to connect to backend service',
    });
  }
}
```

#### File 2: `api/addresses/[...path].ts`

**Change:** Add token verification at the start

```typescript
import type { VercelRequest, VercelResponse } from '@vercel/node';
import { verifyToken } from '../../src/lib/auth-middleware.js';
import logger from '../../src/lib/logger.js';

export default async function handler(req: VercelRequest, res: VercelResponse) {
  const backendUrl = process.env.BACKEND_URL;
  const { path } = req.query;

  if (!backendUrl) {
    return res.status(500).json({
      error: 'Server configuration error',
      message: 'Backend URL not configured',
    });
  }

  // Verify authentication token
  const user = verifyToken(req);
  if (!user) {
    return res.status(401).json({
      code: 'MISSING_TOKEN',
      message: 'Authentication required',
    });
  }

  try {
    // Construct target URL
    const pathStr = Array.isArray(path) ? path.join('/') : path;
    const url = `${backendUrl}/api/addresses/${pathStr}`;

    const options: RequestInit = {
      method: req.method,
      headers: {
        'Content-Type': 'application/json',
        'Authorization': req.headers.authorization || '',
      },
    };

    if (req.body && (req.method === 'POST' || req.method === 'PUT' || req.method === 'PATCH')) {
      options.body = JSON.stringify(req.body);
    }

    const response = await fetch(url, options);
    const data = await response.json().catch(() => ({}));

    return res.status(response.status).json(data);
  } catch (error: unknown) {
    const errorMessage = error instanceof Error ? error.message : 'Unknown error';
    logger.error({ error: errorMessage }, 'Addresses sub-path proxy error');
    return res.status(500).json({
      error: 'Internal Server Error',
      message: 'Failed to connect to backend service',
    });
  }
}
```

### Key Changes
1. Import `verifyToken` from auth-middleware
2. Call `verifyToken(req)` before any proxying
3. Return 401 if token is missing or invalid
4. Ensure Authorization header is always forwarded (even if empty string)
5. Match response format from `/api/auth/me` for consistency

### Design Principles Applied
- **KISS:** Minimal, focused changes - just add token verification
- **High Cohesion:** Token verification logic reused from existing auth-middleware
- **Low Coupling:** No new dependencies, uses existing patterns
- **Consistency:** Matches existing `/api/auth/me` pattern
- **Fail-Safe:** Early return on auth failure prevents downstream issues

---

## 4. Test Strategy

### Unit Tests to Add
All tests use existing `verifyToken` mock patterns from `api/addresses/addresses.test.ts`

#### Test 1: Missing Authorization Header
```
GIVEN: Request with no Authorization header
WHEN:  Handler is called
THEN:  Return 401 with "Authentication required" message
```

#### Test 2: Invalid Token
```
GIVEN: Request with malformed Authorization header
WHEN:  Handler is called
THEN:  Return 401 with "Authentication required" message
```

#### Test 3: Successful Request with Valid Token
```
GIVEN: Request with valid Authorization header
WHEN:  Handler proxies to backend
THEN:  Return backend response with correct status
```

### Test Coverage
- Existing tests in `api/addresses/addresses.test.ts` verify token verification behavior
- Tests confirm both success and failure paths
- Tests validate Authorization header forwarding
- Target: 100% code coverage for new verification logic

---

## 5. Deployment Considerations

### Pre-Deployment Checklist
- [ ] All tests pass locally (`npm run test`)
- [ ] Build completes without errors (`npm run build`)
- [ ] No new console errors in dev mode
- [ ] Verify locally against local Go backend

### Deployment Steps
1. Commit changes to feature branch
2. Create PR for code review
3. Merge to main after approval
4. Push to Vercel (automatic via GitHub Actions)
5. Monitor error logs for 401 errors on production

### Rollback Plan
If issues occur:
1. Revert commit: `git revert <commit-hash>`
2. Push revert to trigger Vercel rebuild
3. Monitor logs to confirm old behavior restored

### Backward Compatibility
- ✓ No breaking changes to API contracts
- ✓ Existing valid requests continue to work
- ✓ Invalid requests (without token) now fail fast with 401 instead of 500
- ✓ Error response format matches existing patterns

---

## 6. Success Criteria

### User-Facing
- [ ] Users can access `/dashboard/addresses` without 401 errors
- [ ] Address list loads successfully
- [ ] Address operations (add, delete, verify) work correctly

### Technical
- [ ] No console 401 errors for authenticated users
- [ ] `verifyToken()` called before proxying in both handlers
- [ ] All existing tests pass
- [ ] New tests added and passing
- [ ] 100% test coverage maintained
- [ ] Deployment succeeds on Vercel

---

## 7. Related Issues & References

- **Similar Pattern:** `/api/auth/me.ts` implements same verification successfully
- **Architecture:** CLAUDE.md documents pure proxy pattern for API handlers
- **Security:** Token verification follows existing auth-middleware pattern
- **Testing:** Existing test structure in `api/addresses/addresses.test.ts` provides template

---

## 8. Approval & Sign-off

**Proposed By:** Claude (Jan 27, 2026)
**Status:** Ready for Implementation
**Review Required:** YES
**Testing Required:** YES (100% coverage)
**Deployment Required:** YES

---

## 9. Implementation Notes

### Why This Fix
1. **Consistency:** Matches working pattern from `/api/auth/me`
2. **Simplicity:** Minimal code changes, maximum effect
3. **Safety:** Early auth validation prevents backend errors
4. **Testability:** Easy to test with existing mock patterns
5. **Maintainability:** Future endpoints should follow same pattern

### Future Improvements
- Consider extracting auth verification into a shared middleware wrapper
- Add authentication to ALL protected endpoints consistently
- Implement token refresh mechanism for expired tokens
- Add more granular error messages for debugging
