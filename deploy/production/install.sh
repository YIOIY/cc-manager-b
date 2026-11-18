#!/usr/bin/env bash
# Installs or upgrades a release directory. Re-runnable: an existing
# environment file is kept (never overwrite the encryption key), the
# `current` symlink is switched atomically and the service restarted.
#
# usage: install.sh <release-dir> [environment-file]
#   SERVER_NAME  public host name used in nginx (default cc.example.com)
#   APP_PORT     loopback port the Go binary listens on (default 18081)
#   NGINX_PORT   loopback port nginx listens on for the tunnel (default 8089)
set -euo pipefail
release=$(cd "${1:?usage: install.sh release-directory [environment-file]}" && pwd)
env_file=${2:-}
server_name=${SERVER_NAME:-cc.example.com}
app_port=${APP_PORT:-18081}
nginx_port=${NGINX_PORT:-8089}

[ "$(id -u)" = 0 ] || { echo 'run as root' >&2; exit 1; }
[ -x "$release/cc-manager" ] || { echo "missing binary in $release" >&2; exit 1; }
[ -f "$release/web/dist/index.html" ] || { echo "missing frontend build in $release" >&2; exit 1; }

if ! id cc-manager >/dev/null 2>&1; then
    useradd --system --home-dir /var/lib/cc-manager-b --shell /usr/sbin/nologin cc-manager
fi
install -d -m 0755 /opt/cc-manager-b/releases
install -d -m 0700 /etc/cc-manager
if [ -e /etc/cc-manager/cc-manager-b.env ]; then
    echo 'Keeping existing /etc/cc-manager/cc-manager-b.env (encryption key must not change).'
elif [ -n "$env_file" ] && [ -f "$env_file" ]; then
    install -m 0600 "$env_file" /etc/cc-manager/cc-manager-b.env
else
    echo 'First install needs an environment file argument (see cc-manager.env.example).' >&2
    exit 1
fi
if ! grep -q '^CC_MANAGER_LISTEN=' /etc/cc-manager/cc-manager-b.env; then
    echo "CC_MANAGER_LISTEN=127.0.0.1:${app_port}" >> /etc/cc-manager/cc-manager-b.env
fi

ln -sfn "$release" /opt/cc-manager-b/current.next
mv -Tf /opt/cc-manager-b/current.next /opt/cc-manager-b/current
install -m 0644 "$release/deploy/production/cc-manager.service" /etc/systemd/system/cc-manager-b.service
sed -e "s/__SERVER_NAME__/${server_name}/g" -e "s/__APP_PORT__/${app_port}/g" -e "s/__NGINX_PORT__/${nginx_port}/g" \
    "$release/deploy/production/cc-manager.nginx.conf" > /etc/nginx/conf.d/cc-manager-b.conf
nginx -t
systemctl daemon-reload
systemctl enable cc-manager-b >/dev/null
systemctl restart cc-manager-b
for _ in $(seq 1 30); do
    if curl --fail --silent "http://127.0.0.1:${app_port}/healthz" >/dev/null; then break; fi
    sleep 1
done
curl --fail --silent "http://127.0.0.1:${app_port}/healthz"
echo
systemctl reload nginx
for _ in $(seq 1 15); do
    if curl --fail --silent -H "Host: ${server_name}" "http://127.0.0.1:${nginx_port}/healthz" >/dev/null; then
        echo "installed $(readlink /opt/cc-manager-b/current)"
        exit 0
    fi
    sleep 1
done
echo 'nginx health check failed' >&2
exit 1
