# Proposal: Fix Withdrawal Address Creation 405 Error

## Problem
Users encounter a `405 Method Not Allowed` error and a JSON parsing error when trying to add a withdrawal address at `/dashboard/withdraw`.
This prevents users from saving withdrawal addresses.

## Root Cause
The project architecture requires a Vercel Serverless Function in `api/` to proxy requests to the Go backend (`internal/`).
Currently, `api/addresses` does not exist.
Requests to `POST /api/addresses` fall through to the Single Page Application (SPA) rewrite (serving `index.html`), which rejects POST requests with a 405 status code.

## Proposed Solution
Create the missing Vercel proxy functions for the `/api/addresses` namespace.
Adhering to the "Backend-Only Business Logic" principle, these functions will purely proxy requests to the Go backend.

## Implementation Details

Create the following files in `api/addresses/`:

1.  `api/addresses/index.ts`
    *   Handles `GET /api/addresses` (List)
    *   Handles `POST /api/addresses` (Create)
    *   Proxies to `${BACKEND_URL}/api/addresses`

2.  `api/addresses/[...path].ts`
    *   Handles sub-paths like:
        *   `DELETE /api/addresses/:id`
        *   `POST /api/addresses/:id/verify`
        *   `POST /api/addresses/:id/primary`
    *   Proxies to `${BACKEND_URL}/api/addresses/${path}`

## Testing Plan
1.  **Unit Tests**: Create a test file `api/addresses/addresses.test.ts` (mocking the backend fetch).
2.  **Manual Verification**: Use `curl` or the provided `mock-api-server.mjs` (if adaptable) or verify against the running backend to ensure the proxy works.
    *   Since the real backend is needed for 100% verification, we will rely on the unit test of the proxy logic.

## Design Principles Checklist
- [x] **KISS**: Simple proxy forwarding.
- [x] **High Cohesion/Low Coupling**: Frontend implementation details (proxy) separated from business logic (Go).
- [x] **Testing**: 100% coverage for the new proxy files.
- [x] **Isolation**: Changes only affect `api/addresses` routing.
