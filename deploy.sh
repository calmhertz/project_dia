#!/usr/bin/env bash
#
#   ./deploy.sh <server|worker|both> up
#   ./deploy.sh <server|worker|both> down
#   ./deploy.sh <server|worker|both> restart
#   ./deploy.sh <server|worker|both> rebuild
#   ./deploy.sh <server|worker|both> reset [--yes]

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVER_ENV="$ROOT/deploy/server/.env.server"
WORKER_ENV="$ROOT/deploy/worker/.env.worker"
DATA="${AAGASA_DATA_DIR:-$ROOT/data}"
NETWORK="aagasa"
PODS_BOTH=(aagasa-w aagasa-api aagasa-db)
PODS_SERVER=(aagasa-api aagasa-db)
PODS_WORKER=(aagasa-w)

say()  { printf '\n== %s\n' "$1"; }
note() { printf '   %s\n' "$1"; }
warn() { printf '!! %s\n' "$1" >&2; }
die()  { printf 'deploy: %s\n' "$1" >&2; exit 1; }

usage() {
    cat <<'EOF'
Usage: ./deploy.sh <server|worker|both> <up|down|restart|rebuild|reset>

  up        build and start
  down      stop and remove containers; keep data
  restart   down, then up
  rebuild   down, then up with fresh images
  reset     delete state, rebuild, start (--yes skips the prompt)
EOF
}

ask() {
    local answer=""
    if [[ -t 0 ]]; then
        read -r -p "   $1 [$2]: " answer
    fi
    printf '%s' "${answer:-$2}"
}

ask_number() {
    local value
    while true; do
        value="$(ask "$1" "$2")"
        if [[ "$value" =~ ^[0-9]+$ ]] && ((value >= $3 && value <= $4)); then
            printf '%s' "$value"
            return
        fi
        printf '   enter a whole number from %s to %s\n' "$3" "$4" >&2
        [[ -t 0 ]] || die "invalid default for: $1"
    done
}

env_value() { sed -n "s/^$2=//p" "$1" 2>/dev/null | tail -1; }
secret() { od -An -N24 -tx1 /dev/urandom | tr -d ' \n'; }

ensure_env_for() {
    local target="$1"
    local need_server=false need_worker=false
    case "$target" in
        server) need_server=true ;;
        worker) need_worker=true ;;
        both)   need_server=true; need_worker=true ;;
    esac
    [[ "$need_server" == true && -f "$SERVER_ENV" ]] && need_server=false
    [[ "$need_worker" == true && -f "$WORKER_ENV" ]] && need_worker=false
    [[ "$need_server" == false && "$need_worker" == false ]] && return

    say "Creating missing configuration"
    local shared
    shared="$(env_value "$SERVER_ENV" AAGASA_WORKER_SHARED_SECRET)"
    shared="${shared:-$(env_value "$WORKER_ENV" AAGASA_WORKER_SHARED_SECRET)}"
    shared="${shared:-$(secret)}"

    if [[ "$need_server" == true ]]; then
        [[ -d "$DATA/postgres" ]] \
            && die "$SERVER_ENV is missing but a database exists in $DATA/postgres"
        write_server_env "$(secret)" "$shared"
        note "wrote $SERVER_ENV"
    fi
    if [[ "$need_worker" == true ]]; then
        note "Station details (press Enter to keep the value in brackets):"
        write_worker_env "$shared"
        note "wrote $WORKER_ENV"
    fi
}

write_server_env() {
    (umask 077; cat > "$SERVER_ENV") <<EOF || die "cannot write $SERVER_ENV"
AAGASA_ENV=development
AAGASA_LOG_LEVEL=info
AAGASA_HTTP_ADDRESS=:8080
AAGASA_GRPC_ADDRESS=:9090
AAGASA_POSTGRES_URL=postgres://aagasa:$1@aagasa-db:5432/aagasa?sslmode=disable
AAGASA_REDIS_URL=redis://aagasa-db:6379/0
AAGASA_MONGO_URL=mongodb://aagasa-db:27017
AAGASA_MONGO_DATABASE=aagasa
AAGASA_PREDICTION_ADDRESS=127.0.0.1:9091
AAGASA_CELESTRAK_URL=
AAGASA_SATNOGS_URL=
AAGASA_TLE_REFRESH_ENABLED=true
AAGASA_RECORDINGS_DIR=/var/lib/aagasa/recordings
AAGASA_PIPELINES_DIR=/var/lib/aagasa/pipelines
AAGASA_WEB_DIR=/app/web
AAGASA_WORKER_SHARED_SECRET=$2
AAGASA_STARTUP_TIMEOUT=10
AAGASA_SESSION_TTL=43200
AAGASA_PUBLISH_ADDRESS=0.0.0.0
EOF
}

