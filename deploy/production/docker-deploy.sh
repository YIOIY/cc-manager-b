#!/usr/bin/env bash
# Deploys one image tag of cc-manager with a database safety net.
#
#   usage: docker-deploy.sh <tag> [registry-user]
#   A registry token may be piped on stdin; it is used for one pull with a
#   throwaway Docker config and never appears on a command line or on disk
#   after the script ends.
#
# Order of work, and what happens on failure:
#   1. lock, preflight, pull the image   -> on failure nothing has changed
#   2. online backup of the database     -> on failure nothing has changed
#   3. stop the running service          -> from here on any failure rolls back:
#   4. start the new tag, wait for          stop the new container; restore the
#      /healthz to report ok and this       step-2 backup ONLY if the new version
#      exact version                        migrated the schema; start the previous
#                                           tag (or the systemd service on the very
#                                           first Docker deploy)
#   5. record the tag, disable systemd, prune old images (best effort)
#
# Installed once by install-docker.sh into /opt/cc-manager-b/docker and run as
# root (the CI user may only sudo this exact path; sudo's env_reset drops the
# overrides below). Paths can be overridden for local testing; defaults are
# the production layout for the cc-manager-b instance (runs beside the older
# cc-manager on the same host; do not reuse its ports or /opt/cc-manager/docker).
set -Eeuo pipefail
# Keep going if the SSH session that started us goes away: a half-finished
# deploy must still roll back.
trap '' HUP PIPE

tag=${1:?usage: docker-deploy.sh <tag> [registry-user]}
registry_user=${2:-}

root=${DEPLOY_ROOT:-/opt/cc-manager-b/docker}
env_file=${ENV_FILE:-/etc/cc-manager/cc-manager-b.env}
data_dir=${DATA_DIR:-/var/lib/cc-manager-b}
backup_root=${BACKUP_ROOT:-/var/backups/cc-manager-b}
app_port=${APP_PORT:-18081}
# Host address the port is published on. Loopback unless this server says
# otherwise in $root/bind-addr (one line, an IPv4 address such as 0.0.0.0).
bind_addr=${BIND_ADDR:-}
pull=${PULL:-1}
# Empty by default so a first Docker deploy never stops/disables the older
# cc-manager systemd unit that still serves production traffic.
systemd_unit=${SYSTEMD_UNIT-}
systemd_user=${SYSTEMD_USER:-cc-manager}
chown_data=${CHOWN_DATA:-1}
health_timeout=${HEALTH_TIMEOUT:-90}
keep_images=${KEEP_IMAGES:-3}
host_tz=${HOST_TZ:-$(cat /etc/timezone 2>/dev/null || echo UTC)}
container_uid=10001

compose_file=$root/compose.yml
if [ -z "$bind_addr" ]; then
    bind_addr=$(tr -d '[:space:]' < "$root/bind-addr" 2>/dev/null || true)
fi
bind_addr=${bind_addr:-127.0.0.1}
state=$root/state
db=$data_dir/cc-manager.db
mkdir -p "$state"
run_log=$state/last-deploy.log
: > "$run_log" 2>/dev/null || true

# Progress goes to the run log and, while it is still there, to the caller.
log() {
    local line
    line="$(date -u +%Y-%m-%dT%H:%M:%SZ) $*"
    printf '%s\n' "$line" >> "$run_log" 2>/dev/null || true
    printf '%s\n' "$line" 2>/dev/null || true
}
die() { log "ERROR: $*"; exit 1; }

[[ $tag =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]] || die "invalid tag: $tag"
[[ $bind_addr =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}$ ]] || die "invalid bind address: $bind_addr"
if [ -z "${IMAGE:-}" ]; then
    [ -s "$root/image" ] || die "missing $root/image (run install-docker.sh)"
    IMAGE=$(tr -d '[:space:]' < "$root/image")
fi
image=$IMAGE
[ -f "$compose_file" ] || die "missing $compose_file"
[ -f "$env_file" ] || die "missing $env_file"

# Compose interpolates "$" inside env_file values: an argon2 hash would be
# mangled and pieces of it printed as "variable is not set". Refuse before
# touching anything; never print the offending value.
env_problem=$(python3 - "$env_file" <<'PY'
import sys
for n, raw in enumerate(open(sys.argv[1], encoding="utf-8"), 1):
    line = raw.strip()
    if not line or line.startswith("#") or "=" not in line:
        continue
    key, value = line.split("=", 1)
    value = value.strip()
    if "$" in value and not (len(value) >= 2 and value[0] == value[-1] == "'"):
        print(f"line {n} ({key.strip()}) contains '$'; wrap the value in single quotes")
PY
)
[ -z "$env_problem" ] || die "$env_file: $env_problem"

