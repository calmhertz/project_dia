#!/usr/bin/env bash

set -uo pipefail

OFFSET="${AAGASA_PORT_OFFSET:-0}"
HTTP="http://127.0.0.1:$((8080 + OFFSET))"
ROOT_PASSWORD="${AAGASA_ROOT_PASSWORD:-development-root-password}"
OPERATOR_PASSWORD="${AAGASA_OPERATOR_PASSWORD:-operator-development-password}"
APPROVER_PASSWORD="${AAGASA_APPROVER_PASSWORD:-approver-development-password}"

PASSED=0
FAILED=0

say()  { printf '\n== %s\n' "$1"; }
pass() { printf '  PASS  %s\n' "$1"; PASSED=$((PASSED + 1)); }
fail() { printf '  FAIL  %s\n' "$1"; FAILED=$((FAILED + 1)); }

json() {
    python3 -c "
import json,sys
try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)
for key in '$1'.split('.'):
    if isinstance(data, list):
        data = data[int(key)] if data else ''
    elif isinstance(data, dict):
        data = data.get(key, '')
    else:
        data = ''
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

status_of() {
    local method="$1" path="$2" token="${3:-}" body="${4:-}"
    local args=(-s -o /dev/null -w '%{http_code}' -X "$method" "$HTTP$path")
    [ -n "$token" ] && args+=(-H "Authorization: Bearer $token")
    [ -n "$body" ] && args+=(-H 'Content-Type: application/json' -d "$body")
    curl "${args[@]}"
}

login() {
    api POST /api/auth/login "" "{\"username\":\"$1\",\"password\":\"$2\"}" | json token
}


say "Sign in"
ROOT_TOKEN="$(login root "$ROOT_PASSWORD")"
OPERATOR_TOKEN="$(login operator "$OPERATOR_PASSWORD")"
APPROVER_TOKEN="$(login approver "$APPROVER_PASSWORD")"

[ -n "$ROOT_TOKEN" ] && pass "root" || { fail "root could not sign in"; exit 1; }
[ -n "$OPERATOR_TOKEN" ] && pass "operator" || fail "operator could not sign in"
[ -n "$APPROVER_TOKEN" ] && pass "approver" || fail "approver could not sign in"


say "Roles"
[ "$(status_of GET /api/station/config "$OPERATOR_TOKEN")" = "403" ] \
    && pass "an operator cannot read the station configuration" \
    || fail "an operator read the station configuration"
[ "$(status_of GET /api/station/config "$APPROVER_TOKEN")" = "403" ] \
    && pass "an admin cannot read the station configuration" \
    || fail "an admin read the station configuration"
[ "$(status_of GET /api/station/config "$ROOT_TOKEN")" = "200" ] \
    && pass "root can" || fail "root cannot read the station configuration"
[ "$(status_of GET /api/audit "$APPROVER_TOKEN")" = "403" ] \
    && pass "an admin cannot read the audit trail" || fail "an admin read the audit trail"


say "Station"
WORKERS="$(api GET /api/workers "$APPROVER_TOKEN")"
WORKER_STATE="$(printf '%s' "$WORKERS" | json workers.0.connection_state)"
[ "$WORKER_STATE" = "online" ] \
    && pass "the ground station is online" \
    || fail "the ground station is $WORKER_STATE"

PIPELINES="$(api GET /api/pipelines "$OPERATOR_TOKEN")"
PIPELINE_COUNT="$(printf '%s' "$PIPELINES" | python3 -c 'import json,sys; print(len(json.load(sys.stdin).get("pipelines", [])))')"
[ "$PIPELINE_COUNT" -gt 0 ] \
    && pass "the station reported $PIPELINE_COUNT pipelines" \
    || fail "no pipelines were reported"


say "Catalogue and prediction"
SATELLITES="$(api GET /api/satellites "$OPERATOR_TOKEN")"
SATELLITE_ID="$(printf '%s' "$SATELLITES" | json satellites.0.id)"
[ -n "$SATELLITE_ID" ] && pass "the catalogue has satellites" || { fail "the catalogue is empty"; exit 1; }

