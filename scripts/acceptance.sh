#!/usr/bin/env bash
# Aagasa V1 acceptance (plan.md V19).
#
#   AAGASA_ENV_NAME=accept AAGASA_PORT_OFFSET=200 scripts/dev-env.sh up
#   AAGASA_PORT_OFFSET=200 scripts/acceptance.sh
#
# Runs the seven critical scenarios from plan.md against a live deployment.
# It stops and starts the Server and the Worker, so give it an environment of
# its own rather than one someone is using.
#
# Steps that need a satellite to actually fly overhead are reported as
# DEFERRED rather than passed: a pass cannot be arranged on demand, and
# claiming otherwise would be the one thing an acceptance run must not do.

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OFFSET="${AAGASA_PORT_OFFSET:-200}"
NAME="${AAGASA_ENV_NAME:-accept}"
HTTP="http://127.0.0.1:$((8080 + OFFSET))"
RUN="${AAGASA_RUN_DIR:-$HOME/.local/share/aagasa/$NAME}"

ROOT_PASSWORD="${AAGASA_ROOT_PASSWORD:-development-root-password}"
OPERATOR_PASSWORD="${AAGASA_OPERATOR_PASSWORD:-operator-development-password}"
APPROVER_PASSWORD="${AAGASA_APPROVER_PASSWORD:-approver-development-password}"

PASSED=0; FAILED=0; DEFERRED=0

scenario() { printf '\n== %s\n' "$1"; }
pass()     { printf '  PASS      %s\n' "$1"; PASSED=$((PASSED + 1)); }
fail()     { printf '  FAIL      %s\n' "$1"; FAILED=$((FAILED + 1)); }
deferred() { printf '  DEFERRED  %s\n' "$1"; DEFERRED=$((DEFERRED + 1)); }
note()     { printf '            %s\n' "$1"; }

json() {
    python3 -c "
import json,sys
try: data = json.load(sys.stdin)
except Exception: sys.exit(0)
for key in '$1'.split('.'):
    if isinstance(data, list): data = data[int(key)] if data else ''
    elif isinstance(data, dict): data = data.get(key, '')
    else: data = ''
print(data if not isinstance(data, (dict, list)) else json.dumps(data))
"
}

api() {
    local method="$1" path="$2" token="${3:-}" body="${4:-}"
    local args=(-s -X "$method" "$HTTP$path" -H 'Accept: application/json')
    [ -n "$token" ] && args+=(-H "Authorization: Bearer $token")
    [ -n "$body" ] && args+=(-H 'Content-Type: application/json' -d "$body")
    curl "${args[@]}"
}

code_of() {
    local method="$1" path="$2" token="${3:-}" body="${4:-}"
    local args=(-s -o /dev/null -w '%{http_code}' -X "$method" "$HTTP$path" --max-time 10)
    [ -n "$token" ] && args+=(-H "Authorization: Bearer $token")
    [ -n "$body" ] && args+=(-H 'Content-Type: application/json' -d "$body")
    curl "${args[@]}"
}

login() { api POST /api/auth/login "" "{\"username\":\"$1\",\"password\":\"$2\"}" | json token; }

worker_state() {
    api GET /api/workers "$APPROVER" | json workers.0.connection_state
}

wait_for_worker() {
    local want="$1" limit="$2" waited=0
    while [ "$waited" -lt "$limit" ]; do
        [ "$(worker_state)" = "$want" ] && return 0
        sleep 2; waited=$((waited + 2))
    done
    return 1
}

wait_for_server() {
    local waited=0
    while [ "$waited" -lt 90 ]; do
        [ "$(code_of GET /readyz)" = "200" ] && return 0
        sleep 2; waited=$((waited + 2))
    done
    return 1
}

env_do() { AAGASA_ENV_NAME="$NAME" AAGASA_PORT_OFFSET="$OFFSET" "$ROOT/scripts/dev-env.sh" "$@" >/dev/null 2>&1; }

# Sign in --------------------------------------------------------------------

ROOT_TOKEN="$(login root "$ROOT_PASSWORD")"
OPERATOR="$(login operator "$OPERATOR_PASSWORD")"
APPROVER="$(login approver "$APPROVER_PASSWORD")"
[ -n "$ROOT_TOKEN" ] && [ -n "$OPERATOR" ] && [ -n "$APPROVER" ] || {
    printf 'acceptance: cannot sign in; is the environment up?\n' >&2; exit 1; }

