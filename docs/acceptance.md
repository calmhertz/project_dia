# V1 Acceptance

The seven critical scenarios from plan.md V19, run against a live deployment.

```sh
AAGASA_ENV_NAME=accept AAGASA_PORT_OFFSET=200 scripts/dev-env.sh up
AAGASA_ENV_NAME=accept AAGASA_PORT_OFFSET=200 scripts/acceptance.sh
```

The suite stops and starts the Server and the Worker, so give it an
environment of its own. It is repeatable: it cancels any passes an earlier run
left holding a window, rather than deleting them, because the station's
history is never destroyed.

A step that needs a satellite actually overhead is reported **DEFERRED**, not
passed. A pass cannot be arranged on demand, and an acceptance run that
claimed otherwise would be worthless.

## Result

Run on 2026-08-28 against a simulated-hardware environment.
**28 passed, 0 failed, 4 deferred**, and two of the deferred steps were then
closed by waiting for a real pass window (below).

| Scenario | Result |
|---|---|
| A - Server offline | 5 passed, 1 deferred |
| B - Normal operation | 7 passed, **execution confirmed separately** |
| C - Worker offline | 4 passed |
| D - Root override | 5 passed |
| E - Public and private | 3 passed, **download confirmed separately** |
| F - Orbital data provider failure | 3 passed |
| G - Storage pressure | 1 deferred |

## A pass, executed

The suite cannot wait for a satellite, so a NOAA 19 pass was approved and left
to run. Fifty minutes later, unattended:

```
executing pass 2b500112 aos=16:15:00 los=16:24:44
capture started  output=.../2b500112-88cb-4a3b-9cf0-51fd1c5865fe
capture stopped  exit_code=-15 terminated=True
parking rotator  az=0 el=0
pass finished    state=completed
registered 1 recording(s) for upload
reported 1 execution record(s), accepted=1
```

The Server holds `baseband.s16`, 524288 bytes, `stored`. The pass is
`completed`. The antenna was commanded through the pass and parked afterwards.

That closes scenario B's execution step end to end: request, approve, plan,
synchronise, execute, capture, upload, report.

It also closes scenario E's download. A third account with no relationship to
the pass listed the public recording and downloaded all 524288 bytes; the same
URL without a token is refused with 401.

### A bug the pass exposed

The simulated rotator logged every real command as unrecognised. Its pattern
expected `W<3 digits> <3 digits>`; the Worker sends `W<4 digits> <3 digits>`.
Pointing worked - the Worker writes and reads no reply - but the stand-in was
recording nothing, so the one thing it exists to demonstrate was invisible.

Fixed, and verified by feeding it commands built by the Worker's own
`format_command` rather than a hand-typed string. A stand-in that quietly
rejects valid input is worse than no stand-in.

### A - Server offline

The Server was stopped. The station kept running, the Server came back, the
station reconnected without being touched, and the Server then reported it
holding the current desired state.

Deferred: a recording captured *during* the outage uploading on reconnect.
This was proven live in V12 - an 11 KiB recording arrived checksummed after
the Server returned, and nothing was re-sent - and repeating it needs a pass
window.

### B - Normal operation

Two users requested passes. The overlapping window was refused with
`station_conflict`, and the refusal named the window without naming the user
holding it. A different window was accepted. An admin approved, an executable
plan was generated with 72 pointing steps, and the station took it.

Execution and the recording reaching the Server were confirmed by waiting for
a pass; see above.

### C - Worker offline

The station was stopped. The Server showed it offline within the heartbeat
timeout, refused new scheduling with `worker_offline`, and when the station
returned it reconnected and reconciled by itself.

### D - Root override

Root scheduled over an approved pass. The held pass became `cancelled` with
reason `cancelled_by_root_override`, an audit record naming it exists, and
Root's pass is approved. Nothing was deleted.

### E - Public and private

A private pass is 404 to an unrelated user - not 403, because even its
existence is private. A public pass is readable by any signed-in user, and its
`requested_by` is withheld from everyone but the owner, an Admin and Root.

Downloading a public recording was confirmed by waiting for a pass: an
unrelated third account read and downloaded it in full, and the same URL
without a token is refused. See above.

### F - Orbital data provider failure

The Server was restarted pointing at unreachable providers and asked to
refresh. It reported `kept_last_known_good` for all three satellites, and the
stored epoch was unchanged afterwards. A provider outage costs freshness, not
data.

### G - Storage pressure

Deferred. Filling a real disk is not something an acceptance run should do.
The retention rule, the twelve-hour floor and the ninety-percent threshold are
covered by `worker/tests/test_recordings.py`, and the Worker also refuses a
pass its disk cannot hold before the antenna moves (V16).

## What acceptance does not cover

- **A real pass on real hardware.** V4 and V10 remain unsigned: the rotator
  and the dongle are attached and respond, but no pass has been tracked and
  recorded end to end with them. This is the one outstanding item for V1.
- **Realtime updates and notifications** (client-spec sections 21 and 31).
  Specified, assigned to no phase, not built; the client polls instead.
- **`raw_and_process`** runs as a live decode only. One RTL-SDR cannot feed
  two SatDump processes, and the shortfall is recorded in the execution
  detail rather than hidden.
- **Retiring an account with history.** It cannot be deleted, by design, and
  there is no deactivate flag.
