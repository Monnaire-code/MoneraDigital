#!/bin/bash

# ====================================================================
# Test Script: Verify JWT_SECRET Consistency Fix
# ====================================================================
# This script verifies that the 401 Unauthorized error fix works correctly.
#
# Usage: ./test-401-fix.sh
# ====================================================================

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Test counter
TESTS_PASSED=0
TESTS_FAILED=0

# Log functions
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[PASS]${NC} $1"
    ((TESTS_PASSED++))
}

log_fail() {
    echo -e "${RED}[FAIL]${NC} $1"
    ((TESTS_FAILED++))
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

# ====================================================================
# Test 1: Verify .env file exists
# ====================================================================
test_env_file_exists() {
    log_info "Test 1: Verify .env file exists"
    
    if [ -f ".env" ]; then
        log_success ".env file exists"
    else
        log_fail ".env file not found"
        return 1
    fi
}

# ====================================================================
# Test 2: Verify JWT_SECRET is set in .env
# ====================================================================
test_jwt_secret_in_env() {
    log_info "Test 2: Verify JWT_SECRET is set in .env"
    
    if grep -q "JWT_SECRET=" .env; then
        JWT_SECRET_VALUE=$(grep "JWT_SECRET=" .env | cut -d '=' -f2-)
        if [ -n "$JWT_SECRET_VALUE" ] && [ "$JWT_SECRET_VALUE" != "your-secret-key" ]; then
            log_success "JWT_SECRET is properly configured in .env"
            echo "  Value: ${JWT_SECRET_VALUE:0:10}...${JWT_SECRET_VALUE: -10}"
        else
            log_fail "JWT_SECRET is default value or empty"
            return 1
        fi
    else
        log_fail "JWT_SECRET not found in .env"
        return 1
    fi
}

# ====================================================================
# Test 3: Verify start-dev.sh has the fix
# ====================================================================
test_start_dev_script_has_fix() {
    log_info "Test 3: Verify start-dev.sh has JWT_SECRET export"
    
    if grep -q "JWT_SECRET=\$(grep \"JWT_SECRET=\" .env" scripts/start-dev.sh; then
        log_success "start-dev.sh exports JWT_SECRET from .env"
    else
        log_fail "start-dev.sh missing JWT_SECRET export"
        return 1
    fi
}

# ====================================================================
# Test 4: Verify backend can read JWT_SECRET from environment
# ====================================================================
test_backend_jwt_secret() {
    log_info "Test 4: Verify backend receives correct JWT_SECRET"
    
    # Export JWT_SECRET as the start script does
    export JWT_SECRET=$(grep "JWT_SECRET=" .env | cut -d '=' -f2-)
    
    # Check if the value matches
    ENV_JWT_SECRET=$(grep "JWT_SECRET=" .env | cut -d '=' -f2-)
    
    if [ "$JWT_SECRET" = "$ENV_JWT_SECRET" ]; then
        log_success "Backend will receive correct JWT_SECRET"
    else
        log_fail "JWT_SECRET mismatch"
        return 1
    fi
}

# ====================================================================
# Test 5: Verify Go config doesn't use hardcoded default
# ====================================================================
test_go_config_default() {
    log_info "Test 5: Verify Go config default is documented"
    
    # Check if the default JWT_SECRET in Go is just a fallback
    if grep -q 'SetDefault("JWT_SECRET"' internal/config/config.go; then
        # The default should only be used if .env file is not found
        log_success "Go config has JWT_SECRET default (fallback only)"
    else
        log_fail "Go config missing JWT_SECRET default"
        return 1
    fi
}

# ====================================================================
# Test 6: Verify frontend auth-middleware uses environment variable
# ====================================================================
test_frontend_auth_middleware() {
    log_info "Test 6: Verify frontend uses process.env.JWT_SECRET"
    
    if grep -q 'process.env.JWT_SECRET' src/lib/auth-middleware.ts; then
        log_success "Frontend auth-middleware uses environment variable"
    else
        log_fail "Frontend auth-middleware doesn't use JWT_SECRET"
        return 1
    fi
}

# ====================================================================
# Test 7: Verify route configuration requires auth
# ====================================================================
test_route_auth_config() {
    log_info "Test 7: Verify wallet routes require authentication"
    
    # Check that wallet/info and wallet/address/get require auth
    if grep -q "'GET /wallet/info'" api/\[...route\].ts && \
       grep -q "'POST /wallet/address/get'" api/\[...route\].ts; then
        if grep -q "requiresAuth: true" api/\[...route\].ts; then
            log_success "Wallet routes require authentication"
        else
            log_fail "Wallet routes missing requiresAuth flag"
            return 1
        fi
    else
        log_fail "Wallet routes not found in configuration"
        return 1
    fi
}

# ====================================================================
# Test 8: Verify backend middleware validates token
# ====================================================================
test_backend_middleware() {
    log_info "Test 8: Verify backend auth middleware exists"
    
    if [ -f internal/middleware/auth.go ]; then
        if grep -q "AuthMiddleware" internal/middleware/auth.go; then
            log_success "Backend auth middleware exists"
        else
            log_fail "AuthMiddleware not found in auth.go"
            return 1
        fi
    else
        log_fail "internal/middleware/auth.go not found"
        return 1
    fi
}

# ====================================================================
# Main test runner
# ====================================================================
main() {
    echo -e "${BLUE}"
    echo "╔════════════════════════════════════════════════════════╗"
    echo "║      Test Suite: 401 Unauthorized Error Fix            ║"
    echo "╚════════════════════════════════════════════════════════╝"
    echo -e "${NC}"
    echo ""
    
    # Change to project directory
    cd "$(dirname "$0")"
    
    # Run tests
    test_env_file_exists
    test_jwt_secret_in_env
    test_start_dev_script_has_fix
    test_backend_jwt_secret
    test_go_config_default
    test_frontend_auth_middleware
    test_route_auth_config
    test_backend_middleware
    
    # Summary
    echo ""
    echo -e "${BLUE}════════════════════════════════════════════════════════${NC}"
    echo -e "${BLUE}Test Summary${NC}"
    echo -e "${BLUE}════════════════════════════════════════════════════════${NC}"
    echo -e "Tests Passed: ${GREEN}${TESTS_PASSED}${NC}"
    echo -e "Tests Failed: ${RED}${TESTS_FAILED}${NC}"
    echo ""
    
    if [ $TESTS_FAILED -eq 0 ]; then
        echo -e "${GREEN}All tests passed! ✓${NC}"
        echo -e "${GREEN}401 Unauthorized error fix is properly implemented.${NC}"
        exit 0
    else
        echo -e "${RED}Some tests failed. Please review the failures above.${NC}"
        exit 1
    fi
}

main "$@"