SATELLITE="$(api GET /api/satellites "$OPERATOR" | json satellites.0.id)"
[ -n "$SATELLITE" ] || { printf 'acceptance: the catalogue is empty\n' >&2; exit 1; }

# next_pass SATELLITE INDEX - the AOS of an upcoming predicted pass.
next_pass() {
    api GET "/api/satellites/$1/passes?hours=72" "$OPERATOR" | python3 -c "
import json,sys
passes = json.load(sys.stdin).get('passes', [])
index = int('${2:-0}')
print(passes[index]['aos'] if len(passes) > index else '')
"
}

request_pass() {
    local token="$1" satellite="$2" aos="$3" visibility="${4:-private}" override="${5:-false}"
    api POST /api/passes "$token" \
        "{\"satellite_id\":\"$satellite\",\"aos\":\"$aos\",\"band\":\"vhf\",\"recording_mode\":\"raw\",\"visibility\":\"$visibility\",\"override\":$override}"
}

# A known starting point ------------------------------------------------------
#
# An acceptance run must be repeatable, and passes from a previous run still
# hold their windows. Cancelling rather than deleting: the station's history
# is never destroyed, so the record of earlier runs stays (spec.md section 13).

scenario "Prepare"

CLEARED=0
for id in $(api GET "/api/passes?scope=all" "$ROOT_TOKEN" | python3 -c "
import json,sys
alive = {'pending_approval', 'approved'}
for entry in json.load(sys.stdin).get('passes', []):
    if entry.get('status') in alive:
        print(entry['id'])
"); do
    api POST "/api/passes/$id/cancel" "$ROOT_TOKEN" >/dev/null
    CLEARED=$((CLEARED + 1))
done
pass "cleared $CLEARED pass(es) left by an earlier run"

# Scenario B - normal operation ----------------------------------------------
# Run first: the later scenarios need a pass to exist.

scenario "B  Normal operation"

AOS_ONE="$(next_pass "$SATELLITE" 0)"
AOS_TWO="$(next_pass "$SATELLITE" 1)"
[ -n "$AOS_ONE" ] && [ -n "$AOS_TWO" ] || { fail "no predicted passes to work with"; exit 1; }

FIRST="$(request_pass "$OPERATOR" "$SATELLITE" "$AOS_ONE")"
PASS_ONE="$(printf '%s' "$FIRST" | json pass.id)"
[ -n "$PASS_ONE" ] && pass "a user requested a pass" \
    || { fail "the request was refused: $(printf '%s' "$FIRST" | json message)"; exit 1; }

# The same window, a different user: the station cannot serve both.
CONFLICT="$(request_pass "$APPROVER" "$SATELLITE" "$AOS_ONE")"
if [ "$(printf '%s' "$CONFLICT" | json error)" = "station_conflict" ]; then
    pass "a conflicting window is refused"
    if printf '%s' "$CONFLICT" | grep -q 'operator'; then
        fail "the conflict names the other user"
    else
        pass "the refusal does not say who holds the slot"
    fi
else
    fail "a conflicting request was accepted: $CONFLICT"
fi

SECOND="$(request_pass "$APPROVER" "$SATELLITE" "$AOS_TWO")"
PASS_TWO="$(printf '%s' "$SECOND" | json pass.id)"
[ -n "$PASS_TWO" ] && pass "a different window is accepted" || fail "a non-conflicting request was refused"

[ "$(api POST "/api/passes/$PASS_ONE/approve" "$APPROVER" | json pass.status)" = "approved" ] \
    && pass "an admin approved it" || fail "approval failed"

PLAN="$(api GET "/api/passes/$PASS_ONE/plan" "$APPROVER")"
POINTS="$(printf '%s' "$PLAN" | json track_point_count)"
[ -n "$POINTS" ] && [ "$POINTS" -gt 0 ] \
    && pass "an executable plan was generated ($POINTS pointing steps)" \
    || fail "no executable plan"

SYNCED=""
for _ in $(seq 1 15); do
    [ "$(api GET /api/workers "$APPROVER" | json workers.0.in_sync)" = "True" ] && { SYNCED=yes; break; }
    sleep 2
done
[ -n "$SYNCED" ] && pass "the plan reached the station" || fail "the station did not take the plan"

deferred "the station executes and the recording is stored on the Server"
note "needs the pass window itself; see the execution note at the end"

# Scenario D - root override -------------------------------------------------

scenario "D  Root override"

OVERRIDE="$(request_pass "$ROOT_TOKEN" "$SATELLITE" "$AOS_ONE" private true)"
OVERRIDE_ID="$(printf '%s' "$OVERRIDE" | json pass.id)"
if [ -n "$OVERRIDE_ID" ]; then
    pass "root scheduled over an approved pass"
    CANCELLED="$(api GET "/api/passes/$PASS_ONE" "$ROOT_TOKEN")"
    [ "$(printf '%s' "$CANCELLED" | json pass.status)" = "cancelled" ] \
        && pass "the held pass is cancelled" || fail "the held pass is still $(printf '%s' "$CANCELLED" | json pass.status)"
    [ "$(printf '%s' "$CANCELLED" | json pass.cancellation_reason)" = "cancelled_by_root_override" ] \
        && pass "it says why it was cancelled" || fail "the cancellation reason is missing"
    AUDIT="$(api GET "/api/audit?action=pass.cancelled_by_root_override" "$ROOT_TOKEN")"
    printf '%s' "$AUDIT" | grep -q "$PASS_ONE" \
        && pass "the override is in the audit trail" || fail "no audit record for the override"
    [ "$(api GET "/api/passes/$OVERRIDE_ID" "$ROOT_TOKEN" | json pass.status)" = "approved" ] \
        && pass "root's pass is approved" || fail "root's pass is not approved"
else
    fail "the override was refused: $(printf '%s' "$OVERRIDE" | json message)"
fi

# Scenario E - public and private --------------------------------------------

scenario "E  Public and private"

PRIVATE_ID="$PASS_TWO"
[ "$(code_of GET "/api/passes/$PRIVATE_ID" "$OPERATOR")" = "404" ] \
    && pass "a private pass is invisible to an unrelated user" \
    || fail "an unrelated user can read a private pass"

AOS_THREE="$(next_pass "$SATELLITE" 2)"
PUBLIC="$(request_pass "$APPROVER" "$SATELLITE" "$AOS_THREE" public)"
PUBLIC_ID="$(printf '%s' "$PUBLIC" | json pass.id)"
if [ -n "$PUBLIC_ID" ]; then
    SEEN="$(api GET "/api/passes/$PUBLIC_ID" "$OPERATOR")"
    [ "$(printf '%s' "$SEEN" | json pass.id)" = "$PUBLIC_ID" ] \
        && pass "a public pass is visible to any signed-in user" || fail "a public pass is hidden"
    OWNER="$(printf '%s' "$SEEN" | json pass.requested_by)"
    [ -z "$OWNER" ] && pass "its owner is not disclosed" || fail "the public pass names its owner: $OWNER"
else
    fail "could not create a public pass"
fi
deferred "downloading a public recording"
note "needs a completed pass; the authorization is covered by the Go tests"

# Scenario C - worker offline ------------------------------------------------

scenario "C  Worker offline"

env_do down_worker 2>/dev/null || pkill -f '/\.venv/bin/aagasa-worker' >/dev/null 2>&1
if wait_for_worker offline 45; then
    pass "the Server shows the station offline"
else
    fail "the Server still reports the station online"
fi

BLOCKED="$(request_pass "$OPERATOR" "$SATELLITE" "$(next_pass "$SATELLITE" 3)")"
[ "$(printf '%s' "$BLOCKED" | json error)" = "worker_offline" ] \
    && pass "new scheduling is refused while it is away" \
    || fail "a pass was scheduled with no station: $BLOCKED"

AAGASA_ENV_NAME="$NAME" AAGASA_PORT_OFFSET="$OFFSET" "$ROOT/scripts/dev-env.sh" up >/dev/null 2>&1
if wait_for_worker online 90; then
    pass "it reconnects by itself"
    for _ in $(seq 1 15); do
        [ "$(api GET /api/workers "$APPROVER" | json workers.0.in_sync)" = "True" ] && break
        sleep 2
    done
    [ "$(api GET /api/workers "$APPROVER" | json workers.0.in_sync)" = "True" ] \
        && pass "state reconciles after the outage" || fail "the station did not reconcile"
else
    fail "the station did not come back"
fi

# Scenario A - server offline ------------------------------------------------

scenario "A  Server offline"

env_do down >/dev/null 2>&1
pkill -x aagasa-server >/dev/null 2>&1
sleep 3
[ "$(code_of GET /health)" != "200" ] && pass "the Server is down" || fail "the Server survived being stopped"

# The Worker must still be running with its schedule.
AAGASA_ENV_NAME="$NAME" AAGASA_PORT_OFFSET="$OFFSET" AAGASA_HARDWARE=simulated \
    "$ROOT/scripts/dev-env.sh" up >/dev/null 2>&1 &
UP_PID=$!
sleep 8
if pgrep -f '/\.venv/bin/aagasa-worker' >/dev/null; then
    pass "the station keeps running without the Server"
else
    fail "the station stopped when the Server did"
fi
wait "$UP_PID" 2>/dev/null

if wait_for_server; then
    pass "the Server came back"
else
    fail "the Server did not come back"
fi
if wait_for_worker online 90; then
    pass "the station reconnected on its own"
else
    fail "the station did not reconnect"
fi
# Convergence means the Server agrees the station holds the current desired
# state, which is a fact it reports rather than something to infer from logs.
APPROVER="$(login approver "$APPROVER_PASSWORD")"
CONVERGED=""
for _ in $(seq 1 20); do
    [ "$(api GET /api/workers "$APPROVER" | json workers.0.in_sync)" = "True" ] && { CONVERGED=yes; break; }
    sleep 2
done
[ -n "$CONVERGED" ] \
    && pass "the two converged on the desired state" \
    || fail "the Server does not report the station in sync after the outage"
deferred "a recording captured during the outage uploads on reconnect"
note "proven live in V12; needs a pass window to repeat here"

# Scenario F - TLE provider failure -------------------------------------------

scenario "F  Orbital data provider failure"

BEFORE_EPOCH="$(api GET /api/tle-status "$APPROVER" | json satellites.0.epoch)"

# Point the Server at providers that cannot answer, and ask it to refresh.
env_do down >/dev/null 2>&1
AAGASA_CELESTRAK_URL="http://127.0.0.1:1/gp.php" \
AAGASA_SATNOGS_URL="http://127.0.0.1:1/api/tle" \
AAGASA_ENV_NAME="$NAME" AAGASA_PORT_OFFSET="$OFFSET" \
    "$ROOT/scripts/dev-env.sh" up >/dev/null 2>&1

if wait_for_server; then
    APPROVER="$(login approver "$APPROVER_PASSWORD")"
    REFRESH="$(api POST /api/satellites/refresh-tles "$APPROVER")"
    KEPT="$(printf '%s' "$REFRESH" | json kept_last_known_good)"
    UPDATED="$(printf '%s' "$REFRESH" | json updated)"
    if [ -n "$KEPT" ] && [ "$KEPT" -gt 0 ]; then
        pass "every provider failed and the last known good was kept ($KEPT satellites)"
    else
        fail "refresh reported updated=$UPDATED kept=$KEPT with no reachable provider"
    fi
    AFTER_EPOCH="$(api GET /api/tle-status "$APPROVER" | json satellites.0.epoch)"
    [ "$AFTER_EPOCH" = "$BEFORE_EPOCH" ] \
        && pass "the stored orbital data is unchanged" \
        || fail "orbital data changed during an outage: $BEFORE_EPOCH -> $AFTER_EPOCH"
else
    fail "the Server did not start with unreachable providers configured"
fi

# Put the real providers back.
env_do down >/dev/null 2>&1
AAGASA_ENV_NAME="$NAME" AAGASA_PORT_OFFSET="$OFFSET" "$ROOT/scripts/dev-env.sh" up >/dev/null 2>&1
wait_for_server && pass "the Server runs again with real providers" || fail "the Server did not restart"

# Scenario G - storage pressure -----------------------------------------------

scenario "G  Storage pressure"

deferred "the station cleans the oldest eligible recordings above 90 percent"
note "filling a real disk is not something an acceptance run should do; the"
note "retention rule, the twelve-hour floor and the pressure threshold are"
note "covered by the Worker's own tests (worker/tests/test_recordings.py)"

# Summary ---------------------------------------------------------------------

printf '\n%s\n' "========================================="
printf 'passed %d, failed %d, deferred %d\n' "$PASSED" "$FAILED" "$DEFERRED"
printf '\nDeferred steps need a satellite overhead or a full disk. To close the\n'
printf 'execution ones, approve a pass and leave this environment running:\n'
printf '  AAGASA_ENV_NAME=%s AAGASA_PORT_OFFSET=%s scripts/dev-env.sh status\n' "$NAME" "$OFFSET"
[ "$FAILED" -eq 0 ]
