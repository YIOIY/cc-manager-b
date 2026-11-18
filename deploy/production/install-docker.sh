#!/usr/bin/env bash
# One-time (re-runnable) server setup for Docker releases pulled by the server.
#
#   printf '%s' "$READ_TOKEN" | sudo ./deploy/production/install-docker.sh <image> <registry-user>
#   e.g. ... install-docker.sh ghcr.io/acme/cc-manager-b acme
#
# READ_TOKEN: a GitHub classic token with only the read:packages scope. It is
# stored by `docker login` in root's Docker config and used by the update timer
# to pull images. Without a token on stdin the existing login is kept.
#
# What it does:
#   - installs compose.yml, docker-deploy.sh, docker-update-b.sh and backup.sh into
#     /opt/cc-manager-b/docker (root-owned), beside the older /opt/cc-manager
#   - installs and starts cc-manager-b-update.timer: every 2 minutes it runs
#     docker-update-b.sh to deploy the image ":prod" points at, if new
# What it does NOT do:
#   - touch the older cc-manager instance (ports, /opt/cc-manager, its timer,
#     systemd unit, or the legacy cc-deploy SSH user)
#   - switch public traffic; point nginx / Cloudflare Tunnel at :18081 when ready
#
# Re-run it after changing any of the installed files.
set -euo pipefail
image=${1:?usage: install-docker.sh <image> <registry-user>  (read-only token on stdin)}
registry_user=${2:?usage: install-docker.sh <image> <registry-user>  (read-only token on stdin)}
root=/opt/cc-manager-b/docker
here=$(cd "$(dirname "$0")" && pwd)

[ "$(id -u)" = 0 ] || { echo 'run as root' >&2; exit 1; }
[[ $image =~ ^[a-z0-9.-]+(:[0-9]+)?/[a-z0-9._/-]+$ ]] || { echo "image must be lower-case registry/path without a tag, got: $image" >&2; exit 1; }
[[ $registry_user =~ ^[A-Za-z0-9][A-Za-z0-9-]{0,38}$ ]] || { echo "invalid registry user: $registry_user" >&2; exit 1; }
for tool in docker curl python3 systemctl; do
    command -v "$tool" >/dev/null || { echo "missing $tool" >&2; missing=1; }
done
docker compose version >/dev/null 2>&1 || { echo 'missing the docker compose plugin' >&2; missing=1; }
if [ "${missing:-0}" = 1 ]; then
    echo 'On Ubuntu: apt-get install -y docker.io docker-compose-v2 curl python3' >&2
    exit 1
fi
env_file=/etc/cc-manager/cc-manager-b.env
[ -f "$env_file" ] || { echo "missing $env_file (keep the existing one: the encryption key must not change)" >&2; exit 1; }

# Pre-cutover checks. Values are never printed.
python3 - "$env_file" "${DATA_DIR:-/var/lib/cc-manager-b}/cc-manager.db" <<'PY'
import sqlite3, sys
env_path, db_path = sys.argv[1], sys.argv[2]
blocking, warnings = [], []
loopback = ("127.", "localhost", "::1", "[::1]")
for n, raw in enumerate(open(env_path, encoding="utf-8"), 1):
    line = raw.strip()
    if not line or line.startswith("#") or "=" not in line:
        continue
    key, value = (part.strip() for part in line.split("=", 1))
    if "$" in value and not (len(value) >= 2 and value[0] == value[-1] == "'"):
        blocking.append(f"{env_path} line {n} ({key}) contains '$': wrap the value in single quotes, e.g. {key}='...'")
    if key == "CC_MANAGER_SUPPLIER_PROXY" and any(h in value for h in loopback):
        warnings.append(f"{key} points at the host loopback; inside the container use host.docker.internal instead")
try:
    rows = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True).execute("select name, host from proxies").fetchall()
    for name, host in rows:
        if any(str(host).startswith(h) for h in loopback):
            warnings.append(f"proxy pool entry '{name}' uses a loopback host; after the switch it must be host.docker.internal")
except Exception as exc:
    warnings.append(f"could not inspect proxies in {db_path}: {exc.__class__.__name__}")
for w in warnings:
    print("WARNING:", w)
if blocking:
    for b in blocking:
        print("ERROR:", b, file=sys.stderr)
    sys.exit(1)
PY

# Registry login first: without it the timer cannot pull anything.
registry=${image%%/*}
if [ ! -t 0 ]; then
    token=$(cat)
    if [ -n "$token" ]; then
        printf '%s' "$token" | docker login "$registry" -u "$registry_user" --password-stdin >/dev/null ||
            { echo "docker login to $registry failed (token needs read:packages)" >&2; exit 1; }
        unset token
        echo "registry: logged in to $registry as $registry_user (stored in root's Docker config)"
    fi
fi
python3 - "$registry" <<'PY' || { echo "no Docker login for $registry: pipe a read-only token on stdin" >&2; exit 1; }
import json, os, sys
cfg = json.load(open(os.path.expanduser("~/.docker/config.json")))
sys.exit(0 if sys.argv[1] in cfg.get("auths", {}) or sys.argv[1] in cfg.get("credHelpers", {}) else 1)
PY

install -d -o root -g root -m 0755 "$root" "$root/state"
install -o root -g root -m 0644 "$here/compose.yml" "$root/compose.yml"
install -o root -g root -m 0755 "$here/docker-deploy.sh" "$root/docker-deploy.sh"
install -o root -g root -m 0755 "$here/docker-update-b.sh" "$root/docker-update-b.sh"
install -o root -g root -m 0755 "$here/backup.sh" "$root/backup.sh"
printf '%s\n' "$image" > "$root/image"
chmod 0644 "$root/image"
install -d -m 0700 /var/backups/cc-manager-b

install -o root -g root -m 0644 "$here/cc-manager-b-update.service" /etc/systemd/system/cc-manager-b-update.service
install -o root -g root -m 0644 "$here/cc-manager-b-update.timer" /etc/systemd/system/cc-manager-b-update.timer
systemctl daemon-reload
systemctl enable --now cc-manager-b-update.timer >/dev/null

cat <<EOS
Installed into $root for image $image.
Update timer: $(systemctl is-active cc-manager-b-update.timer) (every 2 minutes; log: journalctl -u cc-manager-b-update)
Current tag: $(cat "$root/state/current-tag" 2>/dev/null || echo "none yet")
Host port: 127.0.0.1:18081 (older cc-manager keeps 18080; point nginx/tunnel when ready)

Publish from a workstation with scripts/release.sh; the server deploys :prod within a few minutes.
EOS