# One deploy at a time. mkdir is atomic and works without flock; the cleanup
# trap is installed only after we own the lock.
lock=$state/lock
mkdir "$lock" 2>/dev/null || die "another deploy is running (remove $lock if it is stale)"
docker_config=""
cleanup() {
    [ -n "$docker_config" ] && rm -rf "$docker_config"
    rmdir "$lock" 2>/dev/null || true
}
trap cleanup EXIT

# Compose output goes to the server-side log, not to the caller: on failure it
# can contain account data from the application log.
compose() {
    local use_tag=$1; shift
    IMAGE=$image TAG=$use_tag ENV_FILE=$env_file DATA_DIR=$data_dir APP_PORT=$app_port BIND_ADDR=$bind_addr HOST_TZ=$host_tz \
        docker compose -p cc-manager-b -f "$compose_file" "$@" >> "$run_log" 2>&1
}

wait_healthy() {
    local want=$1 deadline=$((SECONDS + health_timeout)) body
    while [ "$SECONDS" -lt "$deadline" ]; do
        if body=$(curl -fsS --max-time 3 "http://127.0.0.1:$app_port/healthz" 2>/dev/null) &&
            python3 -c 'import json,sys; b=json.loads(sys.argv[2]); sys.exit(0 if b.get("ok") is True and (not sys.argv[1] or b.get("version")==sys.argv[1]) else 1)' "$want" "$body"; then
            return 0
        fi
        sleep 2
    done
    return 1
}

own_data() {
    [ "$chown_data" = 1 ] || return 0
    [ -d "$data_dir" ] || return 0
    chown -R "$1" "$data_dir"
}

systemd_present() {
    [ -n "$systemd_unit" ] && command -v systemctl >/dev/null 2>&1 &&
        systemctl cat "$systemd_unit" >/dev/null 2>&1
}

# Python with a SQLite new enough for this database: the schema uses partial
# indexes (SQLite >= 3.8) and the backup uses Connection.backup (Python >= 3.7).
# Hosts that ship older ones (CentOS 7: Python 3.6, SQLite 3.7.17) run the same
# code in a throwaway official Python container, with the given directories
# mounted at the same paths.
#   sqlite_python <dir>... -- <python args>
sqlite_python() {
    local mounts=()
    while [ "$1" != "--" ]; do mounts+=(-v "$1:$1"); shift; done
    shift
    if python3 -c 'import sqlite3, sys; sys.exit(0 if sys.version_info >= (3, 7) and sqlite3.sqlite_version_info >= (3, 8, 3) else 1)' 2>/dev/null; then
        python3 "$@"
    else
        docker run --rm -i --network none "${mounts[@]}" "${SQLITE_PYTHON_IMAGE:-python:3.12-alpine}" python "$@"
    fi
}

schema_version() {   # prints the highest applied migration, or 0
    [ -f "$1" ] || { echo 0; return; }
    sqlite_python "$(dirname "$1")" -- -c 'import sqlite3,sys
try:
    v = sqlite3.connect(f"file:{sys.argv[1]}?mode=ro", uri=True).execute("select coalesce(max(version),0) from schema_migrations").fetchone()[0]
except Exception:
    v = 0
print(v)' "$1"
}

previous=$(cat "$state/current-tag" 2>/dev/null || true)
log "deploy $image:$tag (previous: ${previous:-none})"

# 1. Pull with a throwaway Docker config. Nothing has changed if this fails.
if [ "$pull" = 1 ]; then
    docker_config=$(mktemp -d)
    if [ -n "$registry_user" ] && [ ! -t 0 ]; then
        token=$(cat)
        [ -n "$token" ] || die "empty registry token on stdin"
        printf '%s' "$token" | DOCKER_CONFIG=$docker_config docker login "${image%%/*}" -u "$registry_user" --password-stdin >/dev/null 2>&1 ||
            die "registry login failed; nothing changed"
        unset token
    fi
    DOCKER_CONFIG=$docker_config docker pull -q "$image:$tag" >/dev/null 2>&1 || die "pull failed for $image:$tag; nothing changed"
    rm -rf "$docker_config"; docker_config=""
else
    docker image inspect "$image:$tag" >/dev/null 2>&1 || die "image $image:$tag not present; nothing changed"
fi

