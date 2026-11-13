#!/usr/bin/env bash
# Builds, tests and publishes a production image from this machine. The
# server's update timer (deploy/production/docker-update.sh) deploys whatever
# ":prod" points at within a few minutes, with the usual backup and rollback.
#
#   scripts/release.sh                  test, build, push sha-<commit>, move :prod to it
#   scripts/release.sh --promote <tag>  point :prod at an existing sha-<commit> (roll back/forward)
#
# Options:
#   --skip-tests   do not run `make test` first
#   --no-prod      push sha-<commit> only; leave :prod (and the server) alone
#   --allow-dirty  build uncommitted changes (local testing only: the tag then
#                  does not identify the code)
# Environment:
#   RELEASE_IMAGE     registry path without tag (default ghcr.io/<origin owner>/cc-manager)
#   RELEASE_PLATFORM  target platform (default linux/amd64)
#
# One-time login (needs the write:packages scope):
#   gh auth refresh -h github.com -s write:packages
#   gh auth token | docker login ghcr.io -u <github-user> --password-stdin
set -Eeuo pipefail
cd "$(dirname "$0")/.."

die() { echo "release: $*" >&2; exit 1; }
step() { printf '\n==> %s\n' "$*"; }

promote="" skip_tests=0 move_prod=1 allow_dirty=0
while [ $# -gt 0 ]; do
    case $1 in
        --promote) promote=${2:-}; [ -n "$promote" ] || die "--promote needs a tag"; shift 2 ;;
        --skip-tests) skip_tests=1; shift ;;
        --no-prod) move_prod=0; shift ;;
        --allow-dirty) allow_dirty=1; shift ;;
        -h|--help) sed -n '2,21p' "$0"; exit 0 ;;
        *) die "unknown option $1 (see --help)" ;;
    esac
done

origin=$(git remote get-url origin 2>/dev/null || true)
owner=$(printf '%s' "$origin" | sed -nE 's#^(git@github\.com:|https://github\.com/)([^/]+)/.*#\2#p' | tr '[:upper:]' '[:lower:]')
image=${RELEASE_IMAGE:-${owner:+ghcr.io/$owner/cc-manager}}
[ -n "$image" ] || die "cannot derive the image from the git remote; set RELEASE_IMAGE"
platform=${RELEASE_PLATFORM:-linux/amd64}
tag_re='^sha-[0-9a-f]{12}$'

if [ -n "$promote" ]; then
    [[ $promote =~ $tag_re ]] || die "tag must look like sha-<12 hex>, got $promote"
    step "pointing $image:prod at $promote"
    docker pull -q --platform "$platform" "$image:$promote" >/dev/null || die "cannot pull $image:$promote"
    docker tag "$image:$promote" "$image:prod"
    docker push -q "$image:prod" >/dev/null
    echo "done: the server switches to $promote within a few minutes"
    exit 0
fi

if [ -n "$(git status --porcelain)" ]; then
    [ "$allow_dirty" = 1 ] || die "uncommitted changes; commit first (the image tag must identify the code)"
    echo "warning: building uncommitted changes (--allow-dirty)" >&2
fi
revision=$(git rev-parse HEAD)
tag=${RELEASE_TAG:-sha-${revision:0:12}}
[[ $tag =~ $tag_re ]] || die "invalid tag $tag"
if git fetch -q origin 2>/dev/null && [ -z "$(git branch -r --contains HEAD 2>/dev/null)" ]; then
    echo "warning: $revision is not on GitHub yet; push it so the release can be traced" >&2
fi

if [ "$skip_tests" = 0 ]; then
    step "tests (make test)"
    make test
fi

step "building $tag for $platform"
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
[ -d web/node_modules ] || (cd web && npm ci --no-audit --no-fund)
(cd web && npm run build)
CGO_ENABLED=0 GOOS=${platform%%/*} GOARCH=${platform#*/} \
    go build -trimpath -ldflags "-s -w -X main.version=$tag" -o "$stage/cc-manager" ./cmd/cc-manager
mkdir -p "$stage/web"
cp -R web/dist "$stage/web/dist"

# Assemble the runtime image from an alpine container instead of `docker
# build`: the legacy builder cannot COPY across platforms (Docker 20.10), and
# BuildKit cannot always pull through local registry mirrors. Keep this in step
# with the final stage of deploy/Dockerfile (the healthcheck lives in compose.yml).
# Refresh the base image when Docker Hub is reachable; otherwise go on with a
# cached copy of the right platform rather than block the release.
if ! docker pull -q --platform "$platform" alpine:3.22 >/dev/null 2>&1; then
    cached=$(docker image inspect alpine:3.22 --format '{{.Os}}/{{.Architecture}}' 2>/dev/null || true)
    [ "$cached" = "$platform" ] || die "cannot pull alpine:3.22 and no cached $platform copy (found: ${cached:-none})"
    echo "note: Docker Hub unreachable; using the cached alpine:3.22 ($cached)"
fi
cid=$(docker create --platform "$platform" alpine:3.22 sh -c '
    apk add --no-cache ca-certificates tzdata wget >/dev/null &&
    addgroup -g 10001 app && adduser -D -H -u 10001 -G app app &&
    mkdir -p /data /app/web && chown app:app /data')
trap 'docker rm -f "$cid" >/dev/null 2>&1; rm -rf "$stage"' EXIT
docker start -a "$cid" || die "preparing the base container failed"
# A tar stream so the files are owned by root, not by the local user.
COPYFILE_DISABLE=1 tar --uid 0 --gid 0 -cf - -C "$stage" cc-manager web | docker cp - "$cid:/app"
docker commit \
    --change 'WORKDIR /app' --change 'USER app' --change 'EXPOSE 8080' \
    --change 'ENV CC_MANAGER_LISTEN=0.0.0.0:8080 CC_MANAGER_DB=/data/cc-manager.db CC_MANAGER_WEB_DIR=/app/web/dist' \
    --change "LABEL org.opencontainers.image.title=cc-manager org.opencontainers.image.version=$tag org.opencontainers.image.revision=$revision org.opencontainers.image.source=${owner:+https://github.com/$owner/cc-manager}" \
    --change 'ENTRYPOINT ["/app/cc-manager"]' --change 'CMD ["serve"]' \
    "$cid" "$image:$tag" >/dev/null

step "checking the image"
got=$(docker run --rm --platform "$platform" "$image:$tag" version)
[ "$got" = "$tag" ] || die "image reports version '$got', expected $tag"
echo "image reports $got"

step "pushing $image:$tag"
docker push -q "$image:$tag" >/dev/null
if [ "$move_prod" = 1 ]; then
    docker tag "$image:$tag" "$image:prod"
    docker push -q "$image:prod" >/dev/null
    echo "done: :prod -> $tag; the server deploys it within a few minutes"
    echo "watch: ssh to the server, then: journalctl -u cc-manager-update -f"
else
    echo "done: pushed $tag only (:prod unchanged). Deploy later with: $0 --promote $tag"
fi
