#!/usr/bin/env bash
# Deploy Aagasa (plan.md V18).
#
#   ./deploy.sh server   # control plane on this host
#   ./deploy.sh worker   # ground station on this host
#   ./deploy.sh both     # everything on one host
#   ./deploy.sh down     # stop whatever this project runs on this host
#
# One command per host, driven by podman-compose. It builds the images it
# needs, creates the directory layout, generates secrets that do not yet
# exist, applies the schema and starts the services. Idempotent: run it again
# after changing anything and it converges.
#
# Rootless by design: Podman runs as the invoking user (nothing binds a port
# below 1024), every byte of runtime data lives under deployment/ and is bind
# mounted into the containers (deployment/data, deployment/tls, deployment/web), and there
# is no sudo anywhere in the flow. Container root maps to the invoking user,
# so what the containers write is owned by you.
#
# Building the Worker image needs deployment/worker/satdump.deb present; download
# the SatDump release package for the target architecture and place it there
# (deployment/README.md). Serving the web client needs a Flutter release build at
# client/build/web; run `cd client && flutter build web --release` first.

set -uo pipefail

ROLE="${1:-}"
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY="$REPO/deployment"
ENVF="$DEPLOY/.env"
DATA="$DEPLOY/data"
WEB="$DEPLOY/web"
TLS_DIR="$DEPLOY/tls"

say()  { printf '\n== %s\n' "$1"; }
note() { printf '   %s\n' "$1"; }
die()  { printf 'deploy: %s\n' "$1" >&2; exit 1; }

# SELinux (Fedora etc.) is enforcing by default, and podman-compose drops the
# `z` volume option when it mounts the bind dirs, so the containers depend on
# the host-side labels. Make every bind-mounted path container_file_t so the
# containers can read and write them; re-run whenever a directory is recreated.
relabel_selinux() {
    command -v getenforce >/dev/null 2>&1 || return 0
    [ "$(getenforce 2>/dev/null)" = "Enforcing" ] || [ "$(getenforce 2>/dev/null)" = "Permissive" ] || return 0
    local dir
    for dir in "$WEB" "$TLS_DIR" \
        "$DATA/postgres" "$DATA/redis" "$DATA/mongo" \
        "$DATA/recordings" "$DATA/pipelines" "$DATA/state" "$DATA/worker-recordings" \
        "$DEPLOY/nginx"; do
        [ -e "$dir" ] || continue
        chcon -R -t container_file_t "$dir" 2>/dev/null || note "warning: could not relabel $dir (SELinux may block the bind mount)"
    done
}

[ "$ROLE" = "server" ] || [ "$ROLE" = "worker" ] || [ "$ROLE" = "both" ] || [ "$ROLE" = "down" ] \
    || die "usage: deploy.sh server|worker|both|down"

command -v podman >/dev/null 2>&1 || die "podman is required"
command -v podman-compose >/dev/null 2>&1 || die "podman-compose is required (scripts/install-dependencies.sh)"
command -v openssl >/dev/null 2>&1 || die "openssl is required, to generate secrets"

# Rootless: no root check. Everything this project writes lives in the tree.

if [ "$ROLE" = "down" ]; then
    say "Down"
    ( cd "$DEPLOY" && podman-compose down ) || die "podman-compose down failed"
    note "containers and network stopped; data under $DATA is kept"
    exit 0
fi

# Environment ----------------------------------------------------------------

