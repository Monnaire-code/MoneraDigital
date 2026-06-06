#!/usr/bin/env bash
# =============================================================================
# scripts/install-hooks.sh
#
# C-1 mitigation: install a pre-commit hook that runs scripts/check-secrets.sh
# before any commit is allowed. Catches accidental re-introduction of
# rotated credentials at commit-time, before they reach the remote.
#
# Usage:
#   bash scripts/install-hooks.sh           # install
#   bash scripts/install-hooks.sh uninstall # remove
#
# Idempotent: re-running install is safe.
# =============================================================================

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HOOKS_DIR="$REPO_ROOT/.git/hooks"
HOOK_FILE="$HOOKS_DIR/pre-commit"

case "${1:-install}" in
  uninstall)
    if [ -f "$HOOK_FILE" ] && grep -q "check-secrets.sh" "$HOOK_FILE"; then
      rm -f "$HOOK_FILE"
      echo "Removed pre-commit hook (was installed by scripts/install-hooks.sh)."
    else
      echo "No install-managed pre-commit hook found. Nothing to do."
    fi
    exit 0
    ;;
esac

mkdir -p "$HOOKS_DIR"

if [ -f "$HOOK_FILE" ] && ! grep -q "check-secrets.sh" "$HOOK_FILE"; then
  echo "WARNING: an existing pre-commit hook is present and will be left untouched."
  echo "         Append a call to scripts/check-secrets.sh manually if desired."
fi

cat > "$HOOK_FILE" <<'HOOK_EOF'
#!/usr/bin/env bash
# Auto-installed by scripts/install-hooks.sh — do not edit by hand.
# Re-run scripts/install-hooks.sh uninstall to remove.
set -e
bash "$(git rev-parse --show-toplevel)/scripts/check-secrets.sh"
HOOK_EOF

chmod +x "$HOOK_FILE"
echo "Installed pre-commit hook at $HOOK_FILE"
echo "It runs scripts/check-secrets.sh before every commit."