write_worker_env() {
    local name port baud azimuth elevation sdr
    name="$(ask "Worker name" "worker-1")"
    port="$(ask "Rotator serial port" "/dev/ttyUSB0")"
    baud="$(ask_number "Rotator baud rate" 9600 1 4000000)"
    azimuth="$(ask_number "Rotator park azimuth (0-359)" 0 0 359)"
    elevation="$(ask_number "Rotator park elevation (0-90)" 0 0 90)"
    sdr="$(ask_number "RTL-SDR device index" 0 0 15)"

    (umask 077; cat > "$WORKER_ENV") <<EOF || die "cannot write $WORKER_ENV"
AAGASA_LOG_LEVEL=info
AAGASA_WORKER_NAME=$name
AAGASA_SERVER_GRPC_ADDRESS=aagasa-api:9090
AAGASA_SERVER_TLS_ENABLED=false
AAGASA_WORKER_SHARED_SECRET=$1
AAGASA_STATE_DIR=/var/lib/aagasa/state
AAGASA_RECORDINGS_DIR=/var/lib/aagasa/recordings
AAGASA_ROTATOR_SERIAL_PORT=$port
AAGASA_ROTATOR_BAUD_RATE=$baud
AAGASA_ROTATOR_PARK_AZIMUTH=$azimuth
AAGASA_ROTATOR_PARK_ELEVATION=$elevation
AAGASA_SDR_DEVICE_INDEX=$sdr
AAGASA_RTL_TEST_BIN=rtl_test
AAGASA_SATDUMP_BIN=satdump
AAGASA_SATDUMP_PIPELINES_DIR=/usr/local/share/satdump/resources/pipelines
AAGASA_SATDUMP_LEGACY_CLI=true
AAGASA_HEARTBEAT_INTERVAL_SECONDS=5
EOF
}

postgres_password() {
    local url
    url="$(env_value "$SERVER_ENV" AAGASA_POSTGRES_URL)"
    url="${url#*://aagasa:}"
    printf '%s' "${url%%@*}"
}

ensure_stubs() {
    [[ -d "$ROOT/server/go/internal/gen/worker" && -d "$ROOT/worker/src/aagasa/worker" \
        && -d "$ROOT/server/python/src/aagasa/prediction" ]] && return

    say "Generating gRPC stubs"
    command -v protoc >/dev/null && command -v uv >/dev/null \
        || die "protoc and uv are needed to generate the gRPC stubs"
    local worker_protos=(proto/aagasa/worker/v1/worker.proto
        proto/aagasa/worker/v1/passplan.proto proto/aagasa/worker/v1/recording.proto)
    cd "$ROOT" || die "cannot enter $ROOT"
    mkdir -p server/go/internal/gen
    protoc -I proto \
        --go_out=server/go/internal/gen --go_opt=module=aagasa/internal/gen \
        --go-grpc_out=server/go/internal/gen --go-grpc_opt=module=aagasa/internal/gen \
        proto/aagasa/prediction/v1/prediction.proto "${worker_protos[@]}" \
        || die "protoc (Go) failed"
    (cd server/python && uv run python -m grpc_tools.protoc -I ../../proto \
        --python_out=src --grpc_python_out=src --pyi_out=src \
        ../../proto/aagasa/prediction/v1/prediction.proto) || die "protoc (prediction) failed"
    (cd worker && uv run python -m grpc_tools.protoc -I ../proto \
        --python_out=src --grpc_python_out=src --pyi_out=src \
        "${worker_protos[@]/#proto/../proto}") || die "protoc (worker) failed"
}

build_server() {
    [[ -f "$ROOT/client/build/web/index.html" && -f "$ROOT/client/build/web/main.dart.js" ]] \
        || die "no built web client; run 'flutter build web'"
    cd "$ROOT" || die "cannot enter $ROOT"
    say "Building server"
    podman build -f deploy/server/Containerfile -t aagasa-server:latest . \
        || die "server image build failed"
    say "Building prediction"
    podman build -f deploy/server/Containerfile.prediction -t aagasa-prediction:latest . \
        || die "prediction image build failed"
}

