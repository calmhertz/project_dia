#!/usr/bin/env bash
# Restore an Aagasa deployment from a backup (plan.md V18).
#
#   deployment/scripts/restore.sh deployment/data/backups/20260827T120000Z
#
# This overwrites the live databases. It stops the Server first, because
# restoring underneath a running control plane produces a state neither of
# them agrees with, and it refuses to proceed without an explicit yes.
#
# Rootless: it stops and starts the Server container through podman directly
# and feeds the datastores through `podman exec`; no privileges needed.

set -uo pipefail

DEPLOY="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SOURCE="${1:-}"
PG_POD="${AAGASA_PG_CONTAINER:-aagasa-databases-postgres}"
MONGO_POD="${AAGASA_MONGO_CONTAINER:-aagasa-databases-mongo}"
SERVER_POD="${AAGASA_SERVER_CONTAINER:-aagasa-server}"

say() { printf '%s\n' "$1"; }
die() { printf 'restore: %s\n' "$1" >&2; exit 1; }

[ -n "$SOURCE" ] || die "give the backup directory to restore from"
[ -f "$SOURCE/postgres.sql.gz" ] || die "$SOURCE has no postgres.sql.gz"

cat <<WARNING

This replaces the live databases with the backup in
  $SOURCE

Everything currently in PostgreSQL and MongoDB is discarded: passes,
approvals, recordings metadata, users and the audit trail.

WARNING

read -r -p "Type 'restore' to continue: " answer
[ "$answer" = "restore" ] || die "cancelled"

say "stopping the server"
podman stop "$SERVER_POD" 2>/dev/null || say "  (nothing was running)"

say "restoring postgres"
gunzip -c "$SOURCE/postgres.sql.gz" | podman exec -i "$PG_POD" psql -U aagasa -d aagasa \
    || die "the PostgreSQL restore failed; the database is in an unknown state"

if [ -f "$SOURCE/mongo.archive.gz" ]; then
    say "restoring mongo"
    podman exec -i "$MONGO_POD" mongorestore --quiet --archive --gzip --drop \
        < "$SOURCE/mongo.archive.gz" || say "  mongo restore failed; operational history is incomplete"
fi

say "starting the server"
podman start "$SERVER_POD" 2>/dev/null || say "  start it yourself: podman start $SERVER_POD"

cat <<NEXT

Restored. Two things to check by hand:

  1. Recording content is not in this backup. Compare
     $SOURCE/recordings.manifest
     against deployment/data/recordings and restore any missing files from
     wherever that content is kept.
  2. The station may hold plans the restored Server has never seen. It will
     reconcile on its next heartbeat; watch the Worker log.

NEXT
