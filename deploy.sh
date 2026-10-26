#!/usr/bin/env bash
# Aagasa powertool: podman pods + env files, plus development entrypoints.
#
#   ./deploy.sh help          # full command list
#   ./deploy.sh build         # build server, prediction and worker images
#   ./deploy.sh web-build     # build the Flutter client into deploy/server/web
#   ./deploy.sh up            # databases + server (UI on :8080) + worker
#   ./deploy.sh server-up     # databases + server only
#   ./deploy.sh worker-up     # worker only
#   ./deploy.sh down          # remove everything
#   ./deploy.sh status        # show pods and containers
#   ./deploy.sh logs [server|pred|worker|db|all]  # follow logs
#
# UI and API: http://127.0.0.1:8080
# gRPC:       127.0.0.1:9090
#
# The Go Server serves the built Flutter client from deploy/server/web, at the
# root of the same origin as /api. There is no separate web container, so the
# browser never makes a cross-origin request.
#
# Configuration (edit these, no other files needed):
#   deploy/server/.env.server    -> server env (DB URLs, secrets, ports)
#   deploy/worker/.env.worker    -> worker env (server address, hardware)
#   deploy/server/databases.yaml -> DB passwords/images
#
# Server and Worker run in separate pods on the "aagasa-net" Podman network,
# so they can talk to each other by pod name.
#
# The former Makefile's development targets (proto, test, migrate, fmt, lint,
# clean, deps, dev environment) also live here.

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVER_DIR="$ROOT/deploy/server"
WORKER_DIR="$ROOT/deploy/worker"
SERVER_ENV="$SERVER_DIR/.env.server"
WORKER_ENV="$WORKER_DIR/.env.worker"
SERVER_WEB="$SERVER_DIR/web"
CLIENT_WEB="$ROOT/client/build/web"
NETWORK="aagasa-net"
DB_POD="aagasa-databases"
SERVER_POD="aagasa-server-pod"
WORKER_POD="aagasa-worker-pod"

if [[ "$(id -u)" -eq 0 ]]; then
    STATE="$ROOT/.aagasa-data"
    mkdir -p "$STATE" 2>/dev/null
    if [[ ! -w "$STATE" ]]; then STATE="/var/lib/aagasa"; fi
else
    STATE="${AAGASA_STATE_ROOT:-$HOME/.local/share/aagasa}"
fi
RECORDINGS="$STATE/recordings"
PIPELINES="$STATE/pipelines"
WORKER_STATE="$STATE/worker-state"

say()  { printf '\n== %s\n' "$1"; }
note() { printf '   %s\n' "$1"; }
warn() { printf '!! %s\n' "$1" >&2; }
die()  { printf 'deploy: %s\n' "$1" >&2; exit 1; }

require_podman() {
    command -v podman >/dev/null || die "podman is required"
}

require_env() {
    [[ -f "$SERVER_ENV" ]] || die "missing $SERVER_ENV"
    [[ -f "$WORKER_ENV" ]] || die "missing $WORKER_ENV"
}

ensure_network() {
    if podman network exists "$NETWORK" >/dev/null 2>&1; then
        note "network $NETWORK exists"
    else
        podman network create "$NETWORK" >/dev/null
        note "network $NETWORK created"
    fi
}

remove_pod() {
    local pod="$1"
    if podman pod exists "$pod" >/dev/null 2>&1; then
        podman pod rm -f "$pod" >/dev/null 2>&1 || true
        note "removed pod $pod"
    fi
}

wait_for_postgres() {
    local tries=60
    for ((i = 1; i <= tries; i++)); do
        if podman exec aagasa-databases-postgres pg_isready -U aagasa >/dev/null 2>&1; then
            note "postgres ready"
            return 0
        fi
        sleep 2
    done
    die "postgres did not become ready (podman logs aagasa-databases-postgres)"
}

wait_for_readyz() {
    local tries=60
    for ((i = 1; i <= tries; i++)); do
        if curl -fsS --max-time 3 http://127.0.0.1:8080/readyz 2>/dev/null | grep -q '"status":"ready"'; then
            note "server ready at http://127.0.0.1:8080"
            return 0
        fi
        sleep 2
    done
    die "server did not become ready (./deploy.sh logs server)"
}