build_worker() {
    local arch containerfile
    arch="$(uname -m)"
    case "$arch" in
        x86_64|amd64) containerfile=deploy/worker/Containerfile.amd64deb13 ;;
        aarch64|arm64) containerfile=deploy/worker/Containerfile.rpi4b ;;
        *) die "unsupported worker architecture: $arch" ;;
    esac
    cd "$ROOT" || die "cannot enter $ROOT"
    say "Building worker"
    podman build -f "$containerfile" -t aagasa-worker:latest . \
        || die "worker image build failed"
}

build_for() {
    local target="$1"
    command -v podman >/dev/null || die "podman is required"
    ensure_stubs
    case "$target" in
        server) build_server ;;
        worker) build_worker ;;
        both)   build_server; build_worker ;;
    esac
}

wait_for() {
    local what="$1"; shift
    for _ in $(seq 60); do
        "$@" >/dev/null 2>&1 && return 0
        sleep 2
    done
    die "$what did not become ready"
}

server_ready() {
    local publish="${1:-127.0.0.1}"
    [[ "$publish" == "0.0.0.0" ]] && publish=127.0.0.1
    curl -fsS --max-time 3 "http://$publish:8080/readyz" | grep -q '"status":"ready"'
}

running_for() {
    local target="$1" pod pods=()
    case "$target" in
        server) pods=("${PODS_SERVER[@]}") ;;
        worker) pods=("${PODS_WORKER[@]}") ;;
        both)   pods=("${PODS_BOTH[@]}") ;;
    esac
    for pod in "${pods[@]}"; do
        [[ "$(podman pod inspect --format '{{.State}}' "$pod" 2>/dev/null)" == "Running" ]] \
            || return 1
    done
    return 0
}

start_for() {
    local target="$1"
    command -v podman >/dev/null || die "podman is required"
    if running_for "$target"; then
        note "already running; use './deploy.sh $target restart' to restart"
        return
    fi
    ensure_env_for "$target"
    build_for "$target"
    stop_for "$target" >/dev/null

    mkdir -p "$DATA"/{postgres,mongo,redis,recordings,pipelines,worker-state}
    podman network create "$NETWORK" >/dev/null 2>&1 || true

    if [[ "$target" == server || "$target" == both ]]; then
        say "Databases"
        local rendered
        rendered="$(mktemp)"
        sed -e "s|/var/lib/aagasa|$DATA|g" \
            -e "s|__POSTGRES_PASSWORD__|$(postgres_password)|" \
            "$ROOT/deploy/server/databases.yaml" > "$rendered"
        if ! podman kube play --network "$NETWORK" "$rendered" >/dev/null; then
            rm -f "$rendered"
            die "the database pod did not start"
        fi
        rm -f "$rendered"
        wait_for "postgres" podman exec aagasa-db-postgres pg_isready -U aagasa

        say "Database schema"
        local migrate=(podman run --rm --network "$NETWORK" --env-file "$SERVER_ENV"
            localhost/aagasa-server:latest aagasa-migrate)
        "${migrate[@]}" up >/dev/null || die "postgres migration failed"
        "${migrate[@]}" mongo-init >/dev/null || die "mongo init failed"

        say "Server"
        local publish
        publish="$(env_value "$SERVER_ENV" AAGASA_PUBLISH_ADDRESS)"
        publish="${publish:-0.0.0.0}"
        podman pod create --name aagasa-api --network "$NETWORK" \
            -p "$publish:8080:8080" >/dev/null || die "could not create the API pod"
        podman run -d --pod aagasa-api --name aagasa-prediction --restart=always \
            --env-file "$SERVER_ENV" localhost/aagasa-prediction:latest >/dev/null \
            || die "the prediction container did not start"
        podman run -d --pod aagasa-api --name aagasa-server --restart=always \
            --env-file "$SERVER_ENV" \
            -v "$DATA/recordings:/var/lib/aagasa/recordings:Z" \
            -v "$DATA/pipelines:/var/lib/aagasa/pipelines:Z" \
            localhost/aagasa-server:latest >/dev/null \
            || die "the server container did not start"
        wait_for "the server (podman logs aagasa-server)" server_ready "$publish"
    fi

    if [[ "$target" == worker || "$target" == both ]]; then
        say "Worker"
        local port devices=()
        port="$(env_value "$WORKER_ENV" AAGASA_ROTATOR_SERIAL_PORT)"
        [[ -n "$port" && -e "$port" ]] && devices+=(-v "$port:$port")
        [[ -d /dev/bus/usb ]] && devices+=(-v /dev/bus/usb:/dev/bus/usb)

        podman pod create --name aagasa-w --network "$NETWORK" >/dev/null \
            || die "could not create the worker pod"
        podman run -d --pod aagasa-w --name aagasa-worker --restart=always \
            --env-file "$WORKER_ENV" \
            -v "$DATA/worker-state:/var/lib/aagasa/state:Z" \
            -v "$DATA/recordings:/var/lib/aagasa/recordings:Z" \
            "${devices[@]}" localhost/aagasa-worker:latest >/dev/null \
            || die "the worker container did not start"
    fi

    say "Running"
    [[ "$target" != worker ]] && note "Web UI and API: http://$publish:8080"
    note "Data: $DATA"
    note "Logs: podman logs -f aagasa-server"
}

