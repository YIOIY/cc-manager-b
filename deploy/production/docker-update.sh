#!/usr/bin/env bash
# Run by cc-manager-update.timer. Deploys the image that ":prod" points at
# when it differs from what runs now, using docker-deploy.sh (backup, health
# check, rollback). Publish with scripts/release.sh on a workstation.
#
# - The version comes from the image label org.opencontainers.image.version
#   (sha-<12 hex>); the pulled image is tagged with it locally and deployed
#   without a second pull.
# - A version that failed to deploy is recorded in state/failed-tag and not
#   retried until :prod moves to another version (otherwise a bad image would
#   cause a restart every few minutes).
# - Pull credentials are root's normal Docker login (read-only token, set up by
#   install-docker.sh).
set -Eeuo pipefail

root=${DEPLOY_ROOT:-/opt/cc-manager/docker}
channel=${CHANNEL:-prod}
state=$root/state
mkdir -p "$state"
log() { printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" | tee -a "$state/update.log"; }

if [ -z "${IMAGE:-}" ]; then
    IMAGE=$(tr -d '[:space:]' < "$root/image")
fi
image=$IMAGE
# A deploy (manual or a previous run) is in progress: try next time.
[ -d "$state/lock" ] && exit 0

if ! out=$(docker pull -q "$image:$channel" 2>&1); then
    log "ERROR: cannot pull $image:$channel: $(printf '%s' "$out" | tail -n1)"
    exit 1
fi
version=$(docker image inspect -f '{{index .Config.Labels "org.opencontainers.image.version"}}' "$image:$channel")
if ! [[ $version =~ ^sha-[0-9a-f]{12}$ ]]; then
    log "ERROR: $image:$channel has no valid version label (got '$version'); not deploying"
    exit 1
fi

current=$(cat "$state/current-tag" 2>/dev/null || true)
[ "$version" = "$current" ] && exit 0
if [ "$version" = "$(cat "$state/failed-tag" 2>/dev/null || true)" ]; then
    exit 0   # already failed once; logged when it happened
fi

log "new version on $channel: $version (running: ${current:-none})"
docker tag "$image:$channel" "$image:$version"
if PULL=0 IMAGE=$image "$root/docker-deploy.sh" "$version"; then
    rm -f "$state/failed-tag"
    log "deployed $version"
else
    printf '%s\n' "$version" > "$state/failed-tag"
    log "ERROR: deploy of $version failed and was rolled back; it will not be retried. Details: $state/last-deploy.log"
    exit 1
fi