deploy_databases() {
    say "Databases"
    remove_pod "$DB_POD"
    # Rewrite data paths for rootless mode ($HOME/.local/share/aagasa).
    local rendered
    rendered="$(mktemp /tmp/aagasa-databases.XXXXXX.yaml)"
    sed "s|/var/lib/aagasa|$STATE|g" "$SERVER_DIR/databases.yaml" > "$rendered"
    podman kube play --network "$NETWORK" "$rendered"
    rm -f "$rendered"
    wait_for_postgres
}

run_migrations() {
    say "Database migrations"
    podman run --rm --network "$NETWORK" --env-file "$SERVER_ENV" \
        localhost/aagasa-server:latest aagasa-migrate up \
        || die "postgres migration failed"
    podman run --rm --network "$NETWORK" --env-file "$SERVER_ENV" \
        localhost/aagasa-server:latest aagasa-migrate mongo-init \
        || die "mongo init failed"
    note "schema is up to date"
}

deploy_server() {
    say "Server pod"
    remove_pod "$SERVER_POD"

    podman pod create --name "$SERVER_POD" --network "$NETWORK" \
        -p 8080:8080 -p 9090:9090 >/dev/null

    # Prediction service shares the pod network: the server calls 127.0.0.1:9091.
    podman run -d --pod "$SERVER_POD" --name aagasa-prediction \
        --env-file "$SERVER_ENV" \
        localhost/aagasa-prediction:latest >/dev/null

    # The built client is also baked into the image. Mounting the working tree
    # copy makes `./deploy.sh web-build` take effect without rebuilding Go.
    local webmount=()
    if [[ -f "$SERVER_WEB/index.html" ]]; then
        webmount=(-v "$SERVER_WEB:/app/web:ro,Z")
    else
        warn "no web client at $SERVER_WEB; the server will serve the API only"
    fi

    podman run -d --pod "$SERVER_POD" --name aagasa-server \
        --env-file "$SERVER_ENV" \
        -v "$RECORDINGS:/var/lib/aagasa/recordings:Z" \
        -v "$PIPELINES:/var/lib/aagasa/pipelines:Z" \
        "${webmount[@]}" \
        localhost/aagasa-server:latest >/dev/null

    note "containers started"
    wait_for_readyz
}

