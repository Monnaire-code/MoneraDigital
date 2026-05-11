-- ====================================================================
-- Neon Database Update Script
-- Date: 2026-01-28
-- Description: Add 2FA fields and wallet creation request extensions
-- ====================================================================

-- Start transaction
BEGIN;

-- ====================================================================
-- 1. Update Users Table - Add 2FA timestamp field
-- ====================================================================

-- Add two_factor_last_used_at column for replay attack prevention
ALTER TABLE users 
ADD COLUMN IF NOT EXISTS two_factor_last_used_at BIGINT DEFAULT 0;

-- Add updated_at column if not exists
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns 
                   WHERE table_name = 'users' AND column_name = 'updated_at') THEN
        ALTER TABLE users ADD COLUMN updated_at TIMESTAMP DEFAULT NOW() NOT NULL;
    END IF;
END $$;

-- Create index for two_factor_last_used_at (conditional index for enabled users only)
CREATE INDEX IF NOT EXISTS idx_users_two_factor_last_used_at 
ON users(two_factor_last_used_at) 
WHERE two_factor_enabled = TRUE;

-- ====================================================================
-- 2. Update Wallet Creation Request Table - Add product and currency fields
-- ====================================================================

-- Add product_code column
ALTER TABLE wallet_creation_requests 
ADD COLUMN IF NOT EXISTS product_code VARCHAR(50) DEFAULT '';

-- Add currency column
ALTER TABLE wallet_creation_requests 
ADD COLUMN IF NOT EXISTS currency VARCHAR(20) DEFAULT '';

-- Create unique index for user + product + currency combination
CREATE UNIQUE INDEX IF NOT EXISTS idx_wallet_requests_user_product_currency 
ON wallet_creation_requests(user_id, product_code, currency) 
WHERE status = 'SUCCESS';

-- ====================================================================
-- 3. Add Comments for Documentation
-- ====================================================================

COMMENT ON TABLE users IS 'Core user authentication table with 2FA support';
COMMENT ON COLUMN users.two_factor_secret IS 'Encrypted TOTP secret key';
COMMENT ON COLUMN users.two_factor_enabled IS 'Whether 2FA is enabled for the user';
COMMENT ON COLUMN users.two_factor_backup_codes IS 'Encrypted backup codes for 2FA recovery';
COMMENT ON COLUMN users.two_factor_last_used_at IS 'Timestamp of last 2FA usage for replay attack prevention';

COMMENT ON TABLE wallet_creation_requests IS 'Wallet creation requests with product and currency support';
COMMENT ON COLUMN wallet_creation_requests.product_code IS 'Product code for the wallet (e.g., SPOT, EARN)';
COMMENT ON COLUMN wallet_creation_requests.currency IS 'Currency code for the wallet (e.g., USDT, BTC)';

-- ====================================================================
-- 4. Verification Queries (run manually to verify)
-- ====================================================================

-- Uncomment to verify users table structure:
-- SELECT column_name, data_type, column_default 
-- FROM information_schema.columns 
-- WHERE table_name = 'users' 
-- ORDER BY ordinal_position;

-- Uncomment to verify indexes:
-- SELECT indexname, indexdef 
-- FROM pg_indexes 
-- WHERE tablename IN ('users', 'wallet_creation_requests');

-- Commit transaction
COMMIT;

-- ====================================================================
-- END OF UPDATE SCRIPT
-- ====================================================================
