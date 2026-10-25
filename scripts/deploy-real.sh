#!/usr/bin/env bash
# Aagasa real-hardware deployment helper.
# Usage:
#   scripts/deploy-real.sh --server    # start server + databases on this machine
#   scripts/deploy-real.sh --worker    # start worker (needs server reachable)
#   scripts/deploy-real.sh --both      # start server + worker on this machine
#   scripts/deploy-real.sh --stop      # stop everything
#   scripts/deploy-real.sh --status    # show status
#   scripts/deploy-real.sh --hwtest    # verify hardware (rotator, SDR, SatDump)
#
# Config files (must exist):
#   deploy/server/.env.server    # server env vars
#   deploy/worker/.env.worker    # worker env vars
#
# The script runs binaries directly (not containers) for fastest iteration.
# For production, use deploy/ Containerfiles + Quadlet.

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVER_ENV="${ROOT}/deploy/server/.env.server"
WORKER_ENV="${ROOT}/deploy/worker/.env.worker"
RUN_DIR="${HOME}/.local/share/aagasa/real"
BIN_DIR="${RUN_DIR}/bin"
LOG_DIR="${RUN_DIR}/logs"

SERVER_PID_FILE="${RUN_DIR}/server.pid"
WORKER_PID_FILE="${RUN_DIR}/worker.pid"
PREDICTION_PID_FILE="${RUN_DIR}/prediction.pid"
PG_PID_FILE="${RUN_DIR}/pg.pid"
REDIS_PID_FILE="${RUN_DIR}/redis.pid"
MONGO_PID_FILE="${RUN_DIR}/mongo.pid"

PG_PORT=5432
REDIS_PORT=6379
MONGO_PORT=27017
HTTP_PORT=8080
GRPC_PORT=9090
PREDICTION_PORT=9091

say() { printf '\n== %s\n' "$1"; }
note() { printf '   %s\n' "$1"; }
warn() { printf '!! %s\n' "$1" >&2; }
die() { printf 'deploy-real: %s\n' "$1" >&2; exit 1; }

require_file() {
    [[ -f "$1" ]] || die "missing config file: $1 (copy from .env.example and fill in)"
}

load_env() {
    set -a
    # shellcheck disable=SC1090
    source "$1"
    set +a
}

pid_running() {
    [[ -f "$1" ]] && kill -0 "$(cat "$1")" 2>/dev/null
}

stop_pid() {
    local pid_file="$1" name="$2"
    if pid_running "$pid_file"; then
        local pid=$(cat "$pid_file")
        kill -TERM -- "-$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null
        for _ in {1..20}; do kill -0 "$pid" 2>/dev/null || break; sleep 0.5; done
        kill -KILL -- "-$pid" 2>/dev/null
        rm -f "$pid_file"
        note "$name stopped"
    else
        note "$name not running"
    fi
}

wait_for_http() {
    local url="$1" limit="${2:-60}" waited=0
    while [[ $waited -lt $limit ]]; do
        [[ "$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 "$url" 2>/dev/null)" == "200" ]] && return 0
        sleep 1
        waited=$((waited + 1))
    done
    return 1
}

start_databases() {
    say "Starting databases"
    mkdir -p "${RUN_DIR}/pgdata" "${RUN_DIR}/redisdata" "${RUN_DIR}/mongodata"

    # PostgreSQL
    if pid_running "$PG_PID_FILE"; then
        note "postgres already running"
    else
        pg_ctl -D "${RUN_DIR}/pgdata" -l "${LOG_DIR}/postgres.log" start >/dev/null 2>&1 || {
            initdb -D "${RUN_DIR}/pgdata" -U aagasa >/dev/null 2>&1
            echo "host all all 0.0.0.0/0 trust" >> "${RUN_DIR}/pgdata/pg_hba.conf"
            echo "listen_addresses = '*'" >> "${RUN_DIR}/pgdata/postgresql.conf"
            pg_ctl -D "${RUN_DIR}/pgdata" -l "${LOG_DIR}/postgres.log" start
        }
        echo $! > "$PG_PID_FILE"
        note "postgres started on port $PG_PORT"
    fi

    # Redis
    if pid_running "$REDIS_PID_FILE"; then
        note "redis already running"
    else
        redis-server --port $REDIS_PORT --daemonize yes --pidfile "$REDIS_PID_FILE" --logfile "${LOG_DIR}/redis.log" --dir "${RUN_DIR}/redisdata"
        note "redis started on port $REDIS_PORT"
    fi

    # MongoDB
    if pid_running "$MONGO_PID_FILE"; then
        note "mongo already running"
    else
        mongod --port $MONGO_PORT --dbpath "${RUN_DIR}/mongodata" --logpath "${LOG_DIR}/mongo.log" --pidfilepath "$MONGO_PID_FILE" --fork
        note "mongo started on port $MONGO_PORT"
    fi

    # Wait for postgres
    for _ in {1..30}; do
        pg_isready -h 127.0.0.1 -p $PG_PORT -U aagasa >/dev/null 2>&1 && break
        sleep 1
    done
    pg_isready -h 127.0.0.1 -p $PG_PORT -U aagasa >/dev/null 2>&1 || die "postgres not ready"
}

