-- Migration: 00046_add_pending_status_and_activation_fields
-- Description: Add PENDING status, activation fields, and rate limits table
-- Date: 2026-04-20

-- 1. Add PENDING status to user_status enum
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_enum WHERE enumlabel = 'PENDING') THEN
        ALTER TYPE user_status ADD VALUE IF NOT EXISTS 'PENDING';
    END IF;
END $$;

-- 2. Add activation-related columns to users table
ALTER TABLE users 
  ADD COLUMN IF NOT EXISTS activation_code VARCHAR(255),
  ADD COLUMN IF NOT EXISTS activation_attempts INT DEFAULT 0,
  ADD COLUMN IF NOT EXISTS activation_expires_at TIMESTAMP,
  ADD COLUMN IF NOT EXISTS activated_at TIMESTAMP;

-- 3. Create rate_limits table for rate limiting
CREATE TABLE IF NOT EXISTS rate_limits (
    id SERIAL PRIMARY KEY,
    key_type VARCHAR(50) NOT NULL,
    key_value VARCHAR(255) NOT NULL,
    action VARCHAR(50) NOT NULL,
    attempt_count INT DEFAULT 1,
    window_start TIMESTAMP NOT NULL DEFAULT NOW(),
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),
    UNIQUE(key_type, key_value, action)
);

-- 4. Create indexes for performance
CREATE INDEX IF NOT EXISTS idx_rate_limits_key ON rate_limits(key_type, key_value);
CREATE INDEX IF NOT EXISTS idx_users_activation_code ON users(activation_code) 
  WHERE activation_code IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_users_activation_expires ON users(activation_expires_at)
  WHERE activation_expires_at IS NOT NULL;

-- 5. Create migration tracking table (if not exists)
CREATE TABLE IF NOT EXISTS schema_migrations (
    id SERIAL PRIMARY KEY,
    version VARCHAR(255) NOT NULL UNIQUE,
    applied_at TIMESTAMP DEFAULT NOW(),
    description TEXT
);

-- 6. Record this migration
INSERT INTO schema_migrations (version, description)
VALUES ('00046', 'Add PENDING status and activation fields')
ON CONFLICT (version) DO NOTHING;
