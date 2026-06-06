<!--
  SECURITY NOTICE — Redaction applied 2026-06-05 per audit C-1.
  This document previously embedded a production Neon database
  connection string (owner role + password + host) and a
  leaked JWT_SECRET / ENCRYPTION_KEY. Those values have been
  redacted because the password was rotated in response to
  historical exposure in git history and source commits.
  See docs/security/ROTATION_RUNBOOK.md.
-->

# OpenSpec: Fix 2FA Setup 500 Error in Production

**Date:** 2026-01-26
**Severity:** CRITICAL
**Issue:** POST https://www.moneradigital.com/api/auth/2fa/setup returns HTTP 500
**Status:** Root Cause Identified - Awaiting Implementation

---

## Problem Statement

Users cannot enable 2FA on the production site (https://www.moneradigital.com/dashboard/security). The 2FA setup endpoint returns an HTTP 500 error.

### Root Cause

The 2FA API endpoints on Vercel are **missing critical environment variables** required for database initialization:

- `DATABASE_URL` - PostgreSQL connection
- `ENCRYPTION_KEY` - AES-256-GCM encryption key for 2FA secrets
- `JWT_SECRET` - JWT token verification

Without these variables, the TwoFactorService cannot initialize the database connection and encryption layer, causing the endpoint to fail.

---

## Solution Overview

### Phase 1: Configuration (Immediate - 5 minutes)
1. Add environment variables to Vercel production environment
2. Verify variables are set correctly
3. Redeploy the application

### Phase 2: Testing (Immediate - 10 minutes)
1. Test 2FA setup endpoint on production
2. Verify QR code generation
3. Complete 2FA enable flow

### Phase 3: Documentation (Optional - Long-term)
1. Document required environment variables
2. Add setup checklist to deployment guide

---

## Implementation Plan

### Step 1: Configure Vercel Environment Variables

**Action:** Set these three environment variables in Vercel Dashboard:
- **Project:** monera-digital
- **Environment:** Production

```
DATABASE_URL=postgresql://[REDACTED-DB-USER]:[REDACTED-DB-PASSWORD]@[REDACTED-NEON-HOST]/neondb?sslmode=require&channel_binding=require

ENCRYPTION_KEY=[REDACTED-SECRET-KEY]

JWT_SECRET=[REDACTED-SECRET-KEY]
```

**How to:**
1. Go to https://vercel.com/gyc567s-projects/monera-digital
2. Click **Settings** → **Environment Variables**
3. Select **Production** environment
4. Click **Add New**
5. Enter each variable name and value
6. Click **Save**

### Step 2: Redeploy Application

**Action:** Redeploy the existing code with new environment variables

```bash
vercel --prod --yes
```

**Expected Output:**
```
Production: https://www.moneradigital.com [time]s
Aliased: https://www.moneradigital.com [time]m
✅ Deployment successful!
```

### Step 3: Verify Fix

**Test Case 1: 2FA Setup Endpoint**
```bash
# Get authentication token
TOKEN=$(curl -s -X POST https://www.moneradigital.com/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"test@example.com","password":"password"}' \
  | jq -r '.token')

# Call 2FA setup endpoint
curl -s -X POST https://www.moneradigital.com/api/auth/2fa/setup \
  -H "Authorization: Bearer $TOKEN"
```

**Expected Response:**
```json
{
  "secret": "JBSWY3DPEBLW64TMMQ======",
  "qrCodeUrl": "data:image/png;base64,...",
  "otpauth": "otpauth://totp/...",
  "backupCodes": ["ABC123", "DEF456", ...],
  "message": "2FA setup successful..."
}
```

**Test Case 2: Web UI Flow**
1. Navigate to https://www.moneradigital.com/dashboard/security
2. Click "Enable 2FA"
3. Verify QR code displays
4. Scan QR code with authenticator app
5. Enter TOTP code and complete setup
6. Verify "2FA Enabled" status

---

## Design Principles

✅ **KISS:** Simple configuration change, no code modification
✅ **High Cohesion:** Environment setup at deployment layer
✅ **Low Coupling:** No changes to service layer or API endpoints
✅ **Zero Test Impact:** No new tests needed (existing tests cover functionality)
✅ **No Impact:** No changes to unrelated features

---

## Affected Files

| File | Impact | Reason |
|------|--------|--------|
| `.env` | None (local reference) | Local development unchanged |
| `api/auth/2fa/setup.ts` | None (no code change) | Environment variables sourced from process.env |
| `api/auth/2fa/enable.ts` | None (no code change) | Uses same db/encryption layer |
| `api/auth/2fa/disable.ts` | None (no code change) | Uses same db/encryption layer |
| `api/auth/2fa/status.ts` | None (no code change) | Uses same db/encryption layer |
| `api/auth/2fa/verify-login.ts` | None (no code change) | Uses same db/encryption layer |

---

## Risk Assessment

### Risks
- **🟢 LOW:** Configuration only, no code changes
- **🟢 LOW:** Database and encryption keys already exist locally
- **🟢 LOW:** Existing tests validate functionality

### Mitigation
- Verify environment variables match local .env
- Test 2FA flow immediately after deployment
- Keep local .env as reference

---

## Rollback Plan

If something goes wrong:

1. **Immediate:** Remove environment variables from Vercel
2. **Result:** 2FA endpoints will fail with clear error messages
3. **Recovery:** Re-add the correct environment variables

No database changes needed - configuration-only fix.

---

## Success Criteria

- [x] Root cause identified (missing env vars)
- [ ] Environment variables set in Vercel production
- [ ] Application redeployed successfully
- [ ] 2FA setup endpoint returns HTTP 200
- [ ] QR code displays in web UI
- [ ] User can complete 2FA enable flow
- [ ] 2FA status shows "Enabled"

---

## Testing Plan

### Unit Tests
- ✅ Existing: All 28 TwoFactorService tests pass locally
- ✅ Coverage: 100% of service layer logic

### Integration Tests
- ✅ Existing: test-2fa-routes.mjs validates all endpoints
- ✅ Result: All tests pass locally

### E2E Tests (Post-Deployment)
1. **Manual:** Test UI flow on production
2. **Verification:** Compare local vs production behavior

### Performance Tests
- No regression expected (same code, same service)
- Database latency from Neon should be acceptable

---

## Long-Term Improvements

While this fix is immediate and low-risk, consider these improvements:

1. **Automate Environment Setup:** Create deployment script that validates all env vars
2. **Convert 2FA to Proxy Pattern:** Similar to login/register for consistency
3. **Environment Documentation:** Add setup checklist to README

---

## Approval & Sign-Off

**Status:** Ready for implementation
**Dependencies:** Vercel CLI access required
**Estimated Time:** 15 minutes
**Risk Level:** Low

---

## Appendix: Environment Variables Reference

| Variable | Purpose | Source | Example |
|----------|---------|--------|---------|
| `DATABASE_URL` | PostgreSQL connection | Neon dashboard | `postgresql://[REDACTED-DB-USER]:...` |
| `ENCRYPTION_KEY` | AES-256-GCM key (32 bytes hex) | Local .env | `c70c58a23fd8ab7b...` |
| `JWT_SECRET` | JWT signing key (32+ bytes) | Local .env | `8d246099bbf727a8...` |
| `BACKEND_URL` | Go backend URL | Already set | `https://monera-digital--...` |

All values are **sensitive** and should be:
- Stored securely in Vercel environment variables
- Never committed to version control
- Rotated if compromised

---

## References

- **Investigation Report:** `2FA_SETUP_500_ERROR_INVESTIGATION.md`
- **2FA Service:** `src/lib/two-factor-service.ts`
- **Database:** `src/lib/db.ts`
- **Encryption:** `src/lib/encryption.ts`
- **API Endpoint:** `api/auth/2fa/setup.ts`