PREDICTION="$(api GET "/api/satellites/$SATELLITE_ID/passes?hours=48" "$OPERATOR_TOKEN")"
AOS="$(printf '%s' "$PREDICTION" | json passes.0.aos)"
[ -n "$AOS" ] \
    && pass "a pass was predicted at $AOS" \
    || { fail "no pass was predicted in the next 48 hours"; exit 1; }


say "Requesting"
REQUEST="{\"satellite_id\":\"$SATELLITE_ID\",\"aos\":\"$AOS\",\"band\":\"vhf\",\"recording_mode\":\"raw\",\"visibility\":\"private\"}"
CREATED="$(api POST /api/passes "$OPERATOR_TOKEN" "$REQUEST")"
PASS_ID="$(printf '%s' "$CREATED" | json pass.id)"
PASS_STATUS="$(printf '%s' "$CREATED" | json pass.status)"

if [ -z "$PASS_ID" ]; then
    fail "the request was refused: $(printf '%s' "$CREATED" | json message)"
    printf '\npassed %d, failed %d\n' "$PASSED" "$FAILED"
    exit 1
fi
pass "a pass was requested"
[ "$PASS_STATUS" = "pending_approval" ] \
    && pass "it is waiting for a decision" \
    || fail "status = $PASS_STATUS, want pending_approval"

NO_PIPELINE="{\"satellite_id\":\"$SATELLITE_ID\",\"aos\":\"$AOS\",\"band\":\"vhf\",\"recording_mode\":\"process\",\"visibility\":\"private\"}"
[ "$(status_of POST /api/passes "$OPERATOR_TOKEN" "$NO_PIPELINE")" = "400" ] \
    && pass "a decode without a pipeline is refused" \
    || fail "a decode without a pipeline was accepted"

say "Approving"
[ "$(status_of POST "/api/passes/$PASS_ID/approve" "$OPERATOR_TOKEN")" = "403" ] \
    && pass "an operator cannot approve their own pass" \
    || fail "an operator approved their own pass"

APPROVED="$(api POST "/api/passes/$PASS_ID/approve" "$APPROVER_TOKEN")"
[ "$(printf '%s' "$APPROVED" | json pass.status)" = "approved" ] \
    && pass "an admin approved it" \
    || fail "approval failed: $(printf '%s' "$APPROVED" | json message)"


say "Plan"
PLAN="$(api GET "/api/passes/$PASS_ID/plan" "$APPROVER_TOKEN")"
TRACK_POINTS="$(printf '%s' "$PLAN" | json track_point_count)"
FREQUENCY="$(printf '%s' "$PLAN" | json radio.frequency_hz)"
[ -n "$TRACK_POINTS" ] && [ "$TRACK_POINTS" -gt 0 ] \
    && pass "the plan carries $TRACK_POINTS pointing steps" \
    || fail "the plan has no pointing timeline"
[ "$FREQUENCY" = "137100000" ] \
    && pass "the plan carries the configured frequency" \
    || fail "frequency = $FREQUENCY"

say "Synchronization"
SYNCED=""
for _ in $(seq 1 15); do
    WORKERS="$(api GET /api/workers "$APPROVER_TOKEN")"
    if [ "$(printf '%s' "$WORKERS" | json workers.0.in_sync)" = "True" ]; then
        SYNCED=yes
        break
    fi
    sleep 2
done
[ -n "$SYNCED" ] \
    && pass "the station reports itself in sync with the new plan" \
    || fail "the station did not take the plan within 30s"


say "Visibility"
[ "$(status_of GET "/api/passes/$PASS_ID" "$APPROVER_TOKEN")" = "200" ] \
    && pass "an admin can read the pass" || fail "an admin cannot read the pass"
[ "$(status_of GET /api/passes?scope=all "$OPERATOR_TOKEN")" = "403" ] \
    && pass "an operator cannot list every pass" || fail "an operator listed every pass"


say "Cleanup"
CANCELLED="$(api POST "/api/passes/$PASS_ID/cancel" "$OPERATOR_TOKEN")"
[ "$(printf '%s' "$CANCELLED" | json pass.status)" = "cancelled" ] \
    && pass "the owner cancelled their pass" \
    || fail "cancellation failed"

printf '\n%s\n' "-----------------------------------------"
printf 'passed %d, failed %d\n' "$PASSED" "$FAILED"
[ "$FAILED" -eq 0 ]