# 2. Back up the database while the old service is still running.
backup_dir=""
schema_before=0
if [ -f "$db" ]; then
    backup_dir=$backup_root/$(date -u +%Y%m%dT%H%M%SZ)-pre-$tag
    BACKUP_ROOT=$backup_root CC_MANAGER_DB=$db "$root/backup.sh" "$backup_dir" >> "$run_log" 2>&1 ||
        die "backup failed; nothing changed"
    schema_before=$(schema_version "$backup_dir/cc-manager.db")
    log "backup: $backup_dir (schema v$schema_before)"
fi

# Everything below must not run with errexit/ERR traps interfering: it is the
# path that puts the old service back.
rollback() {
    trap - ERR
    set +e
    log "rolling back: $1"
    compose "$tag" logs --no-color --tail 80
    compose "$tag" down --timeout 10
    docker rm -f cc-manager-b >/dev/null 2>&1
    if docker ps -a --format '{{.Names}}' | grep -qx cc-manager-b; then
        log "ERROR: container cc-manager-b is still present; database left untouched"
    elif [ -n "$backup_dir" ]; then
        local schema_after
        schema_after=$(schema_version "$db")
        if [ "$schema_after" -gt "$schema_before" ]; then
            # The new version migrated the schema; the old one cannot run on it.
            rm -f "$db" "$db-wal" "$db-shm"
            cp "$backup_dir/cc-manager.db" "$db"
            log "database restored from $backup_dir (schema v$schema_after -> v$schema_before); writes made by $tag are lost, check tasks in unknown state"
        else
            # Same schema: keep the data, including anything the new version did
            # while it ran (for example accounts it already submitted).
            log "database kept (schema unchanged at v$schema_before)"
        fi
    fi
    if [ -n "$previous" ]; then
        own_data "$container_uid:$container_uid"
        compose "$previous" up -d --no-build
        if wait_healthy "$previous"; then
            log "rolled back to $previous"
        else
            log "ERROR: previous tag $previous did not become healthy either; see $run_log"
        fi
    elif [ "$used_systemd" = 1 ]; then
        own_data "$systemd_user:$systemd_user"
        systemctl start "$systemd_unit"
        if wait_healthy ""; then
            log "rolled back to systemd service $systemd_unit"
        else
            log "ERROR: systemd service $systemd_unit did not become healthy either"
        fi
    fi
    log "details of the failed run: $run_log"
    exit 1
}

# 3. Stop whatever serves the port now. From here on, failures roll back.
used_systemd=0
# set -E also hands this trap to command substitutions such as $(curl ...)
# while polling /healthz; only the main shell may roll back.
trap '[ "$BASHPID" = "$$" ] && rollback "unexpected error on line $LINENO"' ERR
if [ -n "$previous" ]; then
    compose "$previous" down --timeout 20 || true
elif systemd_present && systemctl is-active --quiet "$systemd_unit"; then
    systemctl stop "$systemd_unit"
    used_systemd=1
fi

# 4. Start the new tag and require it to answer with this exact version.
own_data "$container_uid:$container_uid" || rollback "could not hand $data_dir to the container user"
compose "$tag" up -d --no-build || rollback "compose up failed"
wait_healthy "$tag" || rollback "health check did not report ok with version $tag within ${health_timeout}s"
trap - ERR
set +e

# 5. Record first (the next deploy needs a rollback target), then tidy up.
printf '%s\n' "$tag" > "$state/current-tag.tmp" && mv -f "$state/current-tag.tmp" "$state/current-tag"
[ -n "$previous" ] && printf '%s\n' "$previous" > "$state/previous-tag"
if systemd_present; then
    # Even a stopped unit would start again at boot and fight for the port.
    systemctl disable --now "$systemd_unit" >/dev/null 2>&1 && log "systemd service $systemd_unit disabled; Docker owns the service"
fi
printf '%s\t%s\t%s\t%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$tag" "${previous:--}" "${backup_dir:--}" >> "$state/deploys.log"
# Keep the current and previous tags plus the newest others, KEEP_IMAGES in all.
# Channel tags (prod, main) are pointers, never pruned.
keep_others=$((keep_images - 1))
[ -n "$previous" ] && [ "$previous" != "$tag" ] && keep_others=$((keep_others - 1))
[ "$keep_others" -ge 0 ] || keep_others=0
docker image ls "$image" --format '{{.Tag}}' 2>/dev/null |
    { grep -vxF -e "$tag" -e "${previous:-$tag}" -e '<none>' -e prod -e main || true; } |
    tail -n +"$((keep_others + 1))" | while read -r old; do
        docker image rm "$image:$old" >/dev/null 2>&1
    done
log "deployed $tag"
exit 0
