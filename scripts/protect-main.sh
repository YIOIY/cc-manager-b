#!/usr/bin/env bash
# Turns on GitHub-side protection for main: changes need a pull request with
# one approval and green checks; nobody can delete main or rewrite its history.
# Repository admins (the owner) can still push directly, for releases.
#
# Private repositories only get this on GitHub Pro or above. On the Free plan
# GitHub answers 403 and nothing changes.
#
#   scripts/protect-main.sh            create or update the ruleset
#   scripts/protect-main.sh --status   show what is in force
set -Eeuo pipefail
cd "$(dirname "$0")/.."
repo=$(gh repo view --json nameWithOwner --jq .nameWithOwner)
name="protect main"

existing=$(gh api "repos/$repo/rulesets" --jq ".[] | select(.name == \"$name\") | .id" 2>/tmp/protect-main.err) || {
    if grep -q 'Upgrade to GitHub Pro' /tmp/protect-main.err; then
        echo "protect-main: 这个私有仓库所在的账号是 GitHub Free，不支持分支保护。" >&2
        echo "  升级到 GitHub Pro（Settings → Billing and plans）后重新执行本脚本即可。" >&2
        exit 2
    fi
    cat /tmp/protect-main.err >&2; exit 1
}
if [ "${1:-}" = --status ]; then
    [ -n "$existing" ] && gh api "repos/$repo/rulesets/$existing" --jq '{name, enforcement, rules: [.rules[].type]}' || echo "protect-main: 还没有启用"
    exit 0
fi

body=$(cat <<'JSON'
{
  "name": "protect main",
  "target": "branch",
  "enforcement": "active",
  "conditions": { "ref_name": { "include": ["~DEFAULT_BRANCH"], "exclude": [] } },
  "bypass_actors": [ { "actor_id": 5, "actor_type": "RepositoryRole", "bypass_mode": "always" } ],
  "rules": [
    { "type": "deletion" },
    { "type": "non_fast_forward" },
    { "type": "pull_request", "parameters": {
        "required_approving_review_count": 1,
        "dismiss_stale_reviews_on_push": true,
        "require_code_owner_review": true,
        "require_last_push_approval": true,
        "required_review_thread_resolution": true,
        "allowed_merge_methods": ["squash"] } },
    { "type": "required_status_checks", "parameters": {
        "strict_required_status_checks_policy": false,
        "required_status_checks": [
          { "context": "backend" }, { "context": "frontend" }, { "context": "e2e" }, { "context": "image" } ] } }
  ]
}
JSON
)
if [ -n "$existing" ]; then
    printf '%s' "$body" | gh api -X PUT "repos/$repo/rulesets/$existing" --input - --jq '"updated ruleset \(.id): \(.enforcement)"'
else
    printf '%s' "$body" | gh api -X POST "repos/$repo/rulesets" --input - --jq '"created ruleset \(.id): \(.enforcement)"'
fi
