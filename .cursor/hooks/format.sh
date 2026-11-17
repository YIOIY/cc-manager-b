#!/usr/bin/env bash
# afterFileEdit: keep agent edits Prettier/gofmt clean so CI format:check stays green.
set -euo pipefail

input=$(cat)
file_path=$(python3 -c 'import json,sys; print(json.load(sys.stdin).get("file_path") or "")' <<<"$input" 2>/dev/null || true)
[ -n "${file_path:-}" ] && [ -f "$file_path" ] || exit 0

repo_root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$repo_root" || exit 0

pick_node() {
  is_ok() {
    local bin=$1 major
    [ -x "$bin" ] || return 1
    major=$("$bin" -p "process.versions.node.split('.')[0]" 2>/dev/null) || return 1
    [ "$major" -ge 18 ]
  }
  local candidate root bin
  candidate=$(command -v node 2>/dev/null || true)
  if [ -n "$candidate" ] && is_ok "$candidate"; then
    printf '%s\n' "$candidate"
    return 0
  fi
  for root in "${NVM_DIR:-}" "${HOME}/.nvm" /usr/local/opt/nvm; do
    [ -n "$root" ] && [ -d "$root/versions/node" ] || continue
    for bin in $(ls -1d "$root/versions/node"/v*/bin/node 2>/dev/null | sort -V -r); do
      if is_ok "$bin"; then
        printf '%s\n' "$bin"
        return 0
      fi
    done
  done
  return 1
}

case "$file_path" in
*.go)
  if command -v gofmt >/dev/null 2>&1; then
    gofmt -w "$file_path" >/dev/null 2>&1 || true
  fi
  ;;
*.vue|*.ts|*.tsx|*.js|*.jsx|*.mjs|*.cjs|*.json|*.css|*.md|*.yml|*.yaml)
  case "$file_path" in
  */web/*)
    prettier="$repo_root/web/node_modules/.bin/prettier"
    [ -x "$prettier" ] || exit 0
    if node_bin=$(pick_node); then
      PATH="$(dirname "$node_bin"):$PATH" "$prettier" --write "$file_path" >/dev/null 2>&1 || true
    fi
    ;;
  esac
  ;;
esac

exit 0
