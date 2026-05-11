# Proposal: Fix Withdrawal Address Success Message Internationalization

## Problem
The success message "Address added successfully. Please verify it before first withdrawal." is hardcoded in English in the frontend code.
This causes users of the Chinese version (and other potential languages) to see the message in English instead of their preferred language.

## Root Cause
In `src/pages/dashboard/Withdraw.tsx` and `src/pages/dashboard/Addresses.tsx`, the `toast.success` call uses a hardcoded string literal instead of a translation key via `i18next`.

## Proposed Solution
1.  Add a new translation key `addresses.addSuccessMessage` to `src/i18n/locales/en.json` and `src/i18n/locales/zh.json`.
2.  Update `src/pages/dashboard/Withdraw.tsx` to use `t("addresses.addSuccessMessage")`.
3.  Update `src/pages/dashboard/Addresses.tsx` to use `t("addresses.addSuccessMessage")` (or similar key if the context slightly differs).

## Implementation Details

### Translation Keys

**File:** `src/i18n/locales/en.json`
```json
"addresses": {
  ...
  "addSuccessMessage": "Address added successfully. Please verify it before first withdrawal."
}
```

**File:** `src/i18n/locales/zh.json`
```json
"addresses": {
  ...
  "addSuccessMessage": "地址添加成功。请在首次提现前完成验证。"
}
```

### Code Changes

**File:** `src/pages/dashboard/Withdraw.tsx`
```typescript
// Before
toast.success("Address added successfully. Please verify it before first withdrawal.");

// After
toast.success(t("addresses.addSuccessMessage"));
```

**File:** `src/pages/dashboard/Addresses.tsx`
```typescript
// Before
toast.success(data.message || "Address added successfully");

// After
toast.success(data.message || t("addresses.addSuccessMessage"));
```

## Testing Plan
1.  **Unit Test**: Create a test case in `tests/i18n.spec.ts` (or similar) that mocks the translation and asserts that the `toast.success` is called with the correct key/translated string.
2.  **Manual Verification**:
    *   Switch language to Chinese.
    *   Add a withdrawal address.
    *   Verify the toast message is in Chinese.

## Design Principles Checklist
- [x] **KISS**: Using standard i18n pattern.
- [x] **High Cohesion**: Keeping text in translation files.
- [x] **Testing**: Adding test coverage for the fix.
- [x] **Isolation**: Changes only affect UI text display.
