#!/bin/bash
# ====================================================================
# Neon Database Update Script
# Usage: ./update_database.sh
# ====================================================================

set -e  # Exit on error

echo "=========================================="
echo "Neon Database Update Script"
echo "Date: $(date)"
echo "=========================================="

# Check if DATABASE_URL is set
if [ -z "$DATABASE_URL" ]; then
    echo "❌ Error: DATABASE_URL environment variable is not set"
    echo ""
    echo "Please set it first:"
    echo "export DATABASE_URL='postgresql://...'"
    exit 1
fi

echo "✅ DATABASE_URL is set"

# Test connection
echo ""
echo "Testing database connection..."
if psql "$DATABASE_URL" -c "SELECT version();" > /dev/null 2>&1; then
    echo "✅ Database connection successful"
else
    echo "❌ Failed to connect to database"
    exit 1
fi

# Show current schema version (if migrations table exists)
echo ""
echo "Checking current migration status..."
psql "$DATABASE_URL" -c "
SELECT EXISTS (
    SELECT FROM information_schema.tables 
    WHERE table_name = 'migrations'
);" 2>/dev/null | grep -q "t" && {
    echo "Current applied migrations:"
    psql "$DATABASE_URL" -c "SELECT version, name, executed_at FROM migrations ORDER BY version;" 2>/dev/null || echo "Could not query migrations table"
} || echo "Migrations table does not exist yet"

# Preview changes
echo ""
echo "=========================================="
echo "Preview of changes to be applied:"
echo "=========================================="
echo ""
echo "1. Users table:"
echo "   - Add two_factor_last_used_at (BIGINT)"
echo "   - Add updated_at (TIMESTAMP)"
echo "   - Create idx_users_two_factor_last_used_at index"
echo ""
echo "2. Wallet Creation Request table:"
echo "   - Add product_code (VARCHAR(50))"
echo "   - Add currency (VARCHAR(20))"
echo "   - Create idx_wallet_requests_user_product_currency index"
echo ""

# Confirm before proceeding
read -p "Do you want to proceed with the update? (yes/no): " confirm
if [ "$confirm" != "yes" ]; then
    echo "Update cancelled"
    exit 0
fi

# Execute the SQL script
echo ""
echo "Executing update script..."
if psql "$DATABASE_URL" -f scripts/update_neon_database.sql; then
    echo ""
    echo "=========================================="
    echo "✅ Database update completed successfully!"
    echo "=========================================="
else
    echo ""
    echo "=========================================="
    echo "❌ Database update failed!"
    echo "=========================================="
    exit 1
fi

# Verify the changes
echo ""
echo "Verifying changes..."
echo ""
echo "Users table columns:"
psql "$DATABASE_URL" -c "
SELECT column_name, data_type 
FROM information_schema.columns 
WHERE table_name = 'users' 
AND column_name IN ('two_factor_last_used_at', 'updated_at')
ORDER BY ordinal_position;"

echo ""
echo "Wallet creation request columns:"
psql "$DATABASE_URL" -c "
SELECT column_name, data_type 
FROM information_schema.columns 
WHERE table_name = 'wallet_creation_request' 
AND column_name IN ('product_code', 'currency')
ORDER BY ordinal_position;"

echo ""
echo "New indexes:"
psql "$DATABASE_URL" -c "
SELECT indexname 
FROM pg_indexes 
WHERE indexname IN ('idx_users_two_factor_last_used_at', 'idx_wallet_requests_user_product_currency');"

echo ""
echo "=========================================="
echo "Update verification complete!"
echo "=========================================="
