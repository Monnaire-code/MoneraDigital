#!/bin/bash
# scripts/db-promote/03-verify.sh
#
# Post-deploy verification: schema, data integrity, FK checks, API smoke.
# Read-only against DB; performs a real GET /api/fund/stats if API_URL
# is set in the env (or BACKEND_URL).
#
# Usage:
#   bash scripts/db-promote/03-verify.sh
#   API_URL=http://localhost:8081 bash scripts/db-promote/03-verify.sh
#   ENV_FILE=.env.prod bash scripts/db-promote/03-verify.sh
#
# Exit codes:
#   0  all checks pass
#   1  any check failed

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

echo "=== verify — fund_reports migration 049 ==="
echo "  project: ${PROJECT_ROOT}"
echo ""

cd "${PROJECT_ROOT}"

echo "--- DB schema + data ---"
go run scripts/db-promote/inspector/main.go verify "$@"
DB_RC=$?

echo ""
echo "--- API smoke ---"
# Skip explicitly if API_URL not set — never guess localhost. (Issue #2 fix)
if [[ -z "${API_URL:-}" ]]; then
    echo "  \033[33mWARN\033[0m API_URL not set; API smoke skipped"
    echo "        set it explicitly: API_URL=https://prod-host:8081 bash scripts/db-promote/03-verify.sh"
    API_RC=0
else
    echo "  GET ${API_URL}/api/fund/stats"
    HTTP_CODE=$(curl -s -o /tmp/fund-smoke.json -w "%{http_code}" -H "Accept: application/json" "${API_URL}/api/fund/stats" || echo "000")
    if [[ "$HTTP_CODE" == "200" ]]; then
        echo "  \033[32mOK\033[0m   HTTP 200"
        python3 -c "
import json, sys
with open('/tmp/fund-smoke.json') as f: d = json.load(f)
assert d.get('success') is True, 'success != true'
c = d['data']['current']
assert c['totalAum'] > 0, 'totalAum not positive'
assert 0 <= c['actualApy'] <= 1, 'actualApy out of range'
pct = sum(a['pct'] for a in d['data']['allocations'])
assert pct == 1.0, f'allocations sum={pct} != 1.0'
print(f'  \033[32mOK\033[0m   currentAum=\${c[\"totalAum\"]:,.2f}  actualApy={c[\"actualApy\"]*100:.2f}%  trend={len(d[\"data\"][\"trend\"])}mos  alloc={len(d[\"data\"][\"allocations\"])}rows  sum_pct={pct*100:.4f}%')
assert 'note' not in d.get('data', {}), 'note key leaked into public DTO'
print('  \033[32mOK\033[0m   no \`note\` key in public response (H1 audit fix)')
"
        API_RC=0
    else
        echo "  \033[31mFAIL\033[0m HTTP $HTTP_CODE"
        head -c 200 /tmp/fund-smoke.json 2>/dev/null || true
        echo ""
        API_RC=1
    fi
fi

echo ""
if [[ $DB_RC -eq 0 && $API_RC -eq 0 ]]; then
    echo "VERIFY: PASS (DB + API)"
    exit 0
fi
echo "VERIFY: FAIL (DB=$DB_RC, API=$API_RC)"
exit 1
