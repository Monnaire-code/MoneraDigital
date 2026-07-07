#!/bin/bash
# scripts/db-promote/04-rollback.sh
#
# DESTRUCTIVE: drops fund_reports and fund_asset_allocations, and
# unregisters migration 049. Use only if you need to remove the feature.
#
# Confirmation gate: requires CONFIRM_ROLLBACK=yes env var AND an
# interactive "yes" prompt.
#
# Usage:
#   CONFIRM_ROLLBACK=yes bash scripts/db-promote/04-rollback.sh
#   CONFIRM_ROLLBACK=yes ENV_FILE=.env.prod bash scripts/db-promote/04-rollback.sh
#
# Exit codes:
#   0  rollback succeeded
#   1  rollback failed
#   2  confirmation missing (no CONFIRM_ROLLBACK=yes)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

echo "=== rollback — fund_reports migration 049 ==="
echo "  \033[31mDESTRUCTIVE\033[0m this will DROP both fund tables and unregister 049"
echo ""

if [[ "${CONFIRM_ROLLBACK:-}" != "yes" ]]; then
    echo "  set CONFIRM_ROLLBACK=yes to enable"
    echo "  e.g. CONFIRM_ROLLBACK=yes bash scripts/db-promote/04-rollback.sh"
    exit 2
fi

printf "Type 'rollback' to confirm: "
read -r ans
if [[ "$ans" != "rollback" ]]; then
    echo "Aborted."
    exit 1
fi

cd "${PROJECT_ROOT}"
exec go run scripts/db-promote/inspector/main.go rollback "$@"