say "Environment"
# Durable layout, all of it inside deployment/ so the deployment is one directory
# (deployment/README.md "Layout on disk"). Everything is owned by the invoking
# user; the containers run as root, which rootless Podman maps back to that
# same user, so there is nothing to chown.
install -d "$DATA"
install -d "$DATA/postgres" "$DATA/mongo"
install -d "$DATA/state" "$DATA/worker-recordings"
install -d "$DATA/recordings" "$DATA/pipelines"
if [ ! -f "$ENVF" ]; then
    if [ "$ROLE" = "worker" ]; then
        die "$ENVF is missing; copy the Server's deployment/.env here (it carries both the shared secret and the datastore password the compose file requires)"
    fi
    cp "$DEPLOY/.env.example" "$ENVF"
    chmod 0600 "$ENVF"
    # Generate the two secrets the stack cannot invent for itself. The URL is
    # assembled inside compose.yaml from the password, so the two cannot drift.
    password="$(openssl rand -hex 24)"
    secret="$(openssl rand -hex 32)"
    sed -i "s/^AAGASA_POSTGRES_PASSWORD=.*/AAGASA_POSTGRES_PASSWORD=$password/" "$ENVF"
    sed -i "s/^AAGASA_WORKER_SHARED_SECRET=.*/AAGASA_WORKER_SHARED_SECRET=$secret/" "$ENVF"
    note "$ENVF created with fresh secrets"
else
    note "$ENVF already present; secrets kept"
fi
# shellcheck disable=SC1091
set -a; . "$ENVF"; set +a

[ -n "${AAGASA_POSTGRES_PASSWORD:-}" ] || die "AAGASA_POSTGRES_PASSWORD is empty in $ENVF"
[ -n "${AAGASA_WORKER_SHARED_SECRET:-}" ] || die "AAGASA_WORKER_SHARED_SECRET is empty in $ENVF"
[ "${#AAGASA_WORKER_SHARED_SECRET}" -ge 32 ] \
    || die "AAGASA_WORKER_SHARED_SECRET must be at least 32 characters (worker-spec)"

# Certificate ----------------------------------------------------------------

if [ "$ROLE" != "worker" ]; then
    say "TLS"
    install -d -m 0700 "$TLS_DIR"
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
    # The Server runs as root inside its image, which rootless Podman maps to
    # the invoking user: owner-only is exactly right, and no chown is needed.
    chmod 0600 "$TLS_DIR/tls.key"
    chmod 0644 "$TLS_DIR/tls.crt"
fi

# Ground station -------------------------------------------------------------

if [ "$ROLE" != "server" ]; then
    say "Ground station"
    # The Worker trusts the Server's self-signed certificate through gRPC's
    # GRPC_DEFAULT_SSL_ROOTS_FILE_PATH. The file must exist before the Worker
    # starts; on a dedicated station host copy it from the Server.
    [ -f "$TLS_DIR/tls.crt" ] || die "no $TLS_DIR/tls.crt; copy the Server's certificate here"
    [ -f "$DEPLOY/worker/satdump.deb" ] || die "deployment/worker/satdump.deb is missing; download the SatDump release package for this architecture (deployment/README.md)"
fi

# Web client -----------------------------------------------------------------

if [ "$ROLE" != "worker" ]; then
    [ -f "$REPO/client/build/web/index.html" ] \
        || die "no web build; run: cd client && flutter build web --release"
    # The built client is copied into deployment/web so the whole deployment,
    # including what nginx serves, is one directory (compose.yaml mounts it).
    rm -rf "$WEB" && mkdir -p "$WEB" && cp -a "$REPO/client/build/web/." "$WEB/"
fi

# Bind-mounted directories must carry SELinux container_file_t labels on
# enforcing hosts (podman-compose drops the compose `z` option); relabel once
# everything that should exist by now is in place.
relabel_selinux

# Generated protobuf stubs ---------------------------------------------------

# server/python imports the compiled stubs it links against at import time, and
# go build compiles this repo's own modules (aagasa/internal/gen), so if the
# stubs are missing nothing in the stack runs. They are not committed: make
# proto regenerates them (proto/ is the single source of truth). When the
# stubs are present the build just uses them.
missing_stubs=()
[ -d "$REPO/server/go/internal/gen" ] || missing_stubs+=(server/go/internal/gen)
[ -d "$REPO/server/python/src/aagasa" ] || missing_stubs+=(server/python/src/aagasa)
[ -d "$REPO/worker/src/aagasa" ] || missing_stubs+=(worker/src/aagasa)
if [ "${#missing_stubs[@]}" -gt 0 ]; then
    die "generated protobuf stubs are missing (${missing_stubs[*]}); run 'make proto' first (protoc + protoc-gen-go + grpc_tools, see scripts/install-dependencies.sh)"
