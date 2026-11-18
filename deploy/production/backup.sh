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
root=${BACKUP_ROOT:-/var/backups/cc-manager-b}
db=${CC_MANAGER_DB:-/var/lib/cc-manager-b/cc-manager.db}
backup_dir=${1:-$root/$(date -u +%Y%m%dT%H%M%SZ)}
retention_days=${RETENTION_DAYS:-30}
[ ! -e "$backup_dir" ] || { echo 'Backup destination already exists' >&2; exit 1; }
mkdir -p "$backup_dir"
chmod 0700 "$backup_dir"
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
sqlite_python "$backup_dir" "$(dirname "$db")" -- - "$backup_dir" "$db" <<'PY'
import pathlib, sqlite3, sys
root, db = pathlib.Path(sys.argv[1]), sys.argv[2]
target = root / 'cc-manager.db'
source = sqlite3.connect(f'file:{db}?mode=ro', uri=True)
memory = sqlite3.connect(':memory:')
source.backup(memory)
source.close()
assert memory.execute('pragma integrity_check').fetchone()[0] == 'ok'
if hasattr(memory, 'serialize'):   # Python >= 3.11
    # Write the image with a plain file write, marked as a rollback-journal
    # database (header bytes 18/19 = 1): checking it then needs no -wal/-shm,
    # and SQLite's pwrite is not used (seccomp blocks it for containers on old
    # kernels such as CentOS 7). The app switches it back to WAL on open.
    image = bytearray(memory.serialize())
    image[18] = image[19] = 1
    target.write_bytes(bytes(image))
    check = sqlite3.connect(f'file:{target}?mode=ro', uri=True)
else:
    out = sqlite3.connect(str(target))
    memory.backup(out)
    out.close()
    check = sqlite3.connect(str(target))
memory.close()
assert check.execute('pragma integrity_check').fetchone()[0] == 'ok'
check.close()
PY
if [ -f /opt/cc-manager-b/current/SHA256SUMS ]; then
    cp -a /opt/cc-manager-b/current/SHA256SUMS "$backup_dir/release-SHA256SUMS"
fi
# sha256sum on Linux, shasum on macOS; same output format.
if command -v sha256sum >/dev/null; then sum='sha256sum'; else sum='shasum -a 256'; fi
(cd "$backup_dir" && $sum cc-manager.db > cc-manager.db.sha256)
chmod 0600 "$backup_dir"/*
if [ "$retention_days" -gt 0 ] && [ -d "$root" ]; then
    find "$root" -mindepth 1 -maxdepth 1 -type d -mtime +"$retention_days" -exec rm -rf {} +
fi
printf 'Verified backup: %s\n' "$backup_dir"
