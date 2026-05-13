# ADR-001: 2FA System Architecture

**Date**: 2026-01-26
**Status**: ACCEPTED (Partially Superseded — 2FA 业务逻辑已迁移至 Go backend `internal/services/twofa_service.go` + `internal/handlers/twofa_handler.go`，TypeScript 层仅保留前端调用。核心决策仍有效。)
**Context**: Implementing two-factor authentication (2FA) using TOTP and backup codes

## Problem Statement

Secure user accounts with time-based one-time passwords (TOTP) and recovery codes while maintaining:
- Clean separation between TypeScript proxy layer and Go backend
- High cohesion, low coupling architecture
- 100% test coverage
- No side effects on existing features

## Decision

Implement a distributed 2FA system with:
1. **Go Backend**: Business logic (TOTP generation, token verification)
2. **TypeScript API Proxy**: Request validation, session management, response transformation
3. **PostgreSQL**: Persistent session storage and user configuration
4. **Encryption**: AES-256-GCM for secrets and backup codes

### Session Management Strategy

**Chosen**: PostgreSQL table (`pending_login_sessions`) over Redis
- **Rationale**:
  - Better audit trail for login attempts
  - Persistent across server restarts
  - Queryable for administrative purposes
  - Aligns with existing PostgreSQL-first architecture
  - Single source of truth in relational database

**Table Structure**:
```sql
pending_login_sessions (
  id SERIAL PRIMARY KEY,
  session_id TEXT UNIQUE NOT NULL,       -- UUID for frontend
  user_id INTEGER NOT NULL REFERENCES users(id),
  created_at TIMESTAMP DEFAULT NOW(),
  expires_at TIMESTAMP NOT NULL           -- 15-minute TTL
)
```

### Authentication Flow (with 2FA)

```
1. User submits email + password to POST /api/auth/login
        ↓
2. Go backend validates credentials
        ↓
3. Go backend checks if user has 2FA enabled
        ├─ No 2FA → return { success: true, token, user }
        │
        └─ 2FA enabled → return { requires2FA: true, sessionId, user }
        ↓
4. Frontend detects requires2FA flag
        ├─ No 2FA → store token, navigate to dashboard
        │
        └─ 2FA required → show verification page with sessionId
        ↓
5. User scans TOTP code or enters backup code
        ↓
6. Frontend calls POST /api/auth/2fa/verify-login (sessionId + code)
        ↓
7. TypeScript endpoint:
   - Validates sessionId (exists and not expired)
   - Calls TwoFactorService.verify()
   - Clears session
   - Returns JWT token
```

### Component Responsibilities

#### TwoFactorService (`src/lib/two-factor-service.ts`)
**Owns**: All cryptographic operations
- `setup()`: Generate secret, QR code, backup codes
- `enable()`: Validate TOTP and activate
- `disable()`: Validate TOTP and deactivate
- `verify()`: Validate TOTP or backup code (one-time use)
- `getStatus()`: Return 2FA configuration

**Encryption**: AES-256-GCM via `encryption.ts` utility
- Secrets encrypted before storage
- Backup codes encrypted as JSON array
- Decryption happens in-memory only

#### SessionService (`src/lib/session-service.ts`)
**Owns**: Temporary login session lifecycle
- `createPendingLoginSession()`: Generate UUID + store in DB
- `getPendingLoginSession()`: Retrieve + validate TTL
- `clearPendingLoginSession()`: Delete after use
- `cleanupExpiredSessions()`: Periodic maintenance

**Design**: Dependency injection of db client, no hardcoded dependencies

#### API Endpoints (`api/auth/2fa/*`)
**Owns**: HTTP request/response handling
- `setup.ts`: Authentication + TwoFactorService.setup()
- `enable.ts`: Validation + TwoFactorService.enable()
- `disable.ts`: Validation + TwoFactorService.disable()
- `verify-login.ts`: SessionService + TwoFactorService.verify() + JWT generation
- `status.ts`: TwoFactorService.getStatus()
- `login.ts`: Go proxy + LoginResponseSchema validation

**Design**: Pure HTTP handlers, no business logic duplication

## Trade-offs

### PostgreSQL vs Redis for Sessions

