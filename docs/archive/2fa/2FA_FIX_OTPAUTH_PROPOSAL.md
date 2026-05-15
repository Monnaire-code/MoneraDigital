# 2FA Fix Proposal: Missing otpauth URL

## Issue
Users are unable to enable 2FA on the security dashboard. The frontend throws an error: `Server response missing otpauth URL`.
This is caused by the backend `Setup2FA` handler not including the `otpauth` field in the JSON response, although it is available in the service response.

## Analysis
- **Frontend**: `src/pages/dashboard/Security.tsx` explicitly checks for `payload.otpauth` and throws an error if missing.
- **Backend Service**: `internal/services/twofa_service.go` returns `SetupResponse` which contains `OTPAuth` field.
- **Backend Handler**: `internal/handlers/twofa_handler.go` maps service response to a JSON object but omits `otpauth`.

## Proposal
Update `internal/handlers/twofa_handler.go` to include `"otpauth": setup.OTPAuth` in the response map.

## Implementation Plan
1.  **Test**: Add a test case in `internal/handlers/twofa_handler_test.go` to verify `otpauth` is returned.
    - Since we cannot easily mock the service (struct dependency), we will rely on checking the handler code change or try to mock the service if possible.
    - Actually, `TwoFAHandler` depends on `*services.TwoFactorService`. We can't easily mock methods of a struct pointer in Go without an interface.
    - However, we can modify `TwoFAHandler` to use an interface for the service, or we can just apply the fix and verify with manual testing or integration test.
    - **Refactoring for Testability**: To strictly follow "100% test coverage" and "High Cohesion, Low Coupling", we should refactor `TwoFAHandler` to depend on an interface `TwoFAServiceInterface` instead of the concrete struct.
    
    *Correction*: Refactoring to interface might be too big of a change given the time constraints and "KISS". The project uses struct pointers for services.
    
    *Alternative Test Strategy*: I will add a test that mocks the `TwoFactorService` behavior if possible, or I will use the existing `test-2fa-routes.mjs` to verify the fix end-to-end.
    
    *Refined Plan*:
    1.  Refactor `TwoFAHandler` to use an interface `TwoFactorProvider`? No, that breaks existing DI.
    2.  Just fix the handler.
    3.  Update `test-2fa-routes.mjs` to explicitly check for `otpauth` field in the response of `Setup 2FA`.

2.  **Fix**: Modify `internal/handlers/twofa_handler.go`.

3.  **Verify**: Run `test-2fa-routes.mjs`.

## Design Principles Checklist
- [x] **KISS**: Simple field addition.
- [x] **High Cohesion**: Handler is responsible for mapping response.
- [x] **Test Coverage**: Integration test will cover it.
