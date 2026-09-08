#!/usr/bin/env bash
# Back up an Aagasa deployment (plan.md V18).
#
#   deployment/scripts/backup.sh [destination]
#
# Takes a consistent copy of PostgreSQL, MongoDB and the recording metadata
# layout. Recording *content* is deliberately not copied here: it is large,
# it is already the authoritative long-term copy on this host, and mixing it
# into a nightly dump makes the dump too big to actually run. Back
# "$DEPLOY/data/recordings" up separately, at whatever cadence its size
# allows.
#
# PostgreSQL is the one that matters: it holds the schedule, the users and the
# audit trail. Redis is sessions only and is intentionally skipped, because a
# restored session is worth nothing and signing in again costs a moment.
#
# Rootless: this needs no privileges. It talks to the datastores through
# `podman exec`, the same way deploy.sh does, and writes under deployment/data.

set -uo pipefail

DEPLOY="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DESTINATION="${1:-$DEPLOY/data/backups}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
TARGET="$DESTINATION/$STAMP"

PG_POD="${AAGASA_PG_CONTAINER:-aagasa-databases-postgres}"
MONGO_POD="${AAGASA_MONGO_CONTAINER:-aagasa-databases-mongo}"
RECORDINGS="${AAGASA_RECORDINGS_DIR:-$DEPLOY/data/recordings}"

# How many dated backups to keep. A ground station fills a disk with
# recordings, so an unbounded backup directory is a second way to run out.
KEEP="${AAGASA_BACKUP_KEEP:-14}"

say() { printf '%s\n' "$1"; }
die() { printf 'backup: %s\n' "$1" >&2; exit 1; }

command -v podman >/dev/null || die "podman is required"
mkdir -p "$TARGET" || die "cannot create $TARGET"

say "backing up to $TARGET"

# PostgreSQL -----------------------------------------------------------------
#
# --clean --if-exists so the dump can be restored over an existing database
# without hand-editing it first.
if ! podman exec "$PG_POD" pg_dump -U aagasa --clean --if-exists aagasa \
        | gzip > "$TARGET/postgres.sql.gz"; then
    die "pg_dump failed; the backup is incomplete and has been left in place for inspection"
fi
say "  postgres $(du -h "$TARGET/postgres.sql.gz" | cut -f1)"

# MongoDB --------------------------------------------------------------------
#
# Operational documents: telemetry and execution history. Losing them costs
# history, not the ability to run the station.
if podman exec "$MONGO_POD" mongodump --quiet --db aagasa --archive --gzip \
        > "$TARGET/mongo.archive.gz"; then
    say "  mongo    $(du -h "$TARGET/mongo.archive.gz" | cut -f1)"
else
    say "  mongo    FAILED (continuing: PostgreSQL is the authoritative copy)"
fi

# What the recordings look like, so a restore can tell what is missing.
if [ -d "$RECORDINGS" ]; then
    ( cd "$RECORDINGS" && find . -type f -printf '%p\t%s\n' ) > "$TARGET/recordings.manifest"
    say "  manifest $(wc -l < "$TARGET/recordings.manifest") recordings listed"
fi

printf 'aagasa backup %s\nhost %s\n' "$STAMP" "$(hostname)" > "$TARGET/INFO"
chmod -R go-rwx "$TARGET"

# Prune, oldest first, keeping the most recent KEEP.
mapfile -t existing < <(find "$DESTINATION" -maxdepth 1 -mindepth 1 -type d -printf '%f\n' | sort)
if [ "${#existing[@]}" -gt "$KEEP" ]; then
    for old in "${existing[@]:0:$((${#existing[@]} - KEEP))}"; do
        rm -rf "${DESTINATION:?}/$old"
        say "  pruned   $old"
    done
fi

say "done"
