/*
  ============================================================================
  WARNING — DESTRUCTIVE ONE-OFF OPS SCRIPT (C-3 audit, 2026-06-05)
  ============================================================================
  This file is NOT a migration. It must NEVER be registered in
  cmd/migrate/main.go and must NEVER be applied by the Go migrator.

  It is a one-time data fix that was originally intended to be run
  manually via psql on 2026-04-20 to reset ACTIVE users to PENDING
  status for the activation flow. It performs a mass UPDATE on the
  users table and is destructive (clears activation_code, expires_at,
  and activated_at columns).

  Historical state:
    - Originally lived in internal/migration/migrations/ as a .sql
      migration sibling to 00046. Neither the .sql nor the Go
      migrator ever executed it (the Go migrator's entry point
      was build-ignored at the time).
    - Carried its own schema_migrations INSERT, which would have
      created a parallel tracking table. That parallel tracking was
      the C-2 audit issue. The new Go migration 046 replaces
      00046's schema work; this 00047 script is preserved here
      strictly as a paper trail.

  If you ever need to re-apply this reset:
    1. Take a fresh pg_dump of the current state.
    2. Confirm with the product owner that mass-resetting ACTIVE
       users is the intent.
    3. Connect via psql with the rotated credentials from your
       password manager (never paste the DSN into a tracked file).
    4. Run this file by hand.
    5. Do NOT add this to cmd/migrate/main.go's registerMigrations.
  ============================================================================
*/

-- Migration: 00047_activate_existing_users
-- Description: Reset all existing ACTIVE users to PENDING status
-- Date: 2026-04-20

-- Reset all existing users to PENDING status for activation flow
UPDATE users
SET status = 'PENDING',
    activation_code = NULL,
    activation_attempts = 0,
    activation_expires_at = NULL,
    activated_at = NULL,
    updated_at = NOW()
WHERE status = 'ACTIVE';

-- Record this migration
INSERT INTO schema_migrations (version, description)
VALUES ('00047', 'Reset existing users to PENDING for activation')
ON CONFLICT (version) DO NOTHING;