build_binaries() {
    say "Building binaries"
    mkdir -p "$BIN_DIR"
    (cd "$ROOT/server/go" && go build -o "$BIN_DIR/aagasa-server" ./cmd/aagasa-server) || die "server build failed"
    (cd "$ROOT/server/go" && go build -o "$BIN_DIR/aagasa-migrate" ./cmd/aagasa-migrate) || die "migrate build failed"
    (cd "$ROOT/server/go" && go build -o "$BIN_DIR/aagasa-seed" ./cmd/aagasa-seed) || die "seed build failed"
    note "server binaries built"
}

migrate_and_seed() {
    say "Database migration & seed"
    "$BIN_DIR/aagasa-migrate" up || die "migration failed"
    "$BIN_DIR/aagasa-migrate" mongo-init || die "mongo init failed"
    "$BIN_DIR/aagasa-seed" --server "http://127.0.0.1:$HTTP_PORT" --root-password "$AAGASA_ROOT_PASSWORD" || true
    note "database ready"
}

start_prediction() {
    say "Starting prediction service"
    if pid_running "$PREDICTION_PID_FILE"; then
        note "prediction already running"
        return
    fi
    mkdir -p "$LOG_DIR"
    setsid env -C "$ROOT/server/python" \
        AAGASA_PREDICTION_ADDRESS="127.0.0.1:$PREDICTION_PORT" \
        AAGASA_LOG_LEVEL="$AAGASA_LOG_LEVEL" \
        uv run aagasa-prediction >"${LOG_DIR}/prediction.log" 2>&1 &
    echo $! > "$PREDICTION_PID_FILE"
    sleep 2
    pid_running "$PREDICTION_PID_FILE" || die "prediction failed to start; see ${LOG_DIR}/prediction.log"
    note "prediction running on 127.0.0.1:$PREDICTION_PORT"
}

start_server() {
    say "Starting server"
    if pid_running "$SERVER_PID_FILE"; then
        note "server already running"
        return
    fi
    mkdir -p "$LOG_DIR"
    setsid "$BIN_DIR/aagasa-server" >"${LOG_DIR}/server.log" 2>&1 &
    echo $! > "$SERVER_PID_FILE"
    if ! wait_for_http "http://127.0.0.1:$HTTP_PORT/readyz" 120; then
        die "server did not become ready; see ${LOG_DIR}/server.log"
    fi
    note "server ready on http://127.0.0.1:$HTTP_PORT"
}

start_worker() {
    say "Starting worker (real hardware)"
    if pid_running "$WORKER_PID_FILE"; then
        note "worker already running"
        return
    fi

    # Verify hardware devices exist
    local rotator_port=$(grep '^AAGASA_ROTATOR_SERIAL_PORT=' "$WORKER_ENV" | cut -d= -f2-)
    [[ -c "$rotator_port" ]] || warn "rotator port $rotator_port not found (will retry on connect)"

    if ! command -v rtl_test >/dev/null; then
        warn "rtl_test not in PATH; RTL-SDR detection will fail"
    fi
    if ! command -v satdump >/dev/null; then
        warn "satdump not in PATH; capture will fail"
    fi

    mkdir -p "$LOG_DIR"
    setsid env -C "$ROOT/worker" \
        AAGASA_WORKER_NAME="$AAGASA_WORKER_NAME" \
        AAGASA_SERVER_GRPC_ADDRESS="$AAGASA_SERVER_GRPC_ADDRESS" \
        AAGASA_SERVER_TLS_ENABLED="$AAGASA_SERVER_TLS_ENABLED" \
        AAGASA_WORKER_SHARED_SECRET="$AAGASA_WORKER_SHARED_SECRET" \
        AAGASA_STATE_DIR="$AAGASA_STATE_DIR" \
        AAGASA_RECORDINGS_DIR="$AAGASA_RECORDINGS_DIR" \
        AAGASA_ROTATOR_SERIAL_PORT="$AAGASA_ROTATOR_SERIAL_PORT" \
        AAGASA_ROTATOR_BAUD_RATE="$AAGASA_ROTATOR_BAUD_RATE" \
        AAGASA_ROTATOR_PARK_AZIMUTH="$AAGASA_ROTATOR_PARK_AZIMUTH" \
        AAGASA_ROTATOR_PARK_ELEVATION="$AAGASA_ROTATOR_PARK_ELEVATION" \
        AAGASA_SDR_DEVICE_INDEX="$AAGASA_SDR_DEVICE_INDEX" \
        AAGASA_RTL_TEST_BIN="$AAGASA_RTL_TEST_BIN" \
        AAGASA_SATDUMP_BIN="$AAGASA_SATDUMP_BIN" \
        AAGASA_SATDUMP_PIPELINES_DIR="$AAGASA_SATDUMP_PIPELINES_DIR" \
        AAGASA_LOG_LEVEL="$AAGASA_LOG_LEVEL" \
        AAGASA_HEARTBEAT_INTERVAL_SECONDS="$AAGASA_HEARTBEAT_INTERVAL_SECONDS" \
        uv run aagasa-worker >"${LOG_DIR}/worker.log" 2>&1 &
    echo $! > "$WORKER_PID_FILE"
    sleep 2
    pid_running "$WORKER_PID_FILE" || die "worker failed to start; see ${LOG_DIR}/worker.log"
    note "worker started"
}

