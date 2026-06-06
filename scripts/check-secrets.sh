#!/usr/bin/env bash
# scripts/check-secrets.sh
#
# C-1 regression guard: fail if any historical leaked credential (or any
# pattern that looks like a hardcoded PostgreSQL connection string) reappears
# in tracked source files. Run from repo root:
#
#   bash scripts/check-secrets.sh
#
# Exits non-zero on any violation. Wire into:
#   - pre-commit hook (scripts/install-hooks.sh)
#   - CI workflow   (.github/workflows/secret-scan.yml)
#
# Add new patterns to ROTATED_LITERALS below. The generic DSN regex covers
# any new leak; the literal list is the high-confidence canary.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

ROTATED_LITERALS=(
  "npg_4zuq7JQNWFDB"
  "ep-bold-cloud-adfpuk12-pooler"
  "ep-weathered-mouse-adjd3txp-pooler"
)

HARDCODED_DSN_REGEX='(postgres|postgresql)://[A-Za-z0-9_.-]+:([^[:space:]@<>"`]+)@'

# Build the list of files to scan. Prefer git-tracked so we never pick up
# .env, node_modules, uncommitted worktrees, etc.
SCAN_FILES=()
if command -v git >/dev/null 2>&1 && git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  while IFS= read -r f; do
    [ -n "$f" ] && SCAN_FILES+=("$f")
  done < <(git ls-files \
    | grep -E '\.(go|ts|tsx|js|mjs|cjs|md|yml|yaml|sh|json|sql)$|\.env(\..*)?$' \
    | grep -vE '^(docs/security/ROTATION_RUNBOOK\.md|scripts/check-secrets\.sh)$' || true)
else
  echo "WARNING: not a git checkout; falling back to find." >&2
  while IFS= read -r f; do
    [ -n "$f" ] && SCAN_FILES+=("$f")
  done < <(find . \
    -type d \( -name .git -o -name node_modules -o -name dist -o -name .worktrees -o -name .dmux \) -prune -o \
    -type f \( -name "*.go" -o -name "*.ts" -o -name "*.tsx" -o -name "*.js" -o -name "*.mjs" -o -name "*.cjs" \
             -o -name "*.md" -o -name "*.yml" -o -name "*.yaml" -o -name "*.sh" -o -name "*.json" \
             -o -name "*.sql" -o -name ".env" -o -name ".env.*" \) -print \
    | grep -vE '(^|/)docs/security/ROTATION_RUNBOOK\.md$|(^|/)scripts/check-secrets\.sh$' || true)
fi

if [ "${#SCAN_FILES[@]}" -eq 0 ]; then
  echo "ERROR: no files to scan (empty tracked set?)." >&2
  exit 1
fi

violations=0

echo "==> Scanning ${#SCAN_FILES[@]} tracked files for rotated credential literals (C-1) ..."
for lit in "${ROTATED_LITERALS[@]}"; do
  matches=$(grep -nF "$lit" "${SCAN_FILES[@]}" 2>/dev/null || true)
  if [ -n "$matches" ]; then
    echo "FAIL: literal '$lit' found in tracked files:"
    echo "$matches" | sed 's/^/    /'
    violations=$((violations + 1))
  fi
done

echo "==> Scanning for hardcoded postgres://user:password@host DSNs ..."
matches=$(grep -nE "$HARDCODED_DSN_REGEX" "${SCAN_FILES[@]}" 2>/dev/null \
  | grep -v "\[REDACTED-" || true)
if [ -n "$matches" ]; then
  # Allow obvious dev-template placeholders: DSNs whose user or host is one of
  # the common placeholder words (user/pass/password/example/test/localhost).
  # A real leak uses the leaked `neondb_owner` user with a non-placeholder host,
  # which the literal scan above already catches; this allowlist is just for
  # help text and example commands.
  filtered=$(echo "$matches" | grep -vE '://(user|pass|password|example|test|admin|root|dbuser|monera)[:@]|@(localhost|127\.0\.0\.1|host|example\.com|monera)[:/]' || true)
  filtered=$(echo "$filtered" | grep -vE 'neondb_owner:xxx@' || true)
  if [ -n "$filtered" ]; then
    echo "FAIL: hardcoded postgres://user:password@host pattern found:"
    echo "$filtered" | sed 's/^/    /'
    violations=$((violations + 1))
  fi
