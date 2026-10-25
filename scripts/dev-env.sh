#!/usr/bin/env bash
# A repeatable Aagasa environment (plan.md V17).
#
#   scripts/dev-env.sh up        databases, migrations, services, fixtures
#   scripts/dev-env.sh down      stop the services, leave the data
#   scripts/dev-env.sh reset     down, then delete the data as well
#   scripts/dev-env.sh status    what is running
#   scripts/dev-env.sh logs [server|worker|prediction]
#
# By default the Worker runs against **simulated hardware**, so the whole
# system works with nothing attached. To use the real rotator and dongle:
#
#   AAGASA_HARDWARE=real scripts/dev-env.sh up
#
# Several environments can exist side by side. Each name gets its own
# containers, ports and data, so a test run cannot disturb a developer's:
#
#   AAGASA_ENV_NAME=test AAGASA_PORT_OFFSET=100 scripts/dev-env.sh up
#
# Nothing here is a production deployment; that is deploy/ and plan.md V18.

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
NAME="${AAGASA_ENV_NAME:-dev}"
# Every port moves together, so one number keeps a whole environment apart.
OFFSET="${AAGASA_PORT_OFFSET:-0}"
RUN="${AAGASA_RUN_DIR:-$HOME/.local/share/aagasa/$NAME}"
LOGS="$RUN/logs"
HARDWARE="${AAGASA_HARDWARE:-simulated}"

PG_PORT=$((5432 + OFFSET))
REDIS_PORT=$((6379 + OFFSET))
MONGO_PORT=$((27017 + OFFSET))
HTTP_PORT=$((8080 + OFFSET))
GRPC_PORT=$((9090 + OFFSET))
PREDICTION_PORT=$((9091 + OFFSET))
WEB_PORT=$((5001 + OFFSET))

# Development credentials. Real deployments generate their own; these exist so
# the environment comes up the same way every time.
SHARED_SECRET="${AAGASA_WORKER_SHARED_SECRET:-aagasa-development-shared-secret-0001}"
ROOT_PASSWORD="${AAGASA_ROOT_PASSWORD:-development-root-password}"

PG="aagasa-$NAME-pg"
REDIS="aagasa-$NAME-redis"
MONGO="aagasa-$NAME-mongo"

export AAGASA_POSTGRES_URL="postgres://aagasa:aagasa@127.0.0.1:$PG_PORT/aagasa?sslmode=disable"
export AAGASA_REDIS_URL="redis://127.0.0.1:$REDIS_PORT/0"
export AAGASA_MONGO_URL="mongodb://127.0.0.1:$MONGO_PORT"
export AAGASA_HTTP_ADDRESS=":$HTTP_PORT"
export AAGASA_GRPC_ADDRESS=":$GRPC_PORT"
export AAGASA_PREDICTION_ADDRESS="127.0.0.1:$PREDICTION_PORT"
export AAGASA_WORKER_SHARED_SECRET="$SHARED_SECRET"
export AAGASA_RECORDINGS_DIR="$RUN/server/recordings"
export AAGASA_PIPELINES_DIR="$RUN/server/pipelines"
export AAGASA_TLE_REFRESH_ENABLED="${AAGASA_TLE_REFRESH_ENABLED:-true}"

say()  { printf '\n== %s\n' "$1"; }
note() { printf '   %s\n' "$1"; }
die()  { printf 'dev-env: %s\n' "$1" >&2; exit 1; }

container_running() { podman inspect -f '{{.State.Running}}' "$1" 2>/dev/null | grep -q true; }
pid_of() { [ -f "$RUN/$1.pid" ] && kill -0 "$(cat "$RUN/$1.pid")" 2>/dev/null && cat "$RUN/$1.pid"; }

# Services are started in their own process group and stopped as a group.
#
# `go run` and `uv run` both launch the real program as a child, so killing
# the recorded pid leaves the server or the Worker holding its port. Killing
# the group takes the wrapper and what it spawned.
start_background() {
    local name="$1"; shift
    if pid_of "$name" >/dev/null; then
        note "$name already running"
        return 0
    fi
    mkdir -p "$LOGS"
    setsid "$@" >"$LOGS/$name.log" 2>&1 &
    echo $! >"$RUN/$name.pid"
    sleep 1
    if ! pid_of "$name" >/dev/null; then
        die "$name failed to start; see $LOGS/$name.log"
    fi
    note "$name started"
}