| Aspect | PostgreSQL | Redis |
|--------|------------|-------|
| Persistence | ✅ Survives restarts | ❌ Volatile |
| Audit trail | ✅ Full history queryable | ❌ No history |
| Performance | ⚠️ Slower (~10ms) | ✅ Very fast (<1ms) |
| Scalability | ⚠️ Scales vertically | ✅ Scales horizontally |
| Operational complexity | ✅ Simpler (uses existing DB) | ⚠️ Requires Redis ops |

**Decision Rationale**: Audit trail and persistence more important than sub-10ms latency for login flow

### Backup Code Format

**Chosen**: 8-character uppercase hexadecimal (e.g., `ABCD1234`)
- **vs 10-digit decimal** (`9476523821`): Hex is more scannable
- **vs 8-word passphrase** (`correct-horse-battery-staple`): More compact
- **Length**: 4 bytes hex = 16 bits entropy per code (adequate for backup use)

### One-Time Backup Code Enforcement

**Mechanism**: Remove code from array after use
- **vs counter-based**: Simpler, no state corruption possible
- **vs signature-based**: No verification overhead
- **Idempotent**: Safe to retry on network errors

## Decisions Made

### 1. Go Backend Detects 2FA Requirement (Option B)
- ✅ No business logic duplication
- ✅ Single source of truth in Go
- ✅ Clean TypeScript proxy layer
- ❌ Frontend must handle 2FA flow logic

**Alternative Rejected**: TypeScript endpoint checks user.twoFactorEnabled
- Would duplicate 2FA-enabled check logic
- Violates backend-as-source-of-truth pattern

### 2. Separate verify-login Endpoint (not in login)
- ✅ Concerns separation (login vs 2FA)
- ✅ Stateless login endpoint
- ✅ Can be rate-limited independently
- ❌ Requires multi-step frontend flow

**Alternative Rejected**: Include 2FA in login endpoint
- Creates statefull endpoint (unexpected)
- Mixed responsibilities (auth + 2FA)

### 3. UUID for Session IDs
- ✅ Cryptographically random
- ✅ Human-readable in logs
- ✅ Standard format (can parse if needed)
- ❌ Slightly longer than numeric ID

### 4. 15-Minute Session TTL
- ✅ Long enough for user input delays
- ✅ Short enough for security (limits brute force window)
- ✅ Standard for temporary auth tokens
- ❌ Could be configurable per user

## Consequences

### Positive
- **Security**: Multi-layer validation (API schema + service layer + DB constraints)
- **Auditability**: All sessions stored in queryable database
- **Testability**: Each component tested independently with mocks
- **Maintainability**: Clear responsibilities (service vs endpoint vs session)
- **Scalability**: No dependency on in-memory state or external services

### Negative
- **Latency**: ~10-20ms added per session lookup (vs <1ms Redis)
- **Database Load**: Every 2FA login creates/deletes session row
- **Complexity**: Requires database schema migration + cleanup logic
- **Flexibility**: Hard to share sessions across service instances (must coordinate cleanup)

## Implementation Status

- ✅ Database schema (pendingLoginSessions table)
- ✅ Database migration (006_create_pending_login_sessions.go)
- ✅ SessionService implementation
- ✅ verify-login endpoint implementation
- ✅ LoginResponseSchema for response validation
- ✅ Comprehensive test coverage (81+ tests)
- ✅ JSDoc documentation
- ⏳ Go backend integration (separate work item)
- ⏳ Frontend 2FA verification UI (separate work item)

## Related Decisions

- **ADR-002**: Backup code generation and one-time use enforcement
- **ADR-003**: TOTP token validation with otplib library
- **Security**: All secrets encrypted with AES-256-GCM before storage

## Validation Checklist

- [x] KISS principle: Simple, focused implementations
- [x] High cohesion: Each service handles one domain
- [x] Low coupling: Dependency injection, no hardcoded dependencies
- [x] 100% test coverage: 81+ tests on core components
- [x] No side effects: Existing features unaffected
- [x] Security: Proper encryption, validation, error handling
- [x] Documentation: JSDoc, this ADR, inline comments
- [x] Scalability: Stateless endpoints, queryable sessions

## References

- openspec/2fa-system.md - Original 2FA specification
- src/lib/two-factor-service.ts - Business logic implementation
- src/lib/session-service.ts - Session management
- api/auth/2fa/verify-login.ts - Login verification endpoint
- src/lib/two-factor-schemas.ts - Zod validation schemas