cmd_server() {
    require_file "$SERVER_ENV"
    load_env "$SERVER_ENV"
    mkdir -p "$RUN_DIR" "$LOG_DIR" "$BIN_DIR"
    build_binaries
    start_databases
    migrate_and_seed
    start_prediction
    start_server
    say "Server up"
    note "REST:    http://127.0.0.1:$HTTP_PORT"
    note "gRPC:    127.0.0.1:$GRPC_PORT"
    note "logs:    $LOG_DIR"
}

cmd_worker() {
    require_file "$WORKER_ENV"
    require_file "$SERVER_ENV"  # need shared secret
    load_env "$WORKER_ENV"
    load_env "$SERVER_ENV"  # for shared secret
    mkdir -p "$RUN_DIR" "$LOG_DIR"
    start_worker
    say "Worker up"
    note "logs: $LOG_DIR"
}

cmd_both() {
    require_file "$SERVER_ENV"
    require_file "$WORKER_ENV"
    load_env "$SERVER_ENV"
    load_env "$WORKER_ENV"
    mkdir -p "$RUN_DIR" "$LOG_DIR" "$BIN_DIR"
    build_binaries
    start_databases
    migrate_and_seed
    start_prediction
    start_server
    start_worker
    say "Server + Worker up"
    note "Server: http://127.0.0.1:$HTTP_PORT"
    note "Worker: running (check $LOG_DIR/worker.log)"
}

cmd_stop() {
    say "Stopping all"
    stop_pid "$WORKER_PID_FILE" "worker"
    stop_pid "$SERVER_PID_FILE" "server"
    stop_pid "$PREDICTION_PID_FILE" "prediction"
    stop_pid "$MONGO_PID_FILE" "mongo"
    stop_pid "$REDIS_PID_FILE" "redis"
    stop_pid "$PG_PID_FILE" "postgres"
    say "All stopped"
}

cmd_status() {
    say "Status"
    for name in postgres redis mongo prediction server worker; do
        local pid_file="${RUN_DIR}/${name}.pid"
        if pid_running "$pid_file"; then
            note "$name: running (pid $(cat "$pid_file"))"
        else
            note "$name: stopped"
        fi
    done
    if [[ -f "$LOG_DIR/server.log" ]]; then
        note "Server health: $(curl -s --max-time 3 "http://127.0.0.1:$HTTP_PORT/readyz" 2>/dev/null || echo 'unreachable')"
    fi
    if [[ -f "$LOG_DIR/worker.log" ]]; then
        tail -5 "$LOG_DIR/worker.log" | sed 's/^/  worker: /'
    fi
}

cmd_hwtest() {
    require_file "$WORKER_ENV"
    load_env "$WORKER_ENV"
    say "Hardware verification"
    cd "$ROOT/worker"
    uv run aagasa-hardware status
    uv run aagasa-hardware pipelines
}

case "${1:-}" in
    --server)   cmd_server ;;
    --worker)   cmd_worker ;;
    --both)     cmd_both ;;
    --stop)     cmd_stop ;;
    --status)   cmd_status ;;
    --hwtest)   cmd_hwtest ;;
    *)
        grep '^# Usage:' "$0" | sed 's/^# //'
        exit 1
        ;;
esac