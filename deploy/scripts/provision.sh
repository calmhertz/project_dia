#!/usr/bin/env bash

set -uo pipefail

ROLE="${1:-}"
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
UNITS=/etc/containers/systemd
STATE=/var/lib/aagasa
TLS_DIR=/etc/aagasa/tls

say()  { printf '\n== %s\n' "$1"; }
note() { printf '   %s\n' "$1"; }
die()  { printf 'provision: %s\n' "$1" >&2; exit 1; }

[ "$ROLE" = "server" ] || [ "$ROLE" = "worker" ] || die "usage: provision.sh server|worker"
[ "$(id -u)" = "0" ] || die "run as root: it installs system units and writes under /var/lib"
command -v podman >/dev/null || die "podman is required"
command -v openssl >/dev/null || die "openssl is required, to generate secrets"

secret() {
    local name="$1" generator="$2"
    if podman secret inspect "$name" >/dev/null 2>&1; then
        note "$name already exists"
        return 0
    fi
    eval "$generator" | podman secret create "$name" - >/dev/null \
        || die "could not create the secret $name"
    note "$name created"
}

say "Directories"
install -d -m 0750 "$STATE"
if [ "$ROLE" = "server" ]; then
    install -d -m 0700 "$STATE/postgres" "$STATE/mongo"
    install -d -m 0750 "$STATE/recordings" "$STATE/pipelines"
    install -d -m 0750 "$TLS_DIR"
else
    install -d -m 0750 "$STATE/state" "$STATE/recordings"
fi
note "$STATE ready"

say "Secrets"
if [ "$ROLE" = "server" ]; then
    secret aagasa-postgres-password "openssl rand -hex 24"
    if ! podman secret inspect aagasa-postgres-url >/dev/null 2>&1; then
        password="$(podman secret inspect --showsecret --format '{{.SecretData}}' aagasa-postgres-password 2>/dev/null)"
        [ -n "$password" ] || die "cannot read the PostgreSQL password back to build its URL"
        printf 'postgres://aagasa:%s@127.0.0.1:5432/aagasa?sslmode=disable' "$password" \
            | podman secret create aagasa-postgres-url - >/dev/null
        note "aagasa-postgres-url created"
    else
        note "aagasa-postgres-url already exists"
    fi
    secret aagasa-redis-url "printf 'redis://127.0.0.1:6379/0'"
    secret aagasa-mongo-url "printf 'mongodb://127.0.0.1:27017'"
fi
secret aagasa-worker-shared-secret "openssl rand -hex 32"

if [ "$ROLE" = "server" ]; then
    say "TLS"
    if [ -f "$TLS_DIR/tls.crt" ] && [ -f "$TLS_DIR/tls.key" ]; then
        note "certificate already in place"
    else
        openssl req -x509 -newkey rsa:4096 -sha256 -days 825 -nodes \
            -keyout "$TLS_DIR/tls.key" -out "$TLS_DIR/tls.crt" \
            -subj "/CN=$(hostname -f 2>/dev/null || hostname)" \
            -addext "subjectAltName=DNS:$(hostname -f 2>/dev/null || hostname),IP:127.0.0.1" \
            >/dev/null 2>&1 || die "could not generate a certificate"
        note "self-signed certificate generated; replace it with a real one"
    fi
    chown 10001:10001 "$TLS_DIR/tls.key" "$TLS_DIR/tls.crt"
    chmod 0600 "$TLS_DIR/tls.key"
    chmod 0644 "$TLS_DIR/tls.crt"
fi

say "Units"
if [ "$ROLE" = "server" ]; then
    install -m 0644 "$REPO/deploy/server/databases.yaml" "$UNITS/databases.yaml"
    install -m 0644 "$REPO/deploy/server/server.yaml" "$UNITS/server.yaml"
    install -m 0644 "$REPO/deploy/server/aagasa-databases.kube" "$UNITS/aagasa-databases.kube"
    install -m 0644 "$REPO/deploy/server/aagasa-server.kube" "$UNITS/aagasa-server.kube"
else
    install -m 0644 "$REPO/deploy/worker/worker.yaml" "$UNITS/worker.yaml"
    install -m 0644 "$REPO/deploy/worker/aagasa-worker.kube" "$UNITS/aagasa-worker.kube"
fi
systemctl daemon-reload
note "installed to $UNITS"

say "Logs"
install -d -m 0755 /etc/systemd/journald.conf.d
cat > /etc/systemd/journald.conf.d/aagasa.conf <<'CONF'
# Aagasa: bound the journal so logs cannot fill the recording disk.
[Journal]
SystemMaxUse=2G
SystemKeepFree=4G
MaxRetentionSec=1month
CONF
systemctl restart systemd-journald
note "journal capped at 2G, kept for a month"

say "Start"
if [ "$ROLE" = "server" ]; then
    systemctl enable --now aagasa-databases.service || die "the databases did not start"
    note "waiting for the datastores"
    for _ in $(seq 1 60); do
        podman exec aagasa-databases-postgres pg_isready -U aagasa >/dev/null 2>&1 && break
        sleep 2
    done
    systemctl enable --now aagasa-server.service || die "the server did not start"
else
    systemctl enable --now aagasa-worker.service || die "the worker did not start"
fi

say "Health"
if [ "$ROLE" = "server" ]; then
    for _ in $(seq 1 60); do
        if curl -sk --max-time 3 https://127.0.0.1:8080/readyz | grep -q '"status":"ready"'; then
            note "the server is ready"
            note "sign in as root / toor and change the password immediately"
            exit 0
        fi
        sleep 2
    done
    die "the server did not become ready; journalctl -u aagasa-server.service"
fi

note "the worker registers with the Server on its own; watch journalctl -u aagasa-worker.service"