stop_for() {
    local target="$1" pod pods=()
    command -v podman >/dev/null || die "podman is required"
    case "$target" in
        server) pods=("${PODS_SERVER[@]}") ;;
        worker) pods=("${PODS_WORKER[@]}") ;;
        both)   pods=("${PODS_BOTH[@]}") ;;
    esac
    for pod in "${pods[@]}"; do
        podman pod exists "$pod" && podman pod rm -f "$pod" >/dev/null
    done
    note "stopped; data kept in $DATA"
}

reset_for() {
    local target="$1" yes="${2:-}"
    local paths=()
    case "$target" in
        server) paths=("$DATA/postgres" "$DATA/mongo" "$DATA/redis") ;;
        worker) paths=("$DATA/worker-state") ;;
        both)   paths=("$DATA/postgres" "$DATA/mongo" "$DATA/redis" "$DATA/worker-state") ;;
    esac

    if [[ "$yes" != "--yes" ]]; then
        [[ -t 0 ]] || die "reset deletes state; run './deploy.sh $target reset --yes' to confirm"
        case "$target" in
            server) printf 'This deletes the database, all accounts and the station setup.\n' ;;
            worker) printf "This deletes the worker's state in %s.\n" "$DATA/worker-state" ;;
            both)   printf "This deletes the database, accounts, station setup and worker state in %s.\n" "$DATA" ;;
        esac
        printf 'Recordings are kept.\n'
        local answer
        read -r -p "Type 'reset' to continue: " answer
        [[ "$answer" == "reset" ]] || die "cancelled"
    fi

    stop_for "$target"
    say "Deleting the $target state"
    rm -rf "${paths[@]}" || die "could not delete ${paths[*]}"
    note "deleted"
    start_for "$target"
}

TARGET="${1:-}"
ACTION="${2:-}"
EXTRA="${3:-}"

if [[ $# -eq 0 || "$TARGET" == "help" || "$TARGET" == "-h" || "$TARGET" == "--help" ]]; then
    usage
    [[ $# -gt 0 ]] || exit 1
    exit 0
fi
[[ $# -ge 2 ]] || { printf 'deploy: expected <server|worker|both> <subcommand>\n' >&2; usage >&2; exit 1; }
[[ $# -le 3 ]] || die "too many arguments"

case "$TARGET" in
    server|worker|both) ;;
    *) printf 'deploy: unknown target: %s\n' "$TARGET" >&2; usage >&2; exit 1 ;;
esac
case "$ACTION" in
    up|down|restart|rebuild|reset) ;;
    *) printf 'deploy: unknown subcommand: %s\n' "$ACTION" >&2; usage >&2; exit 1 ;;
esac
if [[ "$ACTION" == reset ]]; then
    [[ -z "$EXTRA" || "$EXTRA" == "--yes" ]] || die "reset only accepts --yes"
else
    [[ -z "$EXTRA" ]] || die "unexpected argument: $EXTRA"
fi

case "$ACTION" in
    up)               start_for "$TARGET" ;;
    down)             say "Stopping"; stop_for "$TARGET" ;;
    restart|rebuild)  say "Stopping"; stop_for "$TARGET"; start_for "$TARGET" ;;
    reset)            reset_for "$TARGET" "$EXTRA" ;;
esac
