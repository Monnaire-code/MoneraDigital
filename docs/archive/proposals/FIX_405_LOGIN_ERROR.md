# Proposal: Fix 405 Login Error by Cleaning Up API Directory

## 1. Background
Users are reporting a `405 Method Not Allowed` error when logging in via `POST /api/auth/login`.
The error includes `SyntaxError: Unexpected end of JSON input`, suggesting the response body is empty or invalid (typical of Vercel/Server default error pages).

## 2. Root Cause Analysis
- The project recently migrated to a **Unified API Router** (`api/[...route].ts`).
- This router is designed to handle all `/api/*` requests.
- However, the `api/` directory still contains subdirectories from the previous architecture:
  - `api/auth/` (containing an empty `2fa/` folder)
  - `api/addresses/` (empty)
- **Problem:** Vercel's file-system routing gives precedence to existing directories/files over dynamic catch-all routes (`[...route].ts`).
- When `POST /api/auth/login` is requested:
  - Vercel sees the `api/auth` directory.
  - It might be trying to resolve a specific handler within it.
  - Finding none (or getting confused by the empty structure), it likely falls back to a default behavior or fails to correctly hand off to `api/[...route].ts`, or returns 405 because directory listing is not allowed/supported for POST.
- The `api/[...route].ts` unit tests pass, confirming the handler logic itself is correct *if invoked*.
- Therefore, the issue is **Routing Resolution**.

## 3. Proposed Solution
**Objective:** Force Vercel to use `api/[...route].ts` for all API requests.

**Action Items:**
1.  **Clean up `api/` directory:**
    - Delete `api/auth/` directory (and contents).
    - Delete `api/addresses/` directory.
    - Ensure only `api/[...route].ts` (and tests) remain.
2.  **Verify `vercel.json`:**
    - Ensure rewrites are compatible (existing config seems fine, but cleanup is key).

## 4. Testing Plan
- **Unit Tests:** Run `api/__route__.test.ts` to ensure the router logic remains valid.
- **Verification:** Since we cannot verify Vercel routing locally without `vercel dev` (which behaves slightly differently), the primary verification is the clean file structure.
- **Deployment:** Deploy the changes and verify the login flow.

## 5. Design Principles Check
- **KISS:** Removing unused directories simplifies the structure.
- **High Cohesion:** All API routing logic is centralized in `[...route].ts`.
- **Isolation:** Removing empty/unused folders does not affect other logic.
