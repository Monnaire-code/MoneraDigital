#!/bin/bash
# scripts/db-promote/02-promote.sh
#
# Apply fund_reports migration 049. IDEMPOTENT — safe to re-run.
# The underlying Go Migrator skips already-applied versions and the
# migration itself uses CREATE TABLE IF NOT EXISTS + INSERT ... ON
# CONFLICT DO NOTHING, so this is safe to run on a partially-applied DB.
#
# Usage:
#   bash scripts/db-promote/02-promote.sh              # dev (uses .env)
#   ENV_FILE=.env.prod bash scripts/db-promote/02-promote.sh
#   bash scripts/db-promote/02-promote.sh --env-file /opt/monera/.env
#
# Always run 01-preflight.sh first. This script does not enforce that,
# but skipping preflight removes your safety net.
#
# Exit codes:
#   0  migration ran (or was a no-op for already-applied 049)
#   1  migrator failed

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

echo "=== promote — fund_reports migration 049 ==="
echo "  project: ${PROJECT_ROOT}"
echo "  cwd:     $(pwd)"
echo ""
echo "  \033[33mWARN\033[0m this writes to the database. Confirm before continuing."
echo ""
printf "Type 'yes' to continue (any other input aborts): "
read -r ans
if [[ "$ans" != "yes" ]]; then
    echo "Aborted."
    exit 1
fi

cd "${PROJECT_ROOT}"
go run scripts/db-promote/inspector/main.go apply "$@"

echo ""
echo "Run \`bash scripts/db-promote/03-verify.sh\` to confirm the post-state."
