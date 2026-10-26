#!/usr/bin/env bash

set -uo pipefail

DESTINATION="${1:-/var/backups/aagasa}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
TARGET="$DESTINATION/$STAMP"

PG_POD="${AAGASA_PG_CONTAINER:-aagasa-databases-postgres}"
MONGO_POD="${AAGASA_MONGO_CONTAINER:-aagasa-databases-mongo}"
RECORDINGS="${AAGASA_RECORDINGS_DIR:-/var/lib/aagasa/recordings}"

KEEP="${AAGASA_BACKUP_KEEP:-14}"

say() { printf '%s\n' "$1"; }
die() { printf 'backup: %s\n' "$1" >&2; exit 1; }

command -v podman >/dev/null || die "podman is required"
mkdir -p "$TARGET" || die "cannot create $TARGET"

say "backing up to $TARGET"

if ! podman exec "$PG_POD" pg_dump -U aagasa --clean --if-exists aagasa \
        | gzip > "$TARGET/postgres.sql.gz"; then
    die "pg_dump failed; the backup is incomplete and has been left in place for inspection"
fi
say "  postgres $(du -h "$TARGET/postgres.sql.gz" | cut -f1)"

if podman exec "$MONGO_POD" mongodump --quiet --db aagasa --archive --gzip \
        > "$TARGET/mongo.archive.gz"; then
    say "  mongo    $(du -h "$TARGET/mongo.archive.gz" | cut -f1)"
else
    say "  mongo    FAILED (continuing: PostgreSQL is the authoritative copy)"
fi

if [ -d "$RECORDINGS" ]; then
    ( cd "$RECORDINGS" && find . -type f -printf '%p\t%s\n' ) > "$TARGET/recordings.manifest"
    say "  manifest $(wc -l < "$TARGET/recordings.manifest") recordings listed"
fi

printf 'aagasa backup %s\nhost %s\n' "$STAMP" "$(hostname)" > "$TARGET/INFO"
chmod -R go-rwx "$TARGET"

mapfile -t existing < <(find "$DESTINATION" -maxdepth 1 -mindepth 1 -type d -printf '%f\n' | sort)
if [ "${#existing[@]}" -gt "$KEEP" ]; then
    for old in "${existing[@]:0:$((${#existing[@]} - KEEP))}"; do
        rm -rf "${DESTINATION:?}/$old"
        say "  pruned   $old"
    done
fi

say "done"
