#!/usr/bin/env bash
#
# Restore an Aagasa backup made by scripts/backup.sh.
#
#   scripts/restore.sh BACKUP_DIRECTORY
#
# Replaces the live databases (and, if the backup has them, the recordings).
# The env files from the backup are restored only when the live ones are
# missing, because the database password inside them must match the database.

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DATA="${AAGASA_DATA_DIR:-$ROOT/data}"
SOURCE="${1:-}"

say() { printf '%s\n' "$1"; }
die() { printf 'restore: %s\n' "$1" >&2; exit 1; }

[[ -n "$SOURCE" ]] || die "give the backup directory to restore from"
[[ -f "$SOURCE/postgres.sql.gz" ]] || die "$SOURCE has no postgres.sql.gz"
command -v podman >/dev/null || die "podman is required"

cat <<WARNING

This replaces the live station data with the backup in
  $SOURCE

Everything now in PostgreSQL and MongoDB is discarded: passes, approvals,
recording metadata, users and the audit trail.

WARNING
read -r -p "Type 'restore' to continue: " answer
[[ "$answer" == "restore" ]] || die "cancelled"

for file in .env.server:deploy/server .env.worker:deploy/worker; do
    name="${file%%:*}"
    dir="$ROOT/${file#*:}"
    if [[ ! -f "$dir/$name" && -f "$SOURCE/env/$name" ]]; then
        (umask 077; cp "$SOURCE/env/$name" "$dir/$name") && say "restored $name"
    fi
done

if ! podman container exists aagasa-db-postgres; then
    say "starting the station"
    "$ROOT/deploy.sh" both up || die "the station did not start"
fi

say "stopping the server and worker while the data is replaced"
podman stop aagasa-worker aagasa-server >/dev/null 2>&1

say "restoring postgres"
gunzip -c "$SOURCE/postgres.sql.gz" \
    | podman exec -i aagasa-db-postgres psql -q -U aagasa -d aagasa >/dev/null \
    || die "the PostgreSQL restore failed; the database is in an unknown state"

if [[ -f "$SOURCE/mongo.archive.gz" ]]; then
    say "restoring mongo"
    podman exec -i aagasa-db-mongo mongorestore --quiet --archive --gzip --drop \
        < "$SOURCE/mongo.archive.gz" || say "  mongo restore failed; execution history is incomplete"
fi

if [[ -f "$SOURCE/recordings.tar" ]]; then
    say "restoring recordings"
    tar -C "$DATA" -xf "$SOURCE/recordings.tar" || die "restoring the recordings failed"
fi

say "starting the server and worker"
podman start aagasa-server aagasa-worker >/dev/null || die "start them with ./deploy.sh restart"

cat <<NEXT

Restored. To check:
  - Without --recordings in the backup, compare $SOURCE/recordings.manifest
    with $DATA/recordings and copy back any missing files.
  - The worker may hold plans the restored server has never seen; it
    reconciles on its next heartbeat (podman logs -f aagasa-worker).
NEXT
