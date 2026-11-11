#!/usr/bin/env bash
# Consistent SQLite backup via the online backup API, verified twice.
# The environment file (encryption key) is NOT copied next to the database:
# a backup that holds both can decrypt every credential. Keep the key in a
# separate, access-controlled location and rotate it independently.
#
# usage: backup.sh [backup-dir] ; RETENTION_DAYS (default 30) prunes old sets.
set -euo pipefail
# BACKUP_ALLOW_NON_ROOT=1 exists only for local tests of the deploy script.
[ "$(id -u)" = 0 ] || [ "${BACKUP_ALLOW_NON_ROOT:-0}" = 1 ] || { echo 'run as root' >&2; exit 1; }
umask 077
root=${BACKUP_ROOT:-/var/backups/cc-manager}
db=${CC_MANAGER_DB:-/var/lib/cc-manager/cc-manager.db}
backup_dir=${1:-$root/$(date -u +%Y%m%dT%H%M%SZ)}
retention_days=${RETENTION_DAYS:-30}
[ ! -e "$backup_dir" ] || { echo 'Backup destination already exists' >&2; exit 1; }
mkdir -p "$backup_dir"
chmod 0700 "$backup_dir"
python3 - "$backup_dir" "$db" <<'PY'
import pathlib, sqlite3, sys
root, db = pathlib.Path(sys.argv[1]), sys.argv[2]
source = sqlite3.connect(f'file:{db}?mode=ro', uri=True)
backup = sqlite3.connect(root / 'cc-manager.db')
source.backup(backup)
assert backup.execute('pragma integrity_check').fetchone()[0] == 'ok'
backup.close(); source.close()
restored = sqlite3.connect(':memory:')
backup = sqlite3.connect(root / 'cc-manager.db')
backup.backup(restored)
assert restored.execute('pragma integrity_check').fetchone()[0] == 'ok'
restored.close(); backup.close()
PY
if [ -f /opt/cc-manager/current/SHA256SUMS ]; then
    cp -a /opt/cc-manager/current/SHA256SUMS "$backup_dir/release-SHA256SUMS"
fi
(cd "$backup_dir" && shasum -a 256 cc-manager.db > cc-manager.db.sha256)
chmod 0600 "$backup_dir"/*
if [ "$retention_days" -gt 0 ] && [ -d "$root" ]; then
    find "$root" -mindepth 1 -maxdepth 1 -type d -mtime +"$retention_days" -exec rm -rf {} +
fi
printf 'Verified backup: %s\n' "$backup_dir"