deploy_worker() {
    say "Worker pod"
    remove_pod "$WORKER_POD"

    # keep-id maps the unprivileged host user to the Worker's uid 10003, so
    # host ACLs on /dev/bus/usb/... (e.g. user:unag:rw-) still apply inside
    # the container. This must be set on the pod: Podman rejects per-container
    # user namespaces when joining a pod with an infra container.
    podman pod create --name "$WORKER_POD" --network "$NETWORK" \
        --userns=keep-id:uid=10003,gid=10003 >/dev/null

    local devices=()
    [[ -e /dev/ttyUSB0 ]] && devices+=(-v /dev/ttyUSB0:/dev/ttyUSB0)
    [[ -d /dev/bus/usb ]] && devices+=(-v /dev/bus/usb:/dev/bus/usb)
    if [[ ${#devices[@]} -eq 0 ]]; then
        warn "no rotator/SDR device found; worker will start but hardware calls will fail"
    fi

    podman run -d --pod "$WORKER_POD" --name aagasa-worker \
        --env-file "$WORKER_ENV" \
        -e UV_CACHE_DIR=/tmp/uv-cache \
        -e HOME=/home/aagasa \
        --privileged \
        --group-add keep-groups \
        -v "$WORKER_STATE:/var/lib/aagasa/state:Z" \
        -v "$RECORDINGS:/var/lib/aagasa/recordings:Z" \
        "${devices[@]}" \
        localhost/aagasa-worker:latest >/dev/null

    note "worker started"
}

build_web() {
    command -v flutter >/dev/null || die "flutter is required to build the web client"
    say "Building Flutter web client"
    ( cd "$ROOT/client" && flutter build web --release ) \
        || die "flutter web build failed"
    [[ -f "$CLIENT_WEB/index.html" ]] || die "flutter build produced no index.html"

    # deploy/server/web is what the Server serves and what its image copies.
    # Replace it wholesale so removed assets do not linger between builds.
    say "Publishing client to deploy/server/web"
    mkdir -p "$SERVER_WEB"
    find "$SERVER_WEB" -mindepth 1 -maxdepth 1 -exec rm -rf {} +
    cp -a "$CLIENT_WEB/." "$SERVER_WEB/"
    note "published $(du -sh "$SERVER_WEB" | cut -f1) web client"
}

cmd_web_build() {
    require_podman
    build_web
    if podman ps --format '{{.Names}}' | grep -qx aagasa-server; then
        say "Restarting server to pick up the new client"
        podman restart aagasa-server >/dev/null
        wait_for_readyz
    else
        note "server is not running; it will serve this build on the next start"
    fi
    say "Web UI and API: http://127.0.0.1:8080"
}

cmd_build() {
    require_podman
    cd "$ROOT" || die "cannot enter $ROOT"

    say "Building server image"
    podman build -f deploy/server/Containerfile -t aagasa-server:latest . \
        || die "server build failed"

    say "Building prediction image"
    podman build -f deploy/server/Containerfile.prediction -t aagasa-prediction:latest . \
        || die "prediction build failed"

    say "Building worker image"
    if [[ -f "$WORKER_DIR/satdump.deb" ]]; then
        podman build -f deploy/worker/Containerfile -t aagasa-worker:latest . \
            || die "worker build failed"
    else
        warn "deploy/worker/satdump.deb not found; using the simulated SatDump"
        warn "for a functional test. Put the real .deb there and rebuild for on-air use."
        local tmp_cf
        tmp_cf="$(mktemp /tmp/aagasa-worker.XXXXXX.Containerfile)"
        cat > "$tmp_cf" <<'CONTAINERFILE'
FROM docker.io/library/python:3.11-slim
WORKDIR /app
COPY --from=ghcr.io/astral-sh/uv:latest /uv /usr/local/bin/uv
RUN apt-get update \
    && apt-get install -y --no-install-recommends rtl-sdr \
    && rm -rf /var/lib/apt/lists/*
COPY scripts/simulated-hardware/satdump /usr/local/bin/satdump
COPY scripts/simulated-hardware/rtl_test /usr/local/bin/rtl_test-sim
RUN chmod +x /usr/local/bin/satdump /usr/local/bin/rtl_test-sim \
    && useradd --system --uid 10003 --groups dialout aagasa
COPY worker /app
RUN uv sync --frozen --no-dev
USER aagasa
ENV PATH="/app/.venv/bin:$PATH"
CMD ["aagasa-worker"]
CONTAINERFILE
        podman build -f "$tmp_cf" -t aagasa-worker:latest . \
            || { rm -f "$tmp_cf"; die "worker build failed"; }
        rm -f "$tmp_cf"
    fi

    say "Build complete"
}

prepare_dirs() {
    mkdir -p "$RECORDINGS" "$PIPELINES" "$WORKER_STATE"
    # The Worker pod maps the host user to container uid 10003 (keep-id).
    # Files left by an older deployment are owned by a subuid, so the Worker
    # can neither write nor chmod them. Reclaim the state directory through
    # the user namespace first; recordings only need directory write access.
    podman unshare chown -R 0:0 "$WORKER_STATE" 2>/dev/null || true
    # Rootless Podman maps some container uids to subuids on the host, so
    # world-writable data dirs are the simple way to let both containers
    # write. Suitable for a single-host deployment.
    chmod -R a+rwX "$RECORDINGS" "$PIPELINES" "$WORKER_STATE" 2>/dev/null || true
}

cmd_up() {
    require_podman; require_env
    prepare_dirs
    ensure_network
    deploy_databases
    run_migrations
    deploy_server
    deploy_worker
    say "Deployment complete"
    note "Web UI + API: http://127.0.0.1:8080"
    note "gRPC:         127.0.0.1:9090"
    note "Data:         $STATE"
    note "Logs:         ./deploy.sh logs"
}

cmd_server_up() {
    require_podman; require_env
    prepare_dirs
    ensure_network
    deploy_databases
    run_migrations
    deploy_server
    say "Server is up: http://127.0.0.1:8080  (UI + API)"
}

cmd_worker_up() {
    require_podman; require_env
    prepare_dirs
    ensure_network
    deploy_worker
    say "Worker is up"
}

cmd_down() {
    require_podman
    say "Stopping everything"
    for pod in "$WORKER_POD" "$SERVER_POD" "$DB_POD"; do
        remove_pod "$pod"
    done
    podman network rm "$NETWORK" >/dev/null 2>&1 || true
    say "Stopped"
}

cmd_status() {
    require_podman
    say "Pods"
    podman pod ps --filter name=aagasa || true
    say "Containers"
    podman ps -a --filter name=aagasa --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}" || true
    say "Health"
    if curl -fsS --max-time 3 http://127.0.0.1:8080/readyz >/dev/null 2>&1; then
        note "server: reachable"
    else
        note "server: unreachable"
    fi
    if curl -fsS --max-time 3 http://127.0.0.1:8080/ >/dev/null 2>&1; then
        note "web UI: reachable at http://127.0.0.1:8080"
    else
        note "web UI: unreachable"
    fi
    if podman ps --format '{{.Names}}' | grep -qx aagasa-worker; then
        note "worker: running"
    else
        note "worker: not running"
    fi
}

cmd_logs() {
    require_podman
    local target="${1:-all}"
    case "$target" in
        server)  podman logs -f aagasa-server ;;
        pred)    podman logs -f aagasa-prediction ;;
        worker)  podman logs -f aagasa-worker ;;
        db|databases)
            podman logs -f aagasa-databases-postgres &
            podman logs -f aagasa-databases-redis &
            podman logs -f aagasa-databases-mongo &
            wait
            ;;
        all)
            podman logs -f aagasa-server &
            podman logs -f aagasa-prediction &
            podman logs -f aagasa-worker &
            podman logs -f aagasa-databases-postgres &
            wait
            ;;
        *) die "usage: $0 logs [server|pred|worker|db|all]" ;;
    esac
}

# Development entrypoints (former Makefile targets) --------------------------

cmd_deps() {
    require_podman
    "$ROOT/scripts/install-dependencies.sh"
}

cmd_deps_check() {
    "$ROOT/scripts/install-dependencies.sh" --check
}

cmd_proto() {
    cd "$ROOT" || die "cannot enter $ROOT"
    say "Regenerating protobuf/gRPC stubs"
    mkdir -p server/go/internal/gen
    protoc -I proto \
        --go_out=server/go/internal/gen --go_opt=module=aagasa/internal/gen \
        --go-grpc_out=server/go/internal/gen --go-grpc_opt=module=aagasa/internal/gen \
        proto/aagasa/prediction/v1/prediction.proto \
        proto/aagasa/worker/v1/worker.proto proto/aagasa/worker/v1/passplan.proto \
        proto/aagasa/worker/v1/recording.proto \
        || die "protoc (Go) failed"
    (cd server/python && uv run python -m grpc_tools.protoc -I ../../proto \
        --python_out=src --grpc_python_out=src --pyi_out=src \
        ../../proto/aagasa/prediction/v1/prediction.proto) \
        || die "protoc (server python) failed"
    (cd worker && uv run python -m grpc_tools.protoc -I ../proto \
        --python_out=src --grpc_python_out=src --pyi_out=src \
        ../proto/aagasa/worker/v1/worker.proto ../proto/aagasa/worker/v1/passplan.proto \
        ../proto/aagasa/worker/v1/recording.proto) \
        || die "protoc (worker) failed"
    note "stubs regenerated; they are gitignored, do not commit them"
}

cmd_test() {
    cd "$ROOT" || die "cannot enter $ROOT"
    local failed=0
    say "go server"
    (cd server/go && go test ./...) || failed=1
    say "server python"
    (cd server/python && uv run pytest -q) || failed=1
    say "worker"
    (cd worker && uv run pytest -q) || failed=1
    say "client"
    (cd client && flutter test) || failed=1
    if [[ "$failed" -ne 0 ]]; then
        die "test failures"
    fi
    note "all suites passed"
}

# Requires AAGASA_TEST_POSTGRES_URL, AAGASA_TEST_REDIS_URL and
# AAGASA_TEST_MONGO_URL. Without them the store tests skip.
cmd_test_integration() {
    (cd "$ROOT/server/go" && go test ./internal/store/... -count=1)
}

cmd_migrate() {
    # The Go migrator reads AAGASA_POSTGRES_URL/AAGASA_MONGO_URL; source the
    # Server env so the command works standalone against the running databases.
    if [[ -f "$SERVER_ENV" ]]; then
        set -a
        . "$SERVER_ENV"
        set +a
    fi
    cd "$ROOT/server/go" || die "cannot enter $ROOT/server/go"
    say "PostgreSQL migrations"
    go run ./cmd/aagasa-migrate up || die "postgres migration failed"
    say "MongoDB init"
    go run ./cmd/aagasa-migrate mongo-init || die "mongo init failed"
    note "schema is up to date"
}

cmd_fmt() {
    cd "$ROOT" || die "cannot enter $ROOT"
    (cd server/go && gofmt -w .)
    (cd client && dart format lib test)
}

cmd_lint() {
    cd "$ROOT" || die "cannot enter $ROOT"
    local failed=0
    say "go vet"
    (cd server/go && go vet ./...) || failed=1
    say "flutter analyze"
    (cd client && flutter analyze) || failed=1
    if [[ "$failed" -ne 0 ]]; then
        die "lint failures"
    fi
}

# Removes generated code and build output; regenerated by proto and
# web-build. Never removes tracked files.
cmd_clean() {
    cd "$ROOT" || die "cannot enter $ROOT"
    rm -rf server/go/internal/gen server/python/src/aagasa worker/src/aagasa
    rm -rf client/build
    note "generated code and build output removed"
}

# The host-run development environment (no Podman), with simulated hardware
# unless AAGASA_HARDWARE=real.
cmd_dev() {
    local action="${1:-up}"
    "$ROOT/scripts/dev-env.sh" "$action"
}

cmd_integration_test() {
    "$ROOT/scripts/integration-test.sh"
}

cmd_drills() {
    "$ROOT/scripts/failure-drills.sh"
}

cmd_acceptance() {
    "$ROOT/scripts/acceptance.sh"
}

help() {
    cat <<'EOF'
Deploy (everything on the host runs through this script):

  ./deploy.sh build             # build server, prediction and worker images
  ./deploy.sh web-build         # build the Flutter client into deploy/server/web
  ./deploy.sh up                # databases + server (UI on :8080) + worker
  ./deploy.sh server-up         # databases + server only
  ./deploy.sh worker-up         # worker only
  ./deploy.sh down              # remove everything
  ./deploy.sh status            # show pods, containers and health
  ./deploy.sh logs [server|pred|worker|db|all]  # follow logs

Development (former Makefile targets):

  ./deploy.sh deps [--check]    # install (or only report) host dependencies
  ./deploy.sh proto             # regenerate protobuf/gRPC stubs after clone/changes
  ./deploy.sh test              # test every component, no databases needed
  ./deploy.sh test-integration  # store tests against real databases
  ./deploy.sh migrate           # apply PostgreSQL schema and MongoDB init (dev)
  ./deploy.sh fmt               # gofmt + dart format
  ./deploy.sh lint              # go vet + flutter analyze
  ./deploy.sh clean             # remove generated code and build output
  ./deploy.sh dev-up|dev-down|dev-reset|dev-status
                                # simulated-hardware environment via scripts/dev-env.sh
  ./deploy.sh integration-test  # drive a running environment's whole API
  ./deploy.sh drills            # failure drills against a running environment
  ./deploy.sh acceptance        # acceptance scenarios against a running environment
EOF
}

case "${1:-}" in
    build)           cmd_build ;;
    web-build)       cmd_web_build ;;
    up)              cmd_up ;;
    server-up)       cmd_server_up ;;
    worker-up)       cmd_worker_up ;;
    down)            cmd_down ;;
    status)          cmd_status ;;
    logs)            cmd_logs "${2:-all}" ;;
    deps)            shift; cmd_deps "$@" ;;
    deps-check)      cmd_deps_check ;;
    proto)           cmd_proto ;;
    test)            cmd_test ;;
    test-integration) cmd_test_integration ;;
    migrate)         cmd_migrate ;;
    fmt)             cmd_fmt ;;
    lint)            cmd_lint ;;
    clean)           cmd_clean ;;
    dev-up)          cmd_dev up ;;
    dev-down)        cmd_dev down ;;
    dev-reset)       cmd_dev reset ;;
    dev-status)      cmd_dev status ;;
    integration-test) cmd_integration_test ;;
    drills)          cmd_drills ;;
    acceptance)      cmd_acceptance ;;
    help|-h|--help)  help ;;
    *)               help; exit 1 ;;
esac
