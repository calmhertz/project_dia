#!/usr/bin/env bash

set -uo pipefail

HTTP="${AAGASA_HTTP:-http://127.0.0.1:8080}"
PG="${AAGASA_PG_NAME:-aagasa-pg}"
PASSED=0
FAILED=0
SKIPPED=0

say()  { printf '\n=== %s\n' "$1"; }
pass() { printf '  PASS  %s\n' "$1"; PASSED=$((PASSED + 1)); }
fail() { printf '  FAIL  %s\n' "$1"; FAILED=$((FAILED + 1)); }
skip() { printf '  SKIP  %s\n' "$1"; SKIPPED=$((SKIPPED + 1)); }

status_of() { curl -s -o /dev/null -w '%{http_code}' --max-time 5 "$HTTP$1"; }
body_of()   { curl -s --max-time 5 "$HTTP$1"; }

wait_for() {
    local want="$1" path="$2" limit="$3" waited=0
    while [ "$waited" -lt "$limit" ]; do
        [ "$(status_of "$path")" = "$want" ] && return 0
        sleep 1
        waited=$((waited + 1))
    done
    return 1
}

worker_state() {
    podman exec "$PG" psql -U aagasa -d aagasa -tAc \
        "select connection_state from workers limit 1" 2>/dev/null | tr -d '[:space:]'
}

wait_for_worker() {
    local want="$1" limit="$2" waited=0
    while [ "$waited" -lt "$limit" ]; do
        [ "$(worker_state)" = "$want" ] && return 0
        sleep 1
        waited=$((waited + 1))
    done
    return 1
}


say "Baseline"
if [ "$(status_of /health)" = "200" ]; then
    pass "the Server is serving"
else
    printf '  the Server is not running; start it first\n'
    exit 1
fi
if [ "$(status_of /readyz)" = "200" ]; then
    pass "every dependency is reachable"
else
    fail "the stack is already degraded: $(body_of /readyz)"
fi


say "Drill: database outage and reconnect"
if ! podman inspect "$PG" >/dev/null 2>&1; then
    skip "no container named $PG"
else
    podman stop "$PG" >/dev/null 2>&1
    sleep 3

    if [ "$(status_of /health)" = "200" ]; then
        pass "the Server stayed up without its database"
    else
        fail "the Server stopped answering when the database went away"
    fi
    if [ "$(status_of /readyz)" = "503" ]; then
        pass "readiness reported the outage"
    else
        fail "readiness still claims to be ready: $(body_of /readyz)"
    fi

    podman start "$PG" >/dev/null 2>&1
    if wait_for 200 /readyz 60; then
        pass "the Server recovered on its own"
    else
        fail "the Server did not recover within 60s: $(body_of /readyz)"
    fi
fi


say "Drill: Server crash and restart"
if [ -z "${AAGASA_SERVER_CMD:-}" ]; then
    skip "AAGASA_SERVER_CMD is not set"
else
    worker_before="$(worker_state)"
    pkill -x aagasa-server >/dev/null 2>&1
    sleep 3
    if [ "$(status_of /health)" != "200" ]; then
        pass "the Server is down"
    else
        fail "the Server survived being killed"
    fi

    nohup bash -c "$AAGASA_SERVER_CMD" >/dev/null 2>&1 &
    if wait_for 200 /readyz 60; then
        pass "the Server came back"
    else
        fail "the Server did not come back"
    fi

    if [ "$worker_before" = "online" ]; then
        if wait_for_worker online 90; then
            pass "the Worker reconnected without being touched"
        else
            fail "the Worker did not reconnect within 90s"
        fi
    else
        skip "no Worker was online before the drill"
    fi
fi


say "Drill: Worker crash and restart"
if [ -z "${AAGASA_WORKER_CMD:-}" ]; then
    skip "AAGASA_WORKER_CMD is not set"
else
    pkill -f '/\.venv/bin/aagasa-worker' >/dev/null 2>&1
    if wait_for_worker offline 40; then
        pass "the Server noticed the Worker had gone"
    else
        fail "the Server still reports the Worker as online"
    fi

    nohup bash -c "$AAGASA_WORKER_CMD" >"${AAGASA_WORKER_LOG:-/dev/null}" 2>&1 &
    if wait_for_worker online 90; then
        pass "the Worker registered again after a restart"
    else
        fail "the Worker did not come back within 90s"
    fi

    if [ -n "${AAGASA_WORKER_LOG:-}" ] && [ -f "${AAGASA_WORKER_LOG}" ]; then
        if grep -q "local state loaded" "${AAGASA_WORKER_LOG}"; then
            pass "it reloaded its durable state rather than starting empty"
        else
            fail "no sign that local state was reloaded"
        fi
    else
        skip "no Worker log to read"
    fi
fi


say "Drill: storage reporting"
storage="$(body_of /readyz)"
case "$storage" in
    *recordings_free_bytes*)
        pass "readiness reports recording storage"
        case "$storage" in
            *'"accepting_uploads":true'*) pass "the Server is accepting uploads" ;;
            *'"accepting_uploads":false'*) fail "uploads are being refused: the disk is nearly full" ;;
            *) fail "storage is reported without saying whether uploads are accepted" ;;
        esac
        ;;
    *) fail "readiness says nothing about storage" ;;
esac

printf '\n%s\n' "-----------------------------------------"
printf 'passed %d, failed %d, skipped %d\n' "$PASSED" "$FAILED" "$SKIPPED"
[ "$FAILED" -eq 0 ]
