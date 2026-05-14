-- ====================================================================
-- Drop Wrong Table: wallet_creation_request
-- This table was mistakenly created with wrong name (singular instead of plural)
-- ====================================================================

-- Drop the wrong table (if exists)
DROP TABLE IF EXISTS wallet_creation_request CASCADE;

-- Drop associated indexes if they exist
DROP INDEX IF EXISTS idx_wallet_creation_request_user_id;
DROP INDEX IF EXISTS uk_wallet_creation_request_user_id;
DROP INDEX IF EXISTS uk_wallet_creation_request_request_id;
DROP INDEX IF EXISTS idx_wallet_requests_user_product_currency;

-- Verify the correct table exists
SELECT table_name FROM information_schema.tables 
WHERE table_schema = 'public' AND table_name = 'wallet_creation_requests';
