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
