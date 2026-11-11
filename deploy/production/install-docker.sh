#!/usr/bin/env bash
# One-time (re-runnable) server setup for Docker deploys driven by CI.
#
#   sudo ./deploy/production/install-docker.sh <image> <ci-public-key-file>
#   e.g. sudo ./deploy/production/install-docker.sh ghcr.io/acme/cc-manager ./ci_deploy.pub
#
# What it does:
#   - installs compose.yml, docker-deploy.sh, backup.sh and ci-entry.sh into
#     /opt/cc-manager/docker (root-owned: the CI user cannot change them)
#   - creates the "cc-deploy" user whose only SSH key is pinned to ci-entry.sh,
#     and a sudo rule that allows exactly docker-deploy.sh
# What it does NOT do: switch traffic. The first CI deploy stops the systemd
# service, starts the container and rolls back to systemd if it is unhealthy.
#
# Re-run it after changing any of the installed scripts.
set -euo pipefail
image=${1:?usage: install-docker.sh <image> <ci-public-key-file>}
pubkey_file=${2:?usage: install-docker.sh <image> <ci-public-key-file>}
deploy_user=${DEPLOY_USER:-cc-deploy}
root=/opt/cc-manager/docker
here=$(cd "$(dirname "$0")" && pwd)

[ "$(id -u)" = 0 ] || { echo 'run as root' >&2; exit 1; }
[[ $image =~ ^[a-z0-9.-]+(:[0-9]+)?/[a-z0-9._/-]+$ ]] || { echo "image must be lower-case registry/path without a tag, got: $image" >&2; exit 1; }
[ -s "$pubkey_file" ] || { echo "missing public key file $pubkey_file" >&2; exit 1; }
grep -qE '^(ssh-ed25519|ecdsa-sha2-nistp256|ssh-rsa) ' "$pubkey_file" || { echo 'not an SSH public key' >&2; exit 1; }
for tool in docker curl python3; do
    command -v "$tool" >/dev/null || { echo "missing $tool" >&2; missing=1; }
done
docker compose version >/dev/null 2>&1 || { echo 'missing the docker compose plugin' >&2; missing=1; }
if [ "${missing:-0}" = 1 ]; then
    echo 'On Ubuntu: apt-get install -y docker.io docker-compose-v2 curl python3' >&2
    exit 1
fi
env_file=/etc/cc-manager/cc-manager.env
[ -f "$env_file" ] || { echo "missing $env_file (keep the existing one: the encryption key must not change)" >&2; exit 1; }

# Pre-cutover checks. Values are never printed.
python3 - "$env_file" "${DATA_DIR:-/var/lib/cc-manager}/cc-manager.db" <<'PY'
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

install -d -o root -g root -m 0755 "$root" "$root/state"
install -o root -g root -m 0644 "$here/compose.yml" "$root/compose.yml"
install -o root -g root -m 0755 "$here/docker-deploy.sh" "$root/docker-deploy.sh"
install -o root -g root -m 0755 "$here/backup.sh" "$root/backup.sh"
install -o root -g root -m 0755 "$here/ci-entry.sh" "$root/ci-entry.sh"
printf '%s\n' "$image" > "$root/image"
chmod 0644 "$root/image"
install -d -m 0700 /var/backups/cc-manager

if ! id "$deploy_user" >/dev/null 2>&1; then
    useradd --system --create-home --shell /bin/bash "$deploy_user"
fi
passwd -l "$deploy_user" >/dev/null 2>&1 || true
home=$(getent passwd "$deploy_user" | cut -d: -f6)
install -d -o "$deploy_user" -g "$deploy_user" -m 0700 "$home/.ssh"
# The key can only run ci-entry.sh: no shell, no forwarding, no pty.
printf 'command="%s/ci-entry.sh",restrict %s\n' "$root" "$(head -n1 "$pubkey_file")" > "$home/.ssh/authorized_keys"
chown "$deploy_user:$deploy_user" "$home/.ssh/authorized_keys"
chmod 0600 "$home/.ssh/authorized_keys"

sudoers=/etc/sudoers.d/cc-manager-deploy
printf '%s ALL=(root) NOPASSWD: %s/docker-deploy.sh\nDefaults:%s !requiretty\n' "$deploy_user" "$root" "$deploy_user" > "$sudoers.tmp"
chmod 0440 "$sudoers.tmp"
visudo -cf "$sudoers.tmp" >/dev/null
mv "$sudoers.tmp" "$sudoers"

cat <<EOF
Installed into $root for image $image.

GitHub repository → Settings → Environments → "production":
  - Required reviewers: yourself (deploys wait for approval)
  - Secrets:
      DEPLOY_HOST         this server's address
      DEPLOY_PORT         SSH port (optional, default 22)
      DEPLOY_USER         $deploy_user
      DEPLOY_SSH_KEY      the private key matching $pubkey_file
      DEPLOY_KNOWN_HOSTS  output of: ssh-keyscan -t ed25519 <host>

Current service: $(systemctl is-active cc-manager 2>/dev/null || echo unknown) (systemd).
The first approved deploy switches to Docker and rolls back to systemd if it fails.
EOF
