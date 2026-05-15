# OpenSpec: Fix 404 Not Found for 2FA Skip Endpoint

**Date:** January 27, 2026
**Status:** PROPOSAL
**Priority:** HIGH
**Author:** Gemini Agent

---

## 1. Problem Statement

### User-Reported Issue
Users receive a 404 error when attempting to skip 2FA setup during login:
- URL: `https://www.moneradigital.com/api/auth/2fa/skip`
- Method: `POST`
- Error: `404 Not Found`

### Root Cause Analysis
1.  **Frontend/Proxy:** `api/[...route].ts` correctly defines the route and forwards it to `/api/auth/2fa/skip`.
    ```typescript
    'POST /auth/2fa/skip': { requiresAuth: false, backendPath: '/api/auth/2fa/skip' },
    ```
2.  **Backend:** `internal/routes/routes.go` **does not** define this route in the public `auth` group.
    ```go
    auth := public.Group("/auth")
    {
        auth.POST("/register", h.Register)
        auth.POST("/login", h.Login)
        auth.POST("/refresh", h.RefreshToken)
        auth.POST("/logout", h.Logout)
        // 2FA验证登录 - 公开端点
        auth.POST("/2fa/verify-login", h.Verify2FALogin)
        // MISSING: /2fa/skip
    }
    ```

---

## 2. Proposed Solution

### Backend Changes
Add the missing route to `internal/routes/routes.go` and implement the handler in `internal/handlers/handlers.go`.

1.  **Update `internal/routes/routes.go`:**
    Add `auth.POST("/2fa/skip", h.Skip2FALogin)` to the public auth group.

2.  **Update `internal/handlers/handlers.go`:**
    Implement `Skip2FALogin` handler. Since skipping 2FA (if allowed) is essentially just logging in without the second factor or acknowledging the skip, we need to verify what the business logic should be.
    *Assumption:* "Skipping" 2FA usually means the user is in a "setup 2FA" flow but chooses to do it later. This implies they have partially authenticated (username/password) and have a temporary session or user ID.
    However, looking at the frontend code (implied), it seems to be calling this endpoint.
    If the user is *already* logged in (but 2FA is not enabled/enforced), they might just be navigating away.
    *Correction:* The `api/[...route].ts` says `requiresAuth: false`. This suggests it might be used during the login process where a temporary token or user ID is passed.

    Let's look at `Verify2FALogin`. It takes `UserID` and `Token`.
    `Skip2FA` likely needs `UserID` to finalize the login process and issue the JWT.

    *Business Logic for Skip:*
    - If 2FA is *optional* and the user hasn't set it up, they can skip the setup prompt.
    - The backend needs to issue the full access token (JWT) just like `Verify2FALogin` or `Login` does.

### Handler Implementation Details
The `Skip2FALogin` handler needs to:
1.  Accept `UserID` (from request body).
2.  Verify the user exists.
3.  Check if 2FA is indeed *not enabled* (you can't skip if it's enabled).
4.  Generate and return the auth tokens (Access/Refresh) + User info.

---

## 3. Implementation Plan

1.  **Verify Handler Logic:** I'll check `Verify2FALogin` in `internal/handlers/handlers.go` to mimic the token issuance logic.
2.  **Modify `internal/handlers/handlers.go`:** Add `Skip2FALogin`.
3.  **Modify `internal/routes/routes.go`:** Register the route.
4.  **Test:** Add a unit test for the new handler.

---

## 4. Design Principles Applied

-   **KISS:** Reusing existing token generation logic.
-   **High Cohesion:** 2FA logic stays within auth handlers.
-   **Test Coverage:** New handler will be tested.

---

## 5. Success Criteria

-   `POST /api/auth/2fa/skip` returns 200 OK with `LoginResponse` (tokens).
-   User is successfully logged in after skipping.