stop_background() {
    local name="$1" pid
    pid="$(pid_of "$name")" || { note "$name not running"; return 0; }

    # Negative pid: the whole group, which is why setsid was used to start it.
    kill -TERM -- "-$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null
    for _ in $(seq 1 20); do
        kill -0 "$pid" 2>/dev/null || break
        sleep 0.5
    done
    kill -KILL -- "-$pid" 2>/dev/null
    rm -f "$RUN/$name.pid"
    note "$name stopped"
}

wait_for_http() {
    local url="$1" limit="${2:-60}" waited=0
    while [ "$waited" -lt "$limit" ]; do
        [ "$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 "$url")" = "200" ] && return 0
        sleep 1
        waited=$((waited + 1))
    done
    return 1
}

# Databases ------------------------------------------------------------------

start_databases() {
    say "Databases"
    local specs=(
        "$PG|docker.io/library/postgres:17-alpine|-e POSTGRES_USER=aagasa -e POSTGRES_PASSWORD=aagasa -e POSTGRES_DB=aagasa -p $PG_PORT:5432"
        "$REDIS|docker.io/library/redis:7-alpine|-p $REDIS_PORT:6379"
        "$MONGO|docker.io/library/mongo:7|-p $MONGO_PORT:27017"
    )
    for spec in "${specs[@]}"; do
        local name image options
        IFS='|' read -r name image options <<<"$spec"
        if container_running "$name"; then
            note "$name already running"
        elif podman inspect "$name" >/dev/null 2>&1; then
            podman start "$name" >/dev/null && note "$name restarted"
        else
            # shellcheck disable=SC2086
            podman run -d --name "$name" $options "$image" >/dev/null && note "$name created"
        fi
    done

    note "waiting for PostgreSQL"
    for _ in $(seq 1 60); do
        podman exec "$PG" pg_isready -U aagasa >/dev/null 2>&1 && return 0
        sleep 1
    done
    die "PostgreSQL did not become ready"
}

# Binaries, not `go run`.
#
# `go run` reports any nonzero exit as 1, which hides the seed's "already
# configured" answer, and it leaves the real program as a child process that
# outlives a kill. Building once solves both and starts faster.
BIN="$RUN/bin"

build() {
    say "Build"
    mkdir -p "$BIN"
    ( cd "$ROOT/server/go" && go build -o "$BIN/aagasa-server" ./cmd/aagasa-server ) || die "building the server failed"
    ( cd "$ROOT/server/go" && go build -o "$BIN/aagasa-migrate" ./cmd/aagasa-migrate ) || die "building the migrator failed"
    ( cd "$ROOT/server/go" && go build -o "$BIN/aagasa-seed" ./cmd/aagasa-seed ) || die "building the seeder failed"
    note "server, migrator and seeder built"
}

migrate() {
    say "Schema"
    "$BIN/aagasa-migrate" up >/dev/null || die "migration failed"
    "$BIN/aagasa-migrate" mongo-init >/dev/null || die "mongo init failed"
    note "up to date"
}

# Services -------------------------------------------------------------------

start_services() {
    say "Services"
    mkdir -p "$RUN" "$LOGS" "$AAGASA_RECORDINGS_DIR" "$AAGASA_PIPELINES_DIR"

    start_background prediction env -C "$ROOT/server/python" \
        AAGASA_PREDICTION_ADDRESS="127.0.0.1:$PREDICTION_PORT" uv run aagasa-prediction
    start_background server "$BIN/aagasa-server"

    if ! wait_for_http "http://127.0.0.1:$HTTP_PORT/readyz" 120; then
        die "the server did not become ready; see $LOGS/server.log"
    fi
    note "server ready on http://127.0.0.1:$HTTP_PORT"
}

