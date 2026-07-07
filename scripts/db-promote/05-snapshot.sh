#!/bin/bash
# scripts/db-promote/05-snapshot.sh
#
# Read-only backup of the two fund tables to a custom-format pg_dump.
# Output goes to /tmp/fund-snapshots/fund-{timestamp}.dump by default.
#
# Run BEFORE 04-rollback.sh if you want a safety net.
#
# Usage:
#   bash scripts/db-promote/05-snapshot.sh
#   bash scripts/db-promote/05-snapshot.sh --out /tmp/my-snap.dump
#   bash scripts/db-promote/05-snapshot.sh --env-file /opt/monera/.env
#
# Exit codes:
#   0  snapshot written
#   1  pg_dump failed
#   2  user aborted

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

OUT_DIR="/tmp/fund-snapshots"
OUT_FILE=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --out)        OUT_FILE="$2"; shift 2 ;;
        --env-file)   shift;                 # consumed by inspector
            ;;
        --help|-h)
            echo "Usage: bash scripts/db-promote/05-snapshot.sh [--out PATH] [--env-file PATH]"
            exit 0
            ;;
        *) echo "unknown arg: $1" >&2; exit 2 ;;
    esac
done

if [[ -z "$OUT_FILE" ]]; then
    mkdir -p "$OUT_DIR"
    OUT_FILE="$OUT_DIR/fund-$(date +%Y%m%d-%H%M%S).dump"
fi

echo "=== snapshot — fund_reports + fund_asset_allocations ==="
echo "  output:  $OUT_FILE"
echo ""
echo "  \033[33mWARN\033[0m this runs pg_dump which requires network access to the DB."
echo ""

printf "Type 'snapshot' to confirm: "
read -r ans
if [[ "$ans" != "snapshot" ]]; then
    echo "Aborted."
    exit 2
fi

cd "${PROJECT_ROOT}"

# Use the inspector's dsn subcommand so the env-loading logic stays
# in one place (avoids drift between this script and inspector).
DSN=$(go run scripts/db-promote/inspector/main.go dsn "$@" 2>/dev/null) || {
    echo "FATAL: could not resolve DATABASE_URL (load .env or pass --env-file)"
    exit 1
}

# -Fc custom format, compressed, restores cleanly with pg_restore
# -t selects only the two fund tables (skip the rest of the schema)
if pg_dump "$DSN" -Fc -t fund_reports -t fund_asset_allocations -f "$OUT_FILE"; then
    SIZE=$(du -h "$OUT_FILE" | cut -f1)
    echo ""
    echo "  \033[32mOK\033[0m   snapshot written: $OUT_FILE ($SIZE)"
    echo ""
    echo "Restore (if needed):"
    echo "  pg_restore -d \"\$DATABASE_URL\" --clean --if-exists $OUT_FILE"
    exit 0
else
    echo "FATAL: pg_dump failed"
    exit 1
fi
