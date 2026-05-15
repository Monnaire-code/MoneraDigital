# Fix 2FA Status Synchronization

## Status
- **Status**: Completed
- **Date**: 2026-01-26
- **Author**: Gemini Agent
- **Type**: Bug Fix

## Problem Description
After a user successfully enables 2FA, the frontend Security page continues to show "2FA is currently not enabled" even after a page refresh.

### Root Cause Analysis
1.  **Frontend Logic**: The Security page (`src/pages/dashboard/Security.tsx`) fetches the user's 2FA status from the `/api/auth/me` endpoint.
2.  **Backend Behavior**: The Go backend's `GetMe` handler (`internal/handlers/handlers.go`) previously constructed the response solely from the JWT claims (User ID and Email) stored in the context. It **did not** query the database for the latest user state.
3.  **Missing Data**: The `UserInfo` DTO and the `GetMe` response lacked the `TwoFactorEnabled` field.

## Solution Implemented

### 1. DTO Update
Modified `internal/dto/auth.go` to include `TwoFactorEnabled` in the `UserInfo` struct.

```go
type UserInfo struct {
    ID               int    `json:"id"`
    Email            string `json:"email"`
    TwoFactorEnabled bool   `json:"twoFactorEnabled"` // Added
}
```

### 2. Service Layer Update
Implemented `GetUserByID` in `internal/services/auth.go` to fetch the complete user profile, including the `two_factor_enabled` status, from the database.

```go
func (s *AuthService) GetUserByID(userID int) (*models.User, error) {
    var user models.User
    query := `SELECT id, email, two_factor_enabled FROM users WHERE id = $1`
    // ... execution ...
    return &user, nil
}
```

### 3. Handler Layer Update
Updated the `GetMe` handler in `internal/handlers/handlers.go` to:
1.  Retrieve the User ID from the context.
2.  Call `AuthService.GetUserByID` to get the fresh user data.
3.  Return the `twoFactorEnabled` status in the JSON response.

Also updated `Login`, `Register`, and `Verify2FALogin` handlers to ensure `twoFactorEnabled` is consistently returned in all authentication responses.

## Verification

### Automated Tests
- Created `internal/handlers/get_me_test.go` to verify `GetMe` returns the correct JSON structure with `twoFactorEnabled: true`.
- Ran suite `go test -v ./internal/handlers` and verified all tests pass.

### Impact
- **Security**: No negative impact. The status is public information relative to the authenticated user.
- **Performance**: Adds one lightweight database read to the `/api/auth/me` endpoint, which is acceptable for profile fetching.
