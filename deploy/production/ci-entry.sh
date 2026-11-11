#!/usr/bin/env bash
# Forced command for the CI deploy key (see install-docker.sh). Whatever the
# client asks to run arrives in SSH_ORIGINAL_COMMAND; only
#     deploy <tag> [registry-user]
# is accepted. stdin (the short-lived registry token) is passed through.
set -euo pipefail
read -r verb tag user extra <<<"${SSH_ORIGINAL_COMMAND:-}" || true
if [ "${verb:-}" != deploy ] || [ -z "${tag:-}" ] || [ -n "${extra:-}" ]; then
    echo 'only "deploy <tag> [registry-user]" is allowed' >&2
    exit 2
fi
# Only immutable commit tags built by CI.
[[ $tag =~ ^sha-[0-9a-f]{12}$ ]] || { echo "invalid tag (expected sha-<12 hex>)" >&2; exit 2; }
if [ -n "${user:-}" ]; then
    # GitHub logins, including app actors such as dependabot[bot].
    [[ $user =~ ^[A-Za-z0-9][A-Za-z0-9-]{0,38}(\[bot\])?$ ]] || { echo "invalid registry user" >&2; exit 2; }
    exec sudo -n /opt/cc-manager/docker/docker-deploy.sh "$tag" "$user"
fi
exec sudo -n /opt/cc-manager/docker/docker-deploy.sh "$tag"
