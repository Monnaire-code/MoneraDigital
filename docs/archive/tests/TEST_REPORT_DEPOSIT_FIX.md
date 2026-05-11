# Deposit i18n Fix Verification

**Status:** ✅ FIXED

## Issue
User saw `deposit.activate` instead of "Activate Account".

## Root Cause
The `deposit` translation object was accidentally nested inside the `dashboard` object in `en.json` and `zh.json`.
Usage in code: `t("deposit.activate")` (Expects root level).
Actual path: `dashboard.deposit.activate`.

## Fix
Moved `deposit` object to the root level of the JSON structure in both locale files.

## Verification
- Reloading the frontend will load the corrected JSON.
- `t("deposit.activate")` will now resolve correctly.