fi
note "generated protobuf stubs present"

# Build ----------------------------------------------------------------------

WANT_BUILD=()
if [ "$ROLE" != "worker" ]; then
    WANT_BUILD+=(aagasa-prediction aagasa-server)
fi
if [ "$ROLE" != "server" ]; then
    WANT_BUILD+=(aagasa-worker)
fi

say "Build"
for image in "${WANT_BUILD[@]}"; do
    if podman image exists "localhost/$image:latest" 2>/dev/null; then
        note "$image:latest already built; remove it to force a rebuild"
        continue
    fi
    case "$image" in
        aagasa-prediction) containerfile="$DEPLOY/server/Containerfile.prediction";;
        aagasa-server)     containerfile="$DEPLOY/server/Containerfile";;
        aagasa-worker)     containerfile="$DEPLOY/worker/Containerfile";;
    esac
    note "building $image"
    podman build -f "$containerfile" -t "localhost/$image:latest" "$REPO" \
        || die "building $image failed"
    note "built $image"
done

# Services -------------------------------------------------------------------

cd "$DEPLOY" || die "cannot find $DEPLOY"

if [ "$ROLE" != "worker" ]; then
    say "Control plane"
    podman-compose up -d postgres redis mongo || die "the databases did not start"

    note "waiting for the datastores"
    for _ in $(seq 1 60); do
        podman exec aagasa-databases-postgres pg_isready -U aagasa >/dev/null 2>&1 && break
        sleep 2
    done

    say "Schema"
    # The migrator joins the compose network and reaches the datastores by
    # service name, so no loopback publishing and no host networking is needed
    # (the Server itself reaches them the same way).
    podman run --rm --network aagasa \
        -e "AAGASA_POSTGRES_URL=postgres://aagasa:${AAGASA_POSTGRES_PASSWORD}@postgres:5432/aagasa?sslmode=disable" \
        -e "AAGASA_MONGO_URL=mongodb://mongo:27017" \
        "localhost/aagasa-server:latest" aagasa-migrate up \
        || die "schema migration failed"
    podman run --rm --network aagasa \
        -e "AAGASA_POSTGRES_URL=postgres://aagasa:${AAGASA_POSTGRES_PASSWORD}@postgres:5432/aagasa?sslmode=disable" \
        -e "AAGASA_MONGO_URL=mongodb://mongo:27017" \
        "localhost/aagasa-server:latest" aagasa-migrate mongo-init \
        || die "mongo init failed"
    note "up to date"

    say "Services"
    # deployment/web is recreated above; the old nginx container still holds a
    # bind mount to the removed directory, so force a fresh one that mounts the
    # new files (restart alone would keep the stale mount).
    podman-compose up -d prediction server || die "the server did not start"
    podman-compose up -d --force-recreate nginx || die "the web container did not start"
fi

if [ "$ROLE" != "server" ]; then
    say "Ground station"
    # On `both` the Server is already up by here, so the Worker resolves it on
    # the compose network; on a dedicated station host AAGASA_SERVER_GRPC_ADDRESS
    # points it at the real address.
    podman-compose up -d worker || die "the worker did not start"
fi

say "Health"
if [ "$ROLE" != "worker" ]; then
    for _ in $(seq 1 60); do
        if curl -sk --max-time 3 https://127.0.0.1:8080/readyz | grep -q '"status":"ready"'; then
            note "the server is ready"
            note "web client on http://127.0.0.1:50000"
            note "sign in as root / toor and change the password immediately"
            exit 0
        fi
        sleep 2
    done
    die "the server did not become ready; podman-compose -f $DEPLOY/compose.yaml logs server"
fi

note "the worker registers with the Server on its own; watch its logs with podman logs -f aagasa-worker"