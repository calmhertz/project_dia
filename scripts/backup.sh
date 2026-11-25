#!/usr/bin/env bash
#
# Back up the Aagasa station: PostgreSQL, MongoDB, the env files (they hold
# the database password and worker secret a restore needs) and, with
# --recordings, the recording files themselves.
#
#   scripts/backup.sh [--recordings] [DESTINATION]     (default: ./backups)
#
# Keeps the newest AAGASA_BACKUP_KEEP backups (default 14). Run it from cron
# while the station is up.

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DATA="${AAGASA_DATA_DIR:-$ROOT/data}"
KEEP="${AAGASA_BACKUP_KEEP:-14}"

WITH_RECORDINGS=false
if [[ "${1:-}" == "--recordings" ]]; then
    WITH_RECORDINGS=true
    shift
fi
DESTINATION="${1:-$ROOT/backups}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
TARGET="$DESTINATION/$STAMP"

say() { printf '%s\n' "$1"; }
die() { printf 'backup: %s\n' "$1" >&2; exit 1; }

command -v podman >/dev/null || die "podman is required"
podman container exists aagasa-db-postgres \
    || die "the station is not running; start it with ./deploy.sh both up"

(umask 077; mkdir -p "$TARGET") || die "cannot create $TARGET"
say "backing up to $TARGET"

podman exec aagasa-db-postgres pg_dump -U aagasa --clean --if-exists aagasa \
    | gzip > "$TARGET/postgres.sql.gz"
[[ "${PIPESTATUS[0]}" -eq 0 ]] \
    || die "pg_dump failed; the incomplete backup is left in $TARGET for inspection"
say "  postgres    $(du -h "$TARGET/postgres.sql.gz" | cut -f1)"

if podman exec aagasa-db-mongo mongodump --quiet --db aagasa --archive --gzip \
        > "$TARGET/mongo.archive.gz"; then
    say "  mongo       $(du -h "$TARGET/mongo.archive.gz" | cut -f1)"
else
    say "  mongo       FAILED (continuing: PostgreSQL is the authoritative copy)"
fi

mkdir -p "$TARGET/env"
cp "$ROOT/deploy/server/.env.server" "$ROOT/deploy/worker/.env.worker" "$TARGET/env/" \
    || die "could not copy the env files"
say "  env files   copied"

if [[ -d "$DATA/recordings" ]]; then
    (cd "$DATA/recordings" && find . -type f -printf '%p\t%s\n') > "$TARGET/recordings.manifest"
    say "  manifest    $(wc -l < "$TARGET/recordings.manifest") recording files listed"
    if $WITH_RECORDINGS; then
        tar -C "$DATA" -cf "$TARGET/recordings.tar" recordings \
            || die "copying the recordings failed"
        say "  recordings  $(du -h "$TARGET/recordings.tar" | cut -f1)"
    fi
fi

printf 'aagasa backup %s\nhost %s\n' "$STAMP" "$(hostname)" > "$TARGET/INFO"
chmod -R go-rwx "$TARGET"

mapfile -t existing < <(find "$DESTINATION" -maxdepth 1 -mindepth 1 -type d -printf '%f\n' | sort)
if [[ "${#existing[@]}" -gt "$KEEP" ]]; then
    for old in "${existing[@]:0:$((${#existing[@]} - KEEP))}"; do
        rm -rf "${DESTINATION:?}/$old"
        say "  pruned      $old"
    done
fi

say "done"