fi

if [ "$violations" -gt 0 ]; then
  echo ""
  echo "==> $violations secret-leak pattern(s) detected. See docs/security/ROTATION_RUNBOOK.md."
  exit 1
fi

# --- 4. C-2 regression: migration runner integrity --------------------------
# C-2 audit found that cmd/migrate/main.go was build-ignored
# (//go:build ignore), making every Go migration a dead file. This
# block fails if that build tag reappears OR if any .sql sneaks back
# into the migrations directory OR if the registerMigrations list
# diverges from the actual .go files in the directory.

MIGRATE_MAIN="cmd/migrate/main.go"
MIGRATIONS_DIR="internal/migration/migrations"

# 4a. The runner must not be build-ignored.
if [ -f "$MIGRATE_MAIN" ] && head -3 "$MIGRATE_MAIN" | grep -q '^//go:build ignore'; then
  echo "FAIL: $MIGRATE_MAIN has //go:build ignore (C-2: makes the runner dead code)."
  violations=$((violations + 1))
fi

# 4b. The migrations directory must not contain any .sql files. Every
# schema change should be a Go migration so it can be tracked by the
# single `migrations` table and the same CI guard catches them.
if [ -d "$MIGRATIONS_DIR" ]; then
  sql_in_migrations=$(find "$MIGRATIONS_DIR" -maxdepth 1 -name '*.sql' 2>/dev/null || true)
  if [ -n "$sql_in_migrations" ]; then
    echo "FAIL: $MIGRATIONS_DIR contains .sql files (C-2: should be Go-only):"
    echo "$sql_in_migrations" | sed 's/^/    /'
    violations=$((violations + 1))
  fi
fi

# 4c. Every struct in the migrations dir should be registered in the
# runner, and vice versa. We parse both sides and diff.
if [ -f "$MIGRATE_MAIN" ] && [ -d "$MIGRATIONS_DIR" ]; then
  declared=$(grep -oE 'migrations\.[A-Z][A-Za-z0-9_]+' "$MIGRATE_MAIN" | sort -u)
  defined=$(grep -lE '^type [A-Z][A-Za-z0-9_]+ struct' "$MIGRATIONS_DIR"/*.go 2>/dev/null \
    | xargs -n1 basename -s .go \
    | sort -u)

  if [ -n "$declared" ]; then
    missing_in_runner=""
    for d in $defined; do
      # Test internal helpers in the same package often share a naming
      # pattern; we only check the ones that LOOK like Migration impls
      # (i.e., they have a Version() method).
      if grep -q "func .* Version() string" "$MIGRATIONS_DIR/$d.go" 2>/dev/null; then
        # Extract the actual struct name (PascalCase) from `type X struct`
        # — the filename is snake_case, the struct name is what gets
        # passed to migrations.<Name> in the runner.
        struct_name=$(grep -oE '^type [A-Z][A-Za-z0-9_]+ struct' "$MIGRATIONS_DIR/$d.go" | head -1 | awk '{print $2}')
        if [ -z "$struct_name" ]; then
          continue
        fi
        if ! grep -q "migrations\.$struct_name\b" "$MIGRATE_MAIN"; then
          missing_in_runner="$missing_in_runner $struct_name"
        fi
      fi
    done
    if [ -n "$missing_in_runner" ]; then
      echo "FAIL: migration structs without Register() call in $MIGRATE_MAIN:"
      for m in $missing_in_runner; do echo "    - $m"; done
      violations=$((violations + 1))
    fi
  fi
fi

if [ "$violations" -gt 0 ]; then
  echo ""
  echo "==> $violations secret-leak pattern(s) detected. See docs/security/ROTATION_RUNBOOK.md."
  exit 1
fi

echo "==> OK: no rotated literals, no hardcoded DSNs, migration runner intact."
exit 0
