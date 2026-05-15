# OpenSpec: Fix 405 Method Not Allowed Error on Login

**Date:** January 27, 2026  
**Status:** PROPOSAL  
**Priority:** CRITICAL (Users cannot login)  
**Author:** Gemini Agent

---

## 1. Problem Statement

### User-Reported Issue
When users try to login at https://www.moneradigital.com/login, they receive:
- **HTTP 405** error: "Failed to load resource: the server responded with a status of 405 ()"
- **JSON Parse Error:** "SyntaxError: Failed to execute 'json' on 'Response': Unexpected end of JSON input"

### Root Cause Analysis

**Diagnosis: SPA Fallback Issue**
1. The client sends `POST /api/auth/login`.
2. `vercel.json` has a rule: `"source": "/api/(.*)", "destination": "/api/$1"`.
3. Vercel looks for a file `api/auth/login` (or `.ts`, `.js`). **It does not exist.**
4. Vercel fails to match the specific file.
5. Crucially, it **does not fall back** to `api/[...route].ts` automatically because the rewrite rule was processed and didn't result in a match, OR it falls through to the **next** rewrite rule.
6. The next rule is `"source": "/(.*)", "destination": "/index.html"`.
7. Vercel serves `index.html`.
8. Since `index.html` is a static file, it **does not accept POST requests**.
9. Vercel returns **405 Method Not Allowed** for the POST request to `index.html`.
10. The response body is HTML (or empty), not JSON.
11. The frontend tries to parse it as JSON and fails with "Unexpected end of JSON input".

---

## 2. Proposed Solution

### Solution: Explicit Rewrite to Unified Router

We need to tell Vercel that **any** request starting with `/api/` (that doesn't match a static file) should be handled by our unified router `api/[...route].ts`.

**Change in `vercel.json`:**

```json
{
  "rewrites": [
    {
      "source": "/api/(.*)",
      "destination": "/api/[...route]"
    },
    {
      "source": "/(.*)",
      "destination": "/index.html"
    }
  ]
}
```

By changing the destination from `/api/$1` to `/api/[...route]`, we explicitly direct the traffic to our catch-all function.

### Verification of `api/[...route].ts`
Ensure the handler correctly parses the route even if it's rewritten.
The `parseRoute` function uses `req.query.route` or `req.url`.
Vercel populates `req.query` with path segments for `[...route]` files.

---

## 3. Implementation Plan

1.  **Modify `vercel.json`**: Update the API rewrite rule.
2.  **Verify `api/[...route].ts`**: Ensure it handles the routing logic robustly (already confirmed via tests).
3.  **Deploy & Verify**: Push changes and verify login.

---

## 4. Design Principles Applied

- **KISS**: Simple configuration change fixes the routing.
- **High Cohesion**: Routing logic remains centralized in the unified router.
- **Test Coverage**: Existing tests cover the router logic; this fix ensures traffic reaches it.
- **No Side Effects**: Only affects API routing, which is currently broken for these paths.

---

## 5. Success Criteria

- `POST /api/auth/login` returns 200/400/401 (handled by function), NOT 405 (handled by Vercel static).
- Response is valid JSON.