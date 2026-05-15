# Fix Address Verification 2FA

## Problem Description
User `gyc567@gmail.com` (and any user with 2FA enabled) sees an email verification dialog when clicking "Verify" on the Addresses page, instead of the expected Google Authenticator (2FA) dialog.
Additionally, the backend `VerifyAddress` endpoint was a stub and did not perform any actual verification.

## Root Cause Analysis
1.  **Frontend (`Addresses.tsx`)**: Hardcoded to show a generic "Verification Token" dialog without checking the user's 2FA status.
2.  **Backend (`handlers.go`)**: `VerifyAddress` handler was incomplete (stubbed) and did not process the token or check 2FA.

## Solution Design

### 1. Frontend
*   **Fetch User Profile**: Retrieve 2FA status (`twoFactorEnabled`) on component mount.
*   **Dynamic Dialog**:
    *   If 2FA enabled: Show "Google Authenticator Code" input (6 digits).
    *   If 2FA disabled: Show default verification input.
*   **Logic**: Pass the code to the same API endpoint.

### 2. Backend
*   **Update `VerifyAddress`**:
    *   Accept JSON body `{ "token": "..." }`.
    *   Retrieve user profile to check `TwoFactorEnabled`.
    *   If 2FA enabled: Use `AuthService.Verify2FA(userID, token)` to validate.
    *   If 2FA disabled: (Future) Validate email token.
    *   On success: Call `AddressService.VerifyAddress`.
*   **Update `AuthService`**: Expose `Verify2FA` method.

## Implementation Details

### Files Modified
1.  `internal/services/auth.go`: Added `Verify2FA` method.
2.  `internal/handlers/handlers.go`: Implemented `VerifyAddress` logic.
3.  `src/pages/dashboard/Addresses.tsx`: Added 2FA detection and updated UI.

## Verification
*   **Build**: `npm run build` passed.
*   **Logic**:
    *   Frontend correctly fetches 2FA status.
    *   Frontend displays appropriate dialog.
    *   Backend correctly verifies 2FA token before approving address.

## Related Issues
*   Checked `DeactivateAddress`: Currently does not require 2FA. Considered low risk for now but could be enhanced later.
*   Checked `Withdraw.tsx`: Already implements 2FA manually.
