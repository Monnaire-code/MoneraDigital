#!/bin/bash
# scripts/db-promote/01-preflight.sh
#
# Read-only pre-deploy check for the fund_reports migration 049.
# Verifies env, DB reachability, migrations table, source migration file.
# Does NOT modify anything. Safe to run multiple times.
#
# Usage:
#   bash scripts/db-promote/01-preflight.sh
#   ENV_FILE=.env.prod bash scripts/db-promote/01-preflight.sh
#
# Exit codes:
#   0  preflight passed; safe to apply
#   1  preflight failed; do NOT apply

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

echo "=== preflight — fund_reports migration 049 ==="
echo "  project: ${PROJECT_ROOT}"
echo "  cwd:     $(pwd)"
echo ""

cd "${PROJECT_ROOT}"
exec go run scripts/db-promote/inspector/main.go preflight "$@"