start_worker() {
    say "Ground station ($HARDWARE hardware)"
    mkdir -p "$RUN/worker/state" "$RUN/worker/recordings"

    local rotator_port="/dev/ttyUSB0"
    local worker_path="$PATH"

    if [ "$HARDWARE" = "simulated" ]; then
        rotator_port="$RUN/rotator"
        start_background rotator python3 "$ROOT/scripts/simulated-hardware/rotator.py" --link "$rotator_port"
        # Wait for the link, or the Worker starts before its rotator exists.
        for _ in $(seq 1 20); do [ -e "$rotator_port" ] && break; sleep 0.5; done
        [ -e "$rotator_port" ] || die "the simulated rotator did not start"
        # The stand-ins shadow the real binaries for this process only.
        worker_path="$ROOT/scripts/simulated-hardware:$PATH"
        # And a fixed pipeline set, so what the station reports is the same on
        # a machine with SatDump installed and one without.
        export AAGASA_SATDUMP_PIPELINES_DIR="$ROOT/scripts/simulated-hardware/pipelines"
    fi

    AAGASA_WORKER_NAME=worker-1 \
    AAGASA_SERVER_GRPC_ADDRESS="127.0.0.1:$GRPC_PORT" \
    AAGASA_SERVER_TLS_ENABLED=false \
    AAGASA_STATE_DIR="$RUN/worker/state" \
    AAGASA_RECORDINGS_DIR="$RUN/worker/recordings" \
    AAGASA_ROTATOR_SERIAL_PORT="$rotator_port" \
    PATH="$worker_path" \
        start_background worker env -C "$ROOT/worker" uv run aagasa-worker
    note "rotator at $rotator_port"
}

seed() {
    say "Fixtures"
    AAGASA_ROTATOR_SERIAL_PORT="${1:-}" "$BIN/aagasa-seed" \
        --server "http://127.0.0.1:$HTTP_PORT" --root-password "$ROOT_PASSWORD"
    local result=$?

    case "$result" in
    0) return 0 ;;
    # An environment someone is already using has its own credentials, which
    # seeding cannot guess. That is not a reason to refuse to start it.
    3) note "already set up with its own credentials; fixtures left alone"; return 0 ;;
    *) die "seeding failed" ;;
    esac
}

serve_client() {
    say "Client"
    if [ ! -f "$ROOT/client/build/web/index.html" ]; then
        note "no web build; run: cd client && flutter build web --pwa-strategy=none"
        return 0
    fi
    start_background client env AAGASA_WEB_PORT="$WEB_PORT" python3 "$ROOT/scripts/serve-web.py"
    note "client on http://127.0.0.1:$WEB_PORT"
}

# Commands -------------------------------------------------------------------

case "${1:-up}" in
up)
    command -v podman >/dev/null || die "podman is required"
    mkdir -p "$RUN"
    build
    start_databases
    migrate
    start_services
    rotator_path="$RUN/rotator"
    [ "$HARDWARE" = "simulated" ] || rotator_path="/dev/ttyUSB0"
    start_worker
    seed "$rotator_path"
    serve_client
    say "Ready"
    note "server    http://127.0.0.1:$HTTP_PORT"
    note "client    http://127.0.0.1:$WEB_PORT"
    note "root      $ROOT_PASSWORD"
    note "logs      $LOGS"
    ;;
down)
    say "Stopping"
    for service in client worker rotator server prediction; do stop_background "$service"; done
    note "databases left running; use reset to remove them"
    ;;
reset)
    say "Stopping"
    for service in client worker rotator server prediction; do stop_background "$service"; done
    say "Removing data"
    podman rm -f "$PG" "$REDIS" "$MONGO" >/dev/null 2>&1
    rm -rf "$RUN/worker" "$RUN/server"
    note "containers and local state removed"
    ;;
status)
    say "Containers"
    for name in "$PG" "$REDIS" "$MONGO"; do
        container_running "$name" && note "$name running" || note "$name stopped"
    done
    say "Services"
    for service in prediction server rotator worker client; do
        if pid="$(pid_of "$service")"; then
            note "$service running (pid $pid)"
        elif [ "$service" = "rotator" ] && [ "$HARDWARE" != "simulated" ]; then
            note "rotator using real hardware"
        else
            note "$service stopped"
        fi
    done
    say "Health"
    note "$(curl -s --max-time 3 "http://127.0.0.1:$HTTP_PORT/readyz" || echo 'server unreachable')"
    ;;
logs)
    tail -f "$LOGS/${2:-server}.log"
    ;;
*)
    grep '^#' "$0" | sed 's/^# \{0,1\}//' | head -20
    exit 1
    ;;
esac
