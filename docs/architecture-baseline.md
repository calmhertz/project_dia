# Aagasa Architecture Baseline

Status: V0 through V19 complete; V4 and V10 need a live pass to sign off.

This document records the reconnaissance results and the repository structure that all
later phases build on. It refines nothing in `spec.md`; where the two disagree,
`spec.md` wins.

Since this was written, the development environment (`scripts/dev-env.sh`), its
simulated-hardware stand-ins, and the whole-system test harness
(`integration-test.sh`, `acceptance.sh`, `failure-drills.sh`) were removed in an
operations cleanup. The project now runs exclusively through the production
deployment (`./deploy.sh` on real hardware); sections below that describe those
removed pieces are historical record only. See [testing.md](testing.md) and
[hardware.md](hardware.md) for the current path.

---

## 1. Repository structure

```text
DIA_Specs/                        # repository root
├── spec.md                       # global contract (authoritative)
├── plan.md                       # phase plan V0..V19
├── RULES.md                      # engineering rules
├── docs/
│   └── architecture-baseline.md  # this file
├── proto/                        # shared gRPC contracts (Server <-> Worker, Go <-> Python)
├── server/
│   ├── server-spec.md
│   ├── go/                       # Go control plane, module "aagasa"
│   └── python/                   # Skyfield/SGP4 prediction service, uv project
├── worker/
│   ├── worker-spec.md
│   └── src/                      # Worker Python package, uv project
├── client/
│   ├── client-spec.md
│   └── (Flutter app created in V1)
├── deployment/
│   ├── compose.yaml              # the whole stack, one `deploy.sh server|worker|both`
│   ├── server/                   # Containerfile, Containerfile.prediction
│   ├── worker/
│   └── nginx/                    # reverse proxy: web on /, API on /api
└── REFERENCE/                    # read-only prior implementation, never built or imported
```

Naming note: `spec.md` refers to the reference directory as `REFERENEC/` and to the
component specs as `server/spec.md`, `worker/spec.md`, `client/spec.md`. On disk these
are `REFERENCE/` and `*-spec.md`. The filesystem names are used throughout.

### Decisions

- **Go module path: `aagasa`**, with `go.mod` at `server/go/`. Imports read
  `aagasa/internal/...`. Local path, no VCS host prefix.
- **One Go module.** The Go control plane is the only Go code in the project.
- **Two independent uv projects**: `server/python/` and `worker/`. They are deployed
  separately and must not share a lockfile; small duplication is preferred over
  coupling the edge plane to the server plane.
- **`proto/` is the shared contract root.** Both the Server and the Worker generate
  from it. Per RULES §17 a `.proto` change is a contract change affecting both sides.
- **`REFERENCE/` is read-only input.** It is excluded from every build, container
  image, test run, and lint target. No production code imports from it.
- **Git**: initialised at V0, default branch `main`. `REFERENCE/project_dia` is itself
  a git repository with its own history, so it is left untracked by the Aagasa repo
  (`.gitignore`) rather than committed as an opaque gitlink. Its files stay on disk and
  remain readable; `REFERENCE/README-AAGASA.md` is tracked.

---

## 2. Component and technology map

| Component | Language / runtime | Talks to | Transport |
|---|---|---|---|
| Client | Flutter 3.47 / Dart 3.13, Material 3 dark-only, seed `#487CE5` | Server only | REST + WebSocket |
| Server (control plane) | Go 1.27 | Client, Worker, prediction service, datastores | REST/WS out, gRPC in/out |
| Prediction service | Python + uv, Skyfield/SGP4 | Server only (internal) | gRPC |
| Worker (edge plane) | Python + uv | Server, station hardware | gRPC + realtime telemetry channel |

Datastores (Server): PostgreSQL authoritative for scheduling and conflict prevention,
MongoDB for flexible pass/telemetry documents, Redis for sessions and ephemeral state
only, filesystem for recordings and uploaded pipeline JSON.

Deployment for both planes: Podman + Podman Compose, driven by a single
`deploy.sh` (server|worker|both|down).

### Boundaries that must not blur

- The Server decides *what* happens; the Worker performs it. The Worker is never a
  second scheduler (`spec.md` §2.2, worker-spec §10).
- PostgreSQL holds scheduling state. Redis and MongoDB never become the system of
  record for it (`spec.md` §4.3).
- The Client talks only to the Server — never to the Worker, a datastore, or an
  external TLE provider (client-spec §0).
- The prediction service owns computation only, never user/session/scheduling state
  (server-spec §17).

---

## 3. Local toolchain

Verified 2026-08-24 on this machine:

| Tool | Version | Notes |
|---|---|---|
| Go | 1.27.0 | `/home/chiranthcs/Downloads/go/bin` |
| Flutter / Dart | 3.47.1 / 3.13.1 | `/home/chiranthcs/Downloads/flutter/bin` |
| uv | installed | |
| Python | 3.13.5 | |
| protoc | 3.21.12 | |
| Podman | installed | |

Go and Flutter are not on the default non-interactive `PATH`. Add:

```sh
export PATH="$PATH:/home/chiranthcs/Downloads/go/bin:/home/chiranthcs/Downloads/flutter/bin:$(go env GOPATH)/bin"
```

Installed during V1: `protoc-gen-go` v1.36.12 and `protoc-gen-go-grpc` v1.6.2 (into
`$(go env GOPATH)/bin`), plus `grpcio-tools` in both Python projects.

### Build and development commands

`make help` lists the entrypoints. Directly:

```sh
make proto                 # regenerate Go and Python gRPC code (not committed)
make build test fmt lint

# Server (Go)
cd server/go && go build ./... && go test ./... && go vet ./...

# Prediction service (Python)
cd server/python && uv sync && uv run pytest

# Worker (Python)
cd worker && uv sync && uv run pytest

# Client (Flutter)
cd client && flutter pub get && flutter analyze && flutter test

# Deployment
podman build -f deployment/server/Containerfile -t aagasa-server:latest .
podman build -f deployment/worker/Containerfile -t aagasa-worker:latest .
```

Generated protobuf code is gitignored, so `make proto` is required after a clone.

---

## 4. Reference code inventory

`REFERENCE/project_dia` is a complete, working ground station built for the Dhritvan
Space Lab (SJCIT): FastAPI + SQLite server, plain-loop Python worker, REST with a
long-poll mission endpoint, `X-Worker-Token` shared secret, Argon2id hashing, Podman
Kube Play + Quadlet deployment. The replacement is a single idempotent `deploy.sh`
over Podman Compose (plan.md V18); the reference's deployment model was not reused.

Its architecture differs from Aagasa deliberately: no Go, no PostgreSQL/MongoDB/Redis,
no gRPC, no approval workflow with reservation buffers, and the worker predicts its own
passes. Per `spec.md` §25 it is input, not the target design.

### Reuse

| Source | What to take | Used by |
|---|---|---|
| `src/project_dia/rotor.py:7-22` | G-550 command sequence: `W{az:04d} {el:03d}` terminated with `\r`, park is `W0000 000`, clamp az `0..359` / el `0..90`, `serial.Serial(port, baud, timeout=2)`, open-write-close per command | V4 |
| `src/project_dia/recorder.py` | SatDump invocation shapes: `satdump live <pipeline> <out>` and `satdump record <out> --baseband_format ziq`, flags `--source/--frequency/--samplerate/--gain/--bias`; list-argv (never a shell string); terminate-then-kill stop | V4, V10 |
| `src/project_dia/worker/state.py:32-44` | Durable local state via `mkstemp` + `os.replace` atomic write; event dedup by ID | V9 |
| `src/project_dia/predictor.py:39-76` | Skyfield `find_events` rise/culminate/set triple extraction, `wgs84.latlon` observer, altaz derivation | V6 |
| `src/project_dia/security.py:9` | Argon2id parameters: `time_cost=3, memory_cost=65536, parallelism=4, hash_len=32, salt_len=16` | V3 |
| `src/project_dia/tle.py:23-31` | CelesTrak `gp.php?CATNR=<id>&FORMAT=TLE` request shape and TLE line regexes. Endpoint behaviour must still be re-verified live per `spec.md` §11.3 | V5 |
| `Containerfile_worker`, `worker.yaml`, `*.kube` | Reference deployment layout (Quadlet + Kube Play); superseded by Podman Compose in V18; the rotator `/dev/ttyUSB0` and SDR `/dev/bus/usb` device mounts carried over into `deployment/compose.yaml` | V1, V18 |

### Adapt

- `rotor.py:17-22` `_send` swallows every serial exception into a `print`. Violates
  RULES §13 (no silent error swallowing) and §15 (hardware safety). The Aagasa rotator
  controller must surface a typed error and drive the safe/park path.
- `worker/state.py:65-70` `next_event_id` is O(n) and not collision-safe across
  restarts. Aagasa uses server-assigned or content-derived deterministic IDs.
- `recorder.py:70` sleeps a fixed 3 seconds to decide whether SatDump started. Needs a
  deterministic readiness/failure check.

### Reject

- `RotorTracker.track_pass` (`rotor.py:31-52`) blocks on `time.sleep` until AOS and
  linearly interpolates az/el between rise, max and set. Aagasa executes a
  Server-supplied pointing timeline (worker-spec §10).
- `overlap.py` resolves overlaps by priority then max elevation. Aagasa treats *any*
  overlapping station reservation as a hard conflict, enforced transactionally in
  PostgreSQL (`spec.md` §13.3).
- Worker-side pass prediction (`worker/runner.py:76-103`). Scheduling is Server-owned.
- SQLite, FastAPI, and the REST long-poll mission transport.
- Random root password written to `data/admin-login`. Aagasa bootstraps `root`/`toor`
  with a forced change (`spec.md` §17.1).

---

## 5. Open contract question for V8

`spec.md` §2.2 and worker-spec §10 have the Worker execute a tracking timeline supplied
by the Server. `spec.md` §11.3 and worker-spec §22 require the Worker to refresh TLEs
independently every 24 hours while the Server is unreachable.

If the PassPlan carries only a precomputed pointing timeline, a Worker-side TLE refresh
cannot influence pointing and the requirement is inert. If the Worker re-derives
pointing from a fresher TLE, it performs orbital computation the Server is meant to own.

Proposed resolution, to be confirmed before the V8 PassPlan schema is fixed: the
PassPlan carries **both** the pointing timeline and the TLE used to generate it, and the
Worker may recompute *pointing only* — never timing, never scheduling, never pass
selection. Recorded here so the protocol is designed with room for it.

---

## 6. V0 exit criteria

| Criterion (plan.md V0) | Status |
|---|---|
| repository structure documented | done, §1 |
| reference code understood | done, §4 |
| build/development commands documented | done, §3 (commands are intended; nothing builds yet) |
| architecture decision notes captured | done, §1 and §2 |
| `REFERENCE/` clearly separated from production implementation | done, §1 and `REFERENCE/README-AAGASA.md` |

---

## 7. V1 runtime foundation

Each runtime starts independently and reports health. No domain behaviour exists yet:
no schema, no auth, no scheduling, no hardware control.

### What runs

| Component | Entrypoint | Listens on |
|---|---|---|
| Server | `server/go/cmd/aagasa-server` | HTTP `:8080`, Worker gRPC `:9090` |
| Prediction service | `aagasa-prediction` | gRPC `127.0.0.1:9091` (loopback by default) |
| Worker | `aagasa-worker` | no listener; outbound gRPC only |
| Client | Flutter web/Android/iOS | n/a |

### Contracts

`proto/aagasa/prediction/v1/prediction.proto` carries `PredictionService.Health`.
`proto/aagasa/worker/v1/worker.proto` carries `WorkerService.Ping`, hosted by the
Server and called by the Worker. Both are V1 connectivity surfaces; prediction RPCs
(V6), PassPlan delivery (V8) and state synchronization (V12) extend them later.

### Health model

- `GET /health` - liveness. Returns `{"status":"ok","version":...}` and nothing about
  internals.
- `GET /readyz` - readiness. 200 when PostgreSQL, Redis, MongoDB and the prediction
  service all respond; 503 otherwise. Reports component names and booleans only;
  addresses and driver errors go to the log, not the response.

The Server logs a warning and starts anyway if the prediction service is not yet up,
so pod start order does not matter. Datastores must be reachable at startup.

### Security decisions taken in V1

- The Worker gRPC surface is behind a shared-secret interceptor using a constant-time
  comparison; every rejection path returns `Unauthenticated`.
- Both the Server and the Worker refuse to start on a shared secret under 32
  characters, and the Server refuses to start without every datastore URL.
- Worker TLS defaults to on; disabling it is explicit and logs a warning.
- Worker state and recording directories are created `0700`.
- Containers run as non-root users.
- No secret is written to a log, an image, or a committed file.

### Deferred from V1, deliberately

- MongoDB collections and PostgreSQL schema (V2). `store.Database()` exists but no
  collection is touched.
- Client authentication and role-gated navigation (V3/V13). The shell shows four
  destinations as explicit empty states; Public History and Administration arrive
  with roles.
- Podman image builds were not executed. The Containerfiles are written but unbuilt;
  the Worker image additionally needs `deployment/worker/satdump.deb`, which is not
  committed. The Compose stack and `deploy.sh` were validated with
  `podman-compose config`, not a live run; no host has had containers started on it.

---

## 8. V2 data model

Full detail in [data-model.md](data-model.md). Summary of what V2 added:

- PostgreSQL schema: 12 tables, 8 enums, applied by the `aagasa-migrate`
  command using goose with embedded SQL. Migrations never run as a side effect
  of starting the Server.
- Critical invariants pushed into the database rather than Go: the station
  overlap exclusion constraint, the Argon2id password format check, a single
  Root, and an append-only audit trigger.
- Thin repositories in `internal/store` mapping rows to `internal/domain`
  types and translating constraint violations into typed errors
  (`ErrStationOverlap`, `ErrDuplicate`, `ErrNotFound`).
- MongoDB `pass_executions` and `worker_telemetry` collections with indexes,
  including a 14-day telemetry TTL.
- Redis session primitives keyed by token hash.
- `internal/filestore` for recordings and uploaded pipelines, with path
  confinement.

Deferred deliberately: no business logic reads or writes any of this yet. No
bootstrap Root row is created (V3), no TLE is fetched (V5), no pass is
scheduled (V7).

---

## 9. V3 authentication, RBAC and first-run setup

Full API detail in [api.md](api.md).

- Argon2id hashing (`internal/auth`), t=3, m=64 MiB, p=4, 32-byte key, random
  16-byte salt per call, constant-time comparison. A malformed hash is a
  distinct error from a wrong password so a corrupt record is never mistaken
  for a failed login.
- Bootstrap `root`/`toor` created on first start with a forced password
  change. Every endpoint except `/api/auth/me`, `change-password` and `logout`
  returns 403 until the change happens, which is what retires the credential.
- Redis-backed sessions, revoked on logout and on password change.
- RBAC as pure functions in `internal/auth/rbac.go`, so the rules are testable
  without HTTP. Root is undeletable and undemotable, nobody can be promoted to
  Root, only Root may create Admins, and Root cannot delete itself. Migration
  00002 enforces the Root rules with a database trigger too.
- First-run setup writes station, band, hardware, scheduling configuration and
  the Worker in one transaction, then marks the system initialized. It runs
  once.
- `aagasa-rootpw` resets the Root password from the server host without the
  password touching argv, shell history or logs.

Deferred: login rate limiting and lockout, left to V16 because spec.md
section 18 asks V1 authentication to stay simple.

### Test isolation

`internal/testsupport` gives each integration test its own throwaway
PostgreSQL database, so packages can run in parallel without resetting each
other's schema. Without it, `go test ./...` was racy across the `store` and
`httpapi` packages.

---

## 10. V4 station hardware bring-up

Full detail in [hardware.md](hardware.md). `worker/src/aagasa_worker/hardware/`
holds the rotator, SDR probe, SatDump control and the station lifecycle.

- G-550 control reusing the reference command sequence, with typed errors
  instead of the reference's swallowed prints, and a reject/clamp split
  between commanded and tracking positions.
- RTL-SDR detection via `rtl_test`, with a station-level capture lock so a
  second concurrent capture is refused.
- SatDump pipeline discovery and process control with deterministic results.
- Safe startup parks the rotator; safe shutdown stops any capture then parks.
  Both continue past a missing device so the Worker keeps reporting.
- `aagasa-hardware` bring-up CLI, usable as a deployment check.

### Corrected assumption

SatDump pipeline files are JSONC, not JSON. Parsing them strictly found 81
pipelines on the verified installation; parsing them the way SatDump does
finds 185. Discovered only by running against a real SatDump install rather
than a fixture.

### Hardware not present

No G-550 and no RTL-SDR are attached to this machine, so physical motion and
real RF capture are unverified. `hardware.md` lists exactly what that leaves
open, including an unconfirmed `--source rtlsdr` string that must be checked
during real bring-up before V10.

---

## 11. V5 satellite catalogue and TLE pipeline

- `internal/tle` parses and validates element sets, including the modulo-10
  checksum, and provides the CelesTrak and SatNOGS adapters behind a
  `Provider` interface with an ordered fallback chain.
- `internal/catalog` owns the catalogue, the 24-hour refresh policy and the
  last known-good rule: a provider outage or a corrupt response never discards
  stored orbital data.
- Root-only catalogue administration over REST, plus an Admin-triggered
  catalogue-wide refresh and a Root-only metadata re-enrichment path.
- An hourly background sweep refreshes anything older than 24 hours, with a
  sweep at startup so a Server that was down catches up.

### Provider behaviour, verified live

Both endpoints were checked against the real services rather than assumed
(spec.md section 11.3 requires this). CelesTrak answers `404` plus
`No GP data found` for an unknown object and returns CRLF with a
space-padded title; SatNOGS answers `200` with `[]` and prefixes titles with
`0 `. Live tests covering both run under
`AAGASA_TEST_LIVE_PROVIDERS=1` and are skipped by default so the normal suite
stays offline.

### Gap found during live testing

A transient SatNOGS failure while adding a satellite left its metadata empty
with no way to fill it in short of deleting the record and its orbital
history. Added `POST /api/satellites/{id}/refresh-metadata`.

---

## 12. V6 pass prediction

One predictor, in Python, reached over gRPC. The Go control plane computes no
orbital mechanics and the Worker carries no orbital libraries; this is checked
by grep as well as by design (spec.md section 12).

- `server/python/src/aagasa_prediction/predictor.py` uses Skyfield and SGP4 to
  produce AOS, TCA, LOS, the azimuths at each, maximum elevation, duration and
  an optional pointing timeline.
- Skyfield's builtin leap-second data is used, so prediction needs no network
  access at runtime.
- `PredictPasses` on the existing `PredictionService`. Bad input returns
  `INVALID_ARGUMENT` with a specific message; internal failures return
  `INTERNAL` with no traceback.
- `internal/prediction` on the Go side maps `INVALID_ARGUMENT` to a typed
  `ErrInvalidRequest`, so the API layer can tell a bad request from an outage.
- The track is sampled from AOS to LOS inclusive, with the last sample pinned
  to LOS so the Worker has an explicit end point. Point count is capped.

### Determinism

The same elements, observer, threshold and window always produce the same
result. Asserted both in the Python unit tests and across two gRPC calls from
Go, because scheduling decisions are built on these numbers.

### Bug found

`build_satellite` read `satellite.model.error_message`, which sgp4 does not
define. Any malformed TLE raised `AttributeError` instead of the intended
`PredictionError`, so the service would have returned INTERNAL rather than
INVALID_ARGUMENT for the most likely bad input. Found by a test that asserted
garbage elements are rejected.

### Layering note

sgp4 does not verify TLE checksums, and this service deliberately does not
either. Checksum validation happens once on ingest in the Go `tle` package;
a second implementation here would risk the two drifting apart.

---

## 13. V7 scheduling core

`internal/scheduling` is the only place a pass is created, approved or
cancelled.

- `statemachine.go` holds the lifecycle from spec.md section 14. Its
  live-status set is asserted to match the database constraint's WHERE clause,
  so the application and the database cannot disagree about what conflicts.
- `rules.go` holds buffers, overlap, lead time, band mismatch and visibility
  as pure functions, testable without a database.
- `service.go` composes them and lets PostgreSQL enforce the overlap rule on
  insert rather than checking first and inserting after.

### Decisions worth knowing

**Client timings are not trusted.** A request names the pass by start time;
the Server re-predicts, matches within a two-minute tolerance and stores its
own AOS, LOS and elevation.

**Root override is one transaction.** Conflicting passes are cancelled with
`cancelled_by_root_override`, audited, and Root's pass inserted together. A
failure rolls back the cancellations, so the station is never left both
double-booked and empty.

**Root passes are approved through the normal path**, created pending and
immediately approved, so the approver is recorded the same way as any other.

**Private passes 404 rather than 403.** Answering "forbidden" would confirm
the pass exists, which is itself the disclosure section 16 forbids.

### Worker availability

Scheduling is refused while the station worker is offline (section 6.3).
Until V12 drives `connection_state` from the heartbeat, it stays `offline`
after first-run setup, so a live deployment must mark the worker online
before scheduling will succeed. Tests set it explicitly.

### Bug found

`RequestPassWithRootOverride` created the pass already `approved` and then
called `ApprovePass`, whose WHERE clause only matches `pending_approval`. The
whole override transaction failed with "not found". Caught by the override
tests; fixed by creating pending and approving through the normal path.

---

## 14. V8 executable PassPlan

Full detail in [passplan.md](passplan.md).

- `proto/aagasa/worker/v1/passplan.proto` defines the plan and a plan set.
- `internal/passplan` generates, hashes, serializes and persists them.
- Generation happens on approval, including Root override. A generation
  failure fails the approval rather than leaving a pass no Worker will run.
- Migration 00003 adds `pass_plans`, holding the generation hash and the exact
  protobuf bytes handed to the Worker.

### The V0 contradiction is now resolved in the contract

The plan carries both the pointing timeline and the elements it came from.
The Worker executes the timeline, and may recompute *pointing only* from
fresher elements during a long outage; never timing, never pass selection,
never scheduling. See passplan.md for the reasoning. This is now baked into
the protobuf comments, so changing it later is a contract change.

---

## 15. V9 Worker local durable execution

The Worker now holds its own schedule and runs it without the Server.

- `planstore.py` is the durable local store. **SQLite**, not a JSON file:
  real transactions, and it survives a crash mid-write. It is standard
  library, so this costs no dependency. WAL plus `synchronous=FULL` means a
  committed plan is genuinely on disk.
- `executor.py` decides *when* a pass runs. *How* it runs is a handler,
  supplied from outside, so the loop is testable without hardware and V10 can
  plug the station in without touching scheduling.
- `sync.py` plus the new `SyncPassPlans` RPC pull the desired state.

### Reconciliation rules

Three rules make a resync safe (spec.md section 6.2):

- a plan in a terminal state keeps it, so a completed or missed pass is never
  resurrected by a resync
- a plan whose content is unchanged keeps its execution state
- a plan that vanishes from the desired state is cancelled locally, not
  deleted, so its history survives

### Recovery

On startup the Worker fails any pass that was executing when it stopped rather
than resuming midway (spec.md section 20), and marks any pass whose window
closed as missed. Neither is ever replayed.

### Independence

The execution loop runs on its own thread and reads only the local store. It
does not know or care whether the Server is reachable; a Server outage stops
synchronization and nothing else.

### Verified live

With three real approved passes synced, the Server and all three databases
were killed. The Worker stayed up, kept its full schedule, **executed a pass
with no Server present**, queued the result locally, and after a restart still
held the remaining two passes without replaying the completed one.

---

## 16. V10 real pass execution

`pass_execution.StationPassHandler` replaces the V9 placeholder and drives the
rotator, the SDR and SatDump through the sequence in plan.md V10. Detail in
[hardware.md](hardware.md).

The seam held: V9 built the scheduling half against a `PassHandler` protocol,
and V10 swapped in the real implementation without touching the executor, the
plan store or the state machine.

### Bug found

Pointing failures were deduplicated by message text, but each message names the
angle it tried to command, so a broken serial link produced one entry per track
point in both the log and the execution detail. The code carried a comment
claiming it recorded the failure once; it did not. Now counted explicitly.

### Hardware still outstanding

The sequence is verified against the real SatDump binary with a recording
serial transport, but no G-550 and no RTL-SDR are attached to this machine.
hardware.md lists exactly what that leaves unproven, including the
still-unconfirmed `--source rtlsdr` string from V4.

### Server-side status

A pass reaching `EXECUTING` and `COMPLETED` is recorded in the Worker's local
history. Reflecting that back into the Server's pass lifecycle is V12, which
owns result synchronization.

---

## 17. V11 recordings and idempotent upload

### Identity is the whole idempotency story

A recording id is `sha256(pass_id || 0x00 || relative_path)`, computed by the
Worker. Content is deliberately **not** part of it: a partially written file
must not become a different recording, or a retry after a truncated capture
would upload as a second one.

The id is the primary key of `recordings`, so a retry after a lost
acknowledgement is recognised and no duplicate is created (spec.md section
19.3). The Server answers such a retry before reading the body, so the content
is not transferred again.

### Transfer

`UploadRecording` is a client-streaming RPC: metadata first, then chunks. One
RPC still carries one whole file; this is not a resumable protocol, per
spec.md section 19.3's instruction not to add chunk complexity before real
sizes prove it necessary.

Content lands in a temporary file and is renamed into place only after the
checksum and size match, so an interrupted or corrupted transfer never leaves
a partial file where a good one belongs.

### Retention

- Normally only recordings the Server has confirmed **and** that are past the
  twelve hour window are deleted, oldest first.
- Above ninety percent disk usage the Worker may reach past the window, but
  still takes confirmed copies before anything unconfirmed, and stops as soon
  as usage drops back.
- An unconfirmed recording is the last thing ever considered, because losing
  it would lose it for good (worker-spec section 15). A failed upload never
  deletes anything.

### Outcome reporting

`ReportExecutions` closes the loop V10 left open: a pass the Worker ran now
reaches `COMPLETED`, `FAILED` or `MISSED` on the Server. Re-reporting is safe,
and a pass already in a terminal state is never rewritten.

### Bug found

Adding the recordings table shifted timing enough to expose a real gap: ten
concurrent pass requests began surfacing PostgreSQL **deadlock (40P01)**
instead of clean conflicts, which a user would have seen as a 500. Exclusion
constraints make concurrent inserters wait on each other, and enough waiters
can deadlock. `CreatePass` now retries a bounded number of times with a
growing pause, so the answer is always either the row or a genuine overlap
conflict. Verified over five consecutive runs.

---

## 18. V12 Server/Worker synchronization

### Identity moved to the Server

The Worker is now configured with `AAGASA_WORKER_NAME` and registers to
resolve its identifiers, replacing `AAGASA_WORKER_ID` and `AAGASA_STATION_ID`.
Those were previously database identifiers an operator had to paste into
station configuration, and the Worker had in fact been sending its *name*
where a UUID was expected, which the Server silently dropped.

The resolved identity is cached in the durable store, so a Worker that
restarts during an outage still knows who it is and keeps executing.

### Heartbeat and offline detection

Heartbeat every 5 seconds, offline after 15 (spec.md section 7). A background
monitor on the Server flips stale workers and reports each transition once.
The heartbeat carries the generation the Worker holds and how much it is still
carrying, so a backlog is visible while a station is away.

The response says whether the Worker is current, so it pulls the desired state
only when the Server says it has moved.

### The reconnect cycle is ordered

Resolve identity, report outcomes, upload recordings, then reconcile the
desired state. Results go first because only the Server can keep them
permanently. Execution runs on its own thread and is untouched by any of it.

### Two bugs found, one serious

**`ReportExecutionsRequest` was read from the wrong module.** Those messages
are declared in `recording.proto`, so they live in `recording_pb2`, not
`worker_pb2`. The same mistake affected the upload path.

**The resulting `AttributeError` killed the whole Worker process**, because
the reconnect loop only caught `grpc.RpcError`. That directly violates the
requirement that the Worker keep running. The loop now catches any exception,
logs it and continues: synchronization is a convenience, execution is the job.

### Backoff cap reduced

Reconnect backoff was capped at 300 seconds, so a Worker could take five
minutes to notice a restarted Server. Observed during the live cycle test.
Now capped at 60.

### The required scenario, verified live

All ten steps of plan.md V12, with no manual database surgery on the Server
side: the Worker registered itself, scheduling was blocked while it was
offline and worked once it reported in, the Server was killed, the Worker kept
running and executed a pass, the Server returned, and the two converged. The
recording reached the Server (11264 bytes, checksummed), the outcome moved the
pass to `failed`, and nothing was re-sent.

---

## 19. V13 client core user experience

### What a normal user can now do

Sign in, be forced through a password change when the Server says so, browse
the satellite catalogue, read one satellite with its current TLE and predicted
passes, request a pass, follow its approval and execution status, cancel it,
and download the recordings it produced. Those are the plan.md V13 exit
criteria, and each has a widget test.

### Two server endpoints were missing

The client needed recordings, which until now only the Worker wrote:

- `GET /api/passes/{id}/recordings`
- `GET /api/recordings/{id}/download`

Neither carries its own permission. **The right to a recording is derived from
its pass on every request**, so a download URL that is copied out of the app
is refused for anyone the pass is not for, with `404` to match how the pass
itself hides. The stored filesystem path is never serialized; the client
follows the `download_url` the Server hands it. A recording id must be a
64-character hash, which is checked before any lookup, so the id can never be
mistaken for a path.

### Session handling

The token lives behind a small `TokenStore` interface, implemented over
`flutter_secure_storage` in the app and in memory for tests. Introducing that
interface, rather than mocking the plugin, also keeps the app from depending
on the plugin's option types.

A session is restored at startup and confirmed against `/api/auth/me`. A token
the Server rejects is discarded rather than kept to fail later, and a `401`
during use returns to the login screen once instead of showing an error on
every screen.

The client renders what its role permits, but never decides anything: the
Server refused every unauthorized request in the API tests regardless of what
the UI offered.

### Refusals are presented in the user's terms

A station conflict says the slot is taken and reports the occupied window; it
names no user, and a test asserts the owner cannot be read off the screen. An
offline station, a lead-time refusal and a generic rejection each read
differently, because "try again later" and "choose another pass" are different
instructions.

Loading, empty and error are distinct states everywhere. A completed pass with
no recordings says so; a refused recording list shows an error with a retry,
because an empty list and a refusal must not look alike.

### One product bug found by the tests

`PassDetailScreen` started two requests and handed them to `FutureBuilder`s
that had not been built yet. A request that failed quickly escaped as an
unhandled asynchronous error instead of being shown on the page. The futures
are now marked observed at creation; the builders still receive the error.

### Status is never colour alone

Every state chip pairs an icon with text (client-spec section 32), and the
dark theme is the V1 one, seeded from `#487CE5`.

### Verification

Go 214 tests, Worker 150, prediction 39, client 36. `flutter analyze`
reports no issues. Six of the Go tests are new and cover the recording
endpoints, including a leaked download link, an anonymous download, a
malformed id and metadata whose file has gone.

Not covered: nothing on this machine renders the app against a live Server, so
the client is verified against a scripted HTTP layer rather than end to end.
That belongs to V17.

---

## 20. V14 client admin operations

### The split that shapes this phase

Admin reads how the station is **operating**; Root controls how it is
**configured** (client-spec section 4). Everything in V14 respects that line,
and V15 opens the other side of it.

Administration is one role-gated destination rather than admin controls
sprinkled through the normal-user screens, so an operator's day is unchanged
and an Admin knows where operational work lives. Inside it: Approvals,
Station, Orbital data, History.

### Three server endpoints were missing

The Server had no way to *read* several things it already knew:

- `GET /api/scheduling-config` (Admin) - an Admin approves passes, so they
  must be able to read the rules those approvals are judged by. Writing stays
  Root-only. The read reports the version **in force now**, which is not the
  newest saved one: a change becomes effective only after the newly chosen
  lead time, and a test pins that.
- `GET /api/station` (Admin) - name, location, timezone, active band and
  antennas. Serial ports, baud rates, gains, PPM and the SDR identifier are
  deliberately absent, and a test asserts they do not appear.
- `GET /api/tle-status` (Admin) - freshness across the whole catalogue, from
  one `DISTINCT ON` query rather than one lookup per satellite.

**Missing and stale are separate states.** A satellite that has never had
orbital data is not "very old data"; it reports `has_orbital_data: false`,
carries no age, and is counted separately. The staleness threshold (72 hours)
is returned rather than hard-coded in the client.

### What the Admin screens do

The approval queue shows what a decision actually needs: requester, satellite,
window, band, what will be produced, and whether it will be public. Requester
names come from the account list; where the Server withheld the id, the card
says the requester is withheld rather than inventing one. Approve and reject
are the Server's decision reported back, and a refusal (a pass that stopped
being pending) is shown, not swallowed.

The station tab pairs reachability with the backlog a Worker is still holding,
and says plainly that scheduling is refused while the station is unreachable.
History keeps cancelled and rejected passes visible, because history that
drops its failures is not history.

A TLE refresh reports what it **kept**: a provider outage preserves the last
known good data (spec.md section 11.1), which is a different outcome from a
plain failure and reads differently.

### Nothing is trusted to the client

Role checks in the client decide what is offered, never what is permitted.
The Go tests assert the other side: a normal user gets 403 on the station
status, the TLE status and the scheduling config, and an Admin gets 403 on
writing the config.

### One bug found by the tests

`setState(() => _future = api.tleStatus()..ignore())` returns the future from
the arrow body, and Flutter rejects a `setState` callback that returns a
Future. It threw on first paint of the orbital-data tab. Fixed with a block
body.

### Not in this phase

No realtime channel. client-spec section 21 wants WebSocket updates for worker
state and pass progress, and plan.md assigns it to no phase; these screens
poll with pull-to-refresh instead. Recording management beyond listing and
downloading (deletion, retention) is also absent, deliberately: spec.md
prefers cancellation and override state over destructive deletion, so nothing
here deletes.

### Verification

Go 220 tests, Worker 150, prediction 39, client 57. `flutter analyze` reports
no issues. Six of the Go tests are new and cover the three endpoints,
including the role boundaries in both directions.

---

## 21. V15 root setup and administration

### The complete Root surface

First-run setup from the app, station identity and location, active RF mode,
VHF and UHF antenna settings, rotator and receiver settings, scheduling lead
time, buffers, recording margins, minimum elevation, accounts and promotion,
the audit trail, and Root override. Root can now administer the V1 station
without touching the database, which is the plan.md V15 exit criterion.

The emergency path is unchanged and deliberately independent: `aagasa-rootpw`
on the server host still works when the UI does not, and still never puts the
password in a log, an argument or shell history.

### Five server endpoints, all Root

- `GET /api/station/config` - the whole configuration in one read
- `PATCH /api/station` - identity, location, active band
- `PUT /api/station/bands/{band}` - one band's antenna and radio settings
- `PUT /api/station/hardware` - rotator and receiver
- `GET /api/audit` - the trail

Everything else Root needs already existed: users, satellites, the scheduling
configuration write, and override on `POST /api/passes`.

### A configuration change reports, it does not rewrite

Every change answers with `upcoming_passes_unchanged`. **That is a report,
not a refusal.** Passes already pending or approved keep the settings they
were accepted under (spec.md section 13.8), and the operator is told how many
stand under the old ones. A test schedules a pass on VHF, switches the
station to UHF, and asserts the committed pass still reads VHF.

The configuration screen says the same thing before anything is touched: a
banner counts the scheduled passes and states that they keep their settings.

### Park angles are checked three times

The client refuses an out-of-range park position before sending it, the
Server refuses it before storing it, and the database refuses it again. That
looks redundant until you remember the value ends up as a command to a real
rotator: azimuth 0..359, elevation 0..90, and nothing else (spec.md
section 9). A test asserts that a rejected value is not stored.

### The audit trail is readable, not raw

The listing returns what, who, which subject and when. The recorded
before-and-after state stays server-side: it is written for forensic reading
at the database and names configuration that should not be casually
browsable. There is no update or delete counterpart, because the trail is
evidence. The client turns wire names into sentences and falls back to the
raw action for anything unmapped, which is better than hiding it.

### Root override is confirmed in the words that matter

Offered only after a station conflict, and only to Root. The dialog says the
pass holding the slot will be cancelled and its owner told why. It is never
offered for a lead-time refusal, because that rule binds Root too
(spec.md section 13.7).

### First run is a gate, not a dead end

A signed-in user now passes through a check of `GET /api/setup`. Root gets
the setup form; anyone else is told plainly that the root account has to
complete setup, rather than meeting a wall of `409 not_initialized` on every
screen. If the check itself fails, the app carries on into the shell rather
than blocking on a question that is not the user's to answer.

### Test-harness changes

The fake server now matches a route key that names a method
(`POST api/setup`), which a test needs when one verb must fail while another
succeeds on the same path - exactly the first-run case. Route keys are also
matched longest-first so a query-string route wins over the bare path.

### Verification

Go 230 tests, Worker 150, prediction 39, client 76. `flutter analyze` reports
no issues. Nine of the Go tests are new and cover the five endpoints,
including the Admin/Root boundary in both directions, rejected values leaving
nothing stored, and the audit trail containing actions taken long before the
endpoint existed.

### Still open after V15

No realtime channel (client-spec section 21). No notifications
(section 31). The client has still never been rendered against a live Server;
that is V17.

---

## 22. Running the stack locally, and two things that blocked it

Signing in from the web build failed with "cannot reach the server". Two
separate causes, both now fixed.

### Nothing was listening

The Server was not running. The local sequence is: PostgreSQL, Redis and
MongoDB; `aagasa-migrate up` and `mongo-init`; the prediction service; then
`aagasa-server`. The Server needs `AAGASA_POSTGRES_URL`, `AAGASA_REDIS_URL`,
`AAGASA_MONGO_URL` and a `AAGASA_WORKER_SHARED_SECRET` of at least 32
characters, and refuses to start without them.

Verified live: `root` / `toor` returns a token with
`must_change_password: true`, which is the documented first-start behaviour.

### The browser would have blocked it anyway

The Server sent no CORS headers, so a page served from another origin could
never call the API however healthy the Server was. `WithCORS` now wraps the
mux:

- `AAGASA_ALLOWED_ORIGINS` names origins exactly; development additionally
  allows any localhost port, because the Flutter dev server picks one.
- The allowed origin is echoed back rather than `*`, the reply varies on
  `Origin`, and `Access-Control-Allow-Credentials` is never sent - the session
  is a bearer token in a header, not a cookie.
- A preflight from an unknown origin is 403 with no CORS headers.
- `Content-Disposition` is exposed so a recording download can read its name.
- With nothing configured the middleware is not installed at all.

Verified end to end: the release web build served on `127.0.0.1:5000` signs in
against the Server on `127.0.0.1:8080`, and `https://evil.example` is refused.

### The keyboard assertion

`data.physical != 0 && data.logical != 0` in Flutter's `KeyEventManager`. The
framework already ignores an event with **both** fields zero; the assertion
fires when exactly one is, which a browser produces for a key it cannot map -
most visibly when a password manager fills the sign-in form. In a debug build
that kills the app.

`main()` now filters `PlatformDispatcher.onKeyData` and drops any event
missing either field before the framework sees it. Such an event carries no
usable key, so nothing is lost; everything else passes straight through. This
is an upstream Flutter issue, not an Aagasa one, and the workaround is
confined to the entry point.

### Verification

Go 236 tests, client 76. Six of the Go tests are new and cover CORS,
including the localhost allowance refusing `localhost.evil.example`.

---

## 23. Catalogue administration, and why there is no sign-up

Two gaps reported from a live session.

### Root could not add satellites: a real omission

plan.md lists "satellite management" under V14 and "satellite catalogue"
under V15. V14 shipped orbital-data *freshness* and the catalogue-wide TLE
refresh but not catalogue *membership*, and V15 did not catch it. The Server
endpoints existed the whole time; nothing reached them.

Now, on the catalogue screens rather than in a separate admin corner, because
there is one catalogue and it should not be duplicated:

- Add by NORAD catalog number, Root only, offered as an action and again in
  the empty state.
- Edit name, description and open-for-scheduling from the satellite page.
- Refresh display information.
- Remove, with a confirmation.

The refusals are worded for the person reading them. A catalog number no
provider knows is not "502 no_orbital_data" but a statement that no provider
has that number and the number may be wrong. A satellite that cannot be
removed because passes reference it says so and points at closing it for
scheduling instead, which is the reversible alternative
(spec.md section 13 keeps history).

The catalog number itself is not editable: it is the satellite's identity and
the key every orbital-data lookup uses.

### One more setState arrow bug

`setState(() => _future = api.satellites())` hands `setState` the future as
its return value, which Flutter rejects at runtime. It sat in the satellite
list's reload path since V13 and only fired now because nothing had reloaded
that screen in a test. Same shape as the V14 orbital-data bug; both are now
block bodies.

### There is no sign-up, by design

No specification mentions self-registration. spec.md section 17 has Root
creating Admins and Admins creating normal users, and section 17.3 lists what
a normal user can do, starting at "authenticate". Adding a public sign-up
would be a change to the security model, not a missing feature, so it stays
out.

What was missing was saying so: the login screen now reads "Accounts are
created by a station administrator", and a test asserts no sign-up affordance
exists.

### Verification

Client 84 tests, `flutter analyze` clean. Eight are new and cover the role
boundary on adding, both refusals, edit and remove, and the absence of a
sign-up route.

---

## 24. The approval failure, and three defects behind it

Approving a pass returned 500, and afterwards the pass could be neither
approved nor rejected. Three separate faults, all mine.

### The station had no centre frequency, because no screen asked for one

`no frequency configured for band vhf`. The V15 setup wizard collected
antenna, sample rate and gain, but not the centre frequency, and the
configuration screen could not set one either. Without it no pass plan can be
built, so every approval was doomed from first-run setup onwards.

Both screens now carry it. Setup requires it and will not submit without one;
the configuration screen shows it, and a band that is missing one says
plainly that passes on it cannot be approved until it is set.

### Approval was not atomic, which is what made it unrecoverable

`decide()` approved the pass and then generated the plan. When generation
failed the pass was already approved, so the 500 was reported over a change
that had actually happened, and the operator could not reject it either -
approved to rejected is not a legal transition.

The PlanGenerator contract in the same file says "a pass that cannot be turned
into a plan must not stay approved". The code did the opposite. The plan is
now built **before** the status changes, and a failure leaves the pass pending
so it can still be rejected. A plan stored for a pass that is not approved is
invisible to the Worker, because `ListPassPlansForStation` joins on
`status = 'approved'`, so the failure order is safe.

### Both errors were reported as 500

A missing centre frequency is a configuration gap an operator can fix, and a
second approval is somebody else getting there first. Neither is a server
fault. `ErrStationNotConfigured` now maps to `409 station_not_configured`
naming the band and the setting, and `ErrInvalidTransition` to
`409 invalid_state`.

### Why no test caught it

The HTTP test environment built its scheduler with a **nil** plan generator,
so approval over the API never generated a plan in any test. The test setup
fixture also omitted the centre frequency, exactly like the real wizard.

Both are fixed: the test environment now uses a real `passplan.Generator`, and
the stub predictor returns a pointing timeline so plans can actually be built.
Every existing approval test now exercises the real path, and three new tests
cover the failure directly: approval refused with the pass left pending and
still rejectable, a successful approval storing a plan with the configured
frequency, and deciding twice answering 409 rather than 500.

### Left behind on the live station

Two passes are approved with no plan, from before the fix. The Worker ignores
them. They cannot be repaired in place - approved is terminal for a decision -
so the way out is to cancel them and request again once the frequency is set.
Nothing was edited in the database to hide this.

### Verification

Go 239 tests, client 87, Worker 150, prediction 39. The Worker rode out the
server restart and reconnected on its own, which is the V12 behaviour holding
up live for the third time.

---

## 25. Two protections that between them made every account undeletable

`DELETE /api/users/{id}` returned 500. The cause was not the account or the
handler: two safety rules collided.

`audit_records.actor_user_id` was declared `ON DELETE SET NULL`, so removing a
user made PostgreSQL **update** their audit rows. The audit trail is
append-only, and its trigger refused the update:
`audit_records is append-only`. Every login is audited, so in practice **no
account that had ever signed in could be deleted**, and the failure surfaced
as an internal error.

Nulling the actor was the wrong behaviour anyway. An audit trail exists to say
who did something; erasing the actor to tidy up after a deleted account
destroys exactly the evidence the trail is for. Migration 00006 drops the
foreign key and keeps the column: the identifier stays, and a reader who
cannot resolve it to a current account learns something true - the account is
gone. The client already renders an unresolvable actor as its identifier.

### A second, correct refusal was also reported as 500

`passes.requested_by` is `ON DELETE RESTRICT` on purpose: a pass and its
history outlive the account that requested it. That refusal is right, but it
too came back as an internal error. `store.ErrReferenced` now carries
SQLSTATE 23503 and the API answers `409 in_use`. The accounts screen says the
account has passes on record and that the history stays either way.

Note the two are different: an account with passes still cannot be deleted,
by design. What changed is that an account with only audit history now can,
and both answers are honest.

### Retiring an account with history is still not possible

There is no deactivate flag, so an account that has requested a pass can
never be removed or disabled - only left in place. That is a design decision
worth making deliberately rather than inventing a schema change here, and it
is recorded as an open question for V16.

### The approval 409 was the fix working

The same session also showed `409` on approve. That is the V15 fix behaving
correctly: the station still has no centre frequency, so the plan cannot be
built, and the pass stays pending and rejectable instead of being half
approved.

### One flaky test, not yet explained

`TestConcurrentRequestsCannotRaceThroughTheConflictCheck` failed once during a
full parallel run, taking 48s against a normal 0.2s, while migrations were
being applied to the same PostgreSQL container from another process. It has
passed every run since, in isolation and in full sweeps. It sits in the V11
deadlock-retry area, so it is recorded here rather than dismissed.

### Verification

Go 242 tests, client 88, Worker 150, prediction 39. Migration 00006 applied to
the live database; the Worker reconnected by itself after the server restart.

---

## 26. V16 observability, recovery and operational hardening

Most of this phase was already built, in the places that needed it at the
time: structured logs from V1, heartbeat and offline detection from V12,
reconnect backoff from V9 and V12, restart-safe state from V9, safe hardware
shutdown from V10, last-known-good orbital data from V5. V16 closed the two
gaps that were left and then went looking for evidence that any of it works.

### Storage pressure, which nothing handled

The Server had no idea how full its disk was. A ground station fills its disk
with recordings and nothing else, so this was a matter of when.

- `filestore.RecordingsUsage` reads the recording filesystem, using the
  blocks available to us rather than the ones reserved for root.
- An upload is refused **before its body is read** when it would not fit
  alongside a one gigabyte reserve, with gRPC `RESOURCE_EXHAUSTED`. The
  Worker keeps its copy and retries, so refusing costs a delay and nothing
  else.
- `/readyz` reports free bytes, percentage used and whether uploads are being
  accepted. It deliberately **does not** fail readiness on a full disk:
  uploads stop, but scheduling, approving and browsing all still work, and
  taking the API out of service would help nobody.
- Being unable to measure the filesystem allows the write. Refusing every
  upload over a failed measurement is worse than letting the write fail on
  its own.

The Worker now refuses a pass its disk cannot hold, before the antenna moves.
Raw capture is predictable - four bytes per sample for the recording window -
so the estimate is honest, and the refusal names both numbers. Filling the
disk mid-pass loses that recording anyway and endangers the ones already
waiting to upload. The antenna is still parked afterwards: a refused pass
leaves the station safe like any other.

### Worker state changes are audited

spec.md section 22 lists worker state changes as auditable and they were only
logged. `worker.registered`, `worker.online` and `worker.offline` now leave
records. Transitions only - a heartbeat every five seconds is traffic, not an
event - and a test pins that three heartbeats produce one record.

These records carry **no actor**, because no person caused them. That is only
storable because migration 00006 freed the audit trail's actor column of its
foreign key, which is a pleasing confirmation that the earlier fix was the
right shape.

### Error classification

`station_not_configured`, `invalid_state` and `in_use` were added in the two
sessions before this one, each replacing a 500 that blamed the Server for
something the caller or the configuration had done.

### The drills, run for real

`scripts/failure-drills.sh` exercises what unit tests cannot reach, because it
needs real processes and a real database to take away. Run against the live
stack, **13 of 13 passed**:

| Drill | Result |
|---|---|
| Database stopped | Server stayed up, `/health` still 200 |
| Database stopped | `/readyz` reported 503 |
| Database restarted | Server recovered by itself |
| Server killed | Worker kept running |
| Server restarted | Worker reconnected untouched |
| Worker killed | Server marked it offline within the timeout |
| Worker restarted | Registered again and reloaded its durable state |
| Storage | Reported, and reported as accepting uploads |

The remaining exit criteria are covered by unit tests rather than drills,
because they can be provoked honestly there: TLE provider outage (V5, last
known good preserved), SatDump failure, SDR failure, rotator communication
failure (V10, with stub binaries and a failing serial port), and recording
upload failure (V11 and V12, retried and never deleted locally).

Network outage is covered indirectly - the Server crash drill is a network
outage from the Worker's point of view, and the provider tests cover the
outbound direction - but a real partition test needs the container network
harness that belongs to V17.

### Not done, deliberately

No metrics endpoint, no tracing, no monitoring platform. plan.md says not to
add one unless later required, and nothing so far requires it.

An account that has requested a pass still cannot be retired. It cannot be
deleted, by design, and there is no deactivate flag. Adding one is a schema
and policy decision for the operator to make rather than something to invent
here; it is the one open question this phase did not close.

### Verification

Go 248 tests, Worker 153, prediction 39, client 88. The drill script passed
13 of 13 against the running stack, including a real PostgreSQL stop and
start and a real kill of both processes.

---

## 27. Specification audit: three features that were never built

Prompted by a question about pipeline JSON, the specifications were read
against the code rather than against the phase plan. Three gaps, all of which
plan.md leaves unassigned to any phase, so none of them would ever have
arrived by working through the phases in order.

### SatDump pipelines: the whole chain above storage

spec.md section 19.1, server-spec section 21 and client-spec sections 11 and
12 all describe it, and V2 built the table, V8 built the PassPlan field. The
part between them did not exist: no way to see what pipelines a station has,
no way to upload a custom one, and no way to choose either. `POST /api/passes`
had accepted `pipeline_id` since V7 and nothing could ever supply one.

Worse, a pass asking for a decode without a pipeline was accepted, and the
Worker quietly fell back to a raw capture. The user asked for a decoded
image and got a baseband file with no explanation.

Now:

- The Worker reports its SatDump inventory when it registers. The Server has
  no SatDump of its own, so this is the only honest source. 185 pipelines
  arrived from the live station on the first run.
- `GET /api/pipelines` lists them, marking standard and custom apart, and
  says whether a station has reported at all - which reads differently from a
  station with none.
- `POST /api/pipelines` accepts a custom definition, validates its
  **structure** only (parses, is an object, names at least one pipeline), and
  stores it. SatDump's semantics are its own business and pretending to check
  them would be a lie.
- A decode mode without a pipeline is refused with `400 pipeline_required`,
  on the override path too: Root bypasses conflicts, not what makes a pass
  executable.
- The request form asks which pipeline once a decode is chosen, and will not
  submit without one. Admins paste custom definitions on their own tab.

**One bug found by running it live.** The inventory travelled with
registration, and the Worker only registers when it has *no* cached identity.
With a cached identity it never registers again, so nothing was ever
reported. The Worker now registers once per run: SatDump can be reinstalled
while it is down, and a cached identity still covers an outage.

### The recordings destination

client-spec section 5 lists Recordings in the information architecture and
section 20 describes it. Recordings were only reachable by opening the pass
that produced them.

`GET /api/recordings` now returns what a caller may see, with scopes
mirroring the pass listing. Visibility is applied **in the query**, joined to
the pass, so a private recording cannot leak through a listing rather than
being filtered out afterwards. A row carries the satellite and the pass time,
so the list reads without a request per row, and opening one goes to the pass
where the download and the pass state sit together.

### No sign-up, still

Checked again and unchanged: no specification mentions self-registration.

### What remains unimplemented, and why

- **Realtime updates** (client-spec section 21) and **notifications**
  (section 31). Both specified, neither assigned to a phase. The screens poll
  with pull-to-refresh instead. This is the largest remaining gap.
- **`raw_and_process`** runs as a live decode only: one RTL-SDR cannot feed
  two SatDump processes. The shortfall is recorded in the execution detail
  rather than hidden.
- **Retiring an account with history**, unchanged from V16.

### Verification

Go 258 tests, Worker 158, prediction 39, client 101. The live station
reported all 185 of its pipelines to the Server, which is the first
end-to-end confirmation that the chain works with real SatDump.

---

## 28. Configuration that looked unsaved, and the upload that was in the wrong place

### The saves were landing; nothing showed it

Four scheduling versions were in the database, so "not being saved" was not
what was happening. A new version becomes usable only after the newly chosen
lead time (spec.md section 13.6), and the read returns the version **in force
now** - so saving 60 seconds and immediately reloading showed the old 1800
back, which reads exactly like a lost save.

`GET /api/scheduling-config` now also returns `pending`: the saved change and
when it takes effect. The configuration screen shows it above the fields and
says the values below are the ones in force until then. The delay stays; it is
simply visible.

### A superseded change could come back to life

Worse, and found while looking: `LatestEffectiveSchedulingConfig` ordered by
`effective_from DESC`. Save a long lead time, think better of it and save a
short one, and both are pending; the short one applies first, and then the
long one's moment arrives and **undoes the operator's latest decision**. The
live station had exactly this set up: 500 seconds pending behind two later
60-second saves.

The rule is now the most recently *created* configuration among those already
in force. Anything saved before it has been superseded and stays superseded.

### Uploading a pipeline JSON belonged with the list

The specification puts upload beside selection - client-spec section 12 lists
"selection of one standard pipeline" and "upload of custom pipeline JSON"
together, and section 11 puts both in the request flow. The first
implementation only offered upload on an administration tab, which is not
where anyone choosing a pipeline is looking.

Upload now sits directly under the pipeline list in the request form, and is
offered even when the station has reported nothing: having no standard
pipelines does not rule out supplying one. The administration tab uses the
same dialog rather than a second copy of it.

The dialog takes a **file** as the specification describes - name shown, type
and size checked, 256 KB ceiling - or pasted text. Obvious nonsense is caught
locally so a doomed round trip is not made, and anything the client cannot
judge goes to the Server, whose refusal is shown as it stands.

### Verification

Go 262 tests, client 104, Worker 158, prediction 39. Two new store tests pin
the precedence rule and the pending report; two new API tests pin what the
read returns with and without a pending change.

---

## 29. The lead time that looked hard-locked

Reported as "hard locked to 60, not changing according to what Root set". The
saves were landing; the rule for when they apply was wrong in a way that made
raising the lead time effectively impossible.

### What the history showed

Seven versions, and three saved within thirteen seconds of each other:

```
09:25:21  60s    effective 09:26:21
09:25:31  1800s  effective 09:55:31
09:25:34  60s    effective 09:26:34
```

The 30-minute change was saved, and would not have applied for 30 minutes.
Seeing no change, the obvious response is to try again - which starts another
30-minute wait. A lead time can never be raised by anyone who checks whether
it worked.

### The rule was applied to changes it was never meant to cover

spec.md section 13.6 says the new policy becomes usable only after "the
configured new lead-time period has elapsed", and the reason is given in the
sentence before it: the new value "must not become an immediate bypass around
the rule". The implementation applied the delay to every change.

A **shorter** lead time can be a bypass, and still waits. A lead time that is
the same or **longer** is a tightening: it cannot let anyone book sooner than
before, so it now applies at once. This is a deliberate departure from the
literal wording of 13.6 in favour of the reason 13.6 gives for itself; the
anti-bypass property the specification protects is untouched, and a test
still pins that shortening waits.

### A superseded change was reported as waiting

Found by the test written for the fix. Relax, then tighten: the tightening
applies immediately, and the relaxation is still queued. It will never take
effect - `LatestEffectiveSchedulingConfig` prefers the most recently created
version - but `PendingSchedulingConfig` was still announcing it as "saved and
waiting", which is a lie to the operator who saved the tightening.

A pending change is now only reported when it was created **after** whatever
is currently in force.

### Verification

Go 265 tests, client 104, Worker 158, prediction 39. Three new tests: a
tightening applies immediately over the API, a shortening still waits, and a
superseded pending change is not reported.

---

## 30. V17 integration test environment

The phase began by proving its own point. The machine slept overnight,
`/tmp` was cleared, and the environment I had been running by hand vanished:
container names, ports, credentials and start-up order all lived in shell
history. V17 exists so that never matters.

### One command

`scripts/dev-env.sh up` brings up databases, schema, prediction service,
Server, Worker and the built client, then seeds fixtures. `down` stops the
services and keeps the data, `reset` removes both, `status` says what is
running. `make env-up` and friends wrap it.

Each environment has a **name and a port offset**, so a test run and a
developer's session can exist at the same time without touching each other.
That is how this phase was verified: a throwaway environment was built from
nothing on shifted ports while the real one stayed untouched.

### Simulated hardware that is actually hardware-shaped

The Worker opens a serial device, runs a binary and reads a pipeline
directory. The stand-ins provide exactly those, rather than special-casing
the Worker:

- The rotator opens a **pseudo-terminal** and answers G-550 commands on it.
  The Worker cannot tell the difference, and every commanded angle is logged.
- SatDump accepts the two invocations the Worker makes, writes output of a
  plausible size, and runs until stopped.
- `rtl_test` reports one device in the real format.
- A fixed pipeline set, so the station reports the same three pipelines on a
  machine with SatDump installed and one without. The first run of this
  proved the point by reporting 185 - the machine's real installation - which
  would not have been reproducible anywhere else.

### Fixtures go through the API

`aagasa-seed` creates the station, the accounts and the satellites over REST,
not by writing rows. Seeding therefore cannot produce a state the API would
refuse, and it exercises first-run setup, user creation and catalogue
addition on every run. It walks each account through the **forced first
password change**, because the Server rightly creates every account needing
one and the fixtures would otherwise be unusable.

It is idempotent, and it recognises an environment that is already configured
with credentials it was not given: that exits 3, and `dev-env.sh` treats it
as "nothing to seed" rather than a failure, so an existing deployment still
starts.

### Three bugs in my own tooling

**`go run` hides exit codes.** It reports any nonzero exit as 1, which
swallowed the seeder's "already configured" answer. The environment now
builds real binaries once.

**`go run` and `uv run` outlive their pid.** Both launch the real program as
a child, so killing the recorded process left the Server holding its port and
the Worker still heartbeating. Services are now started with `setsid` and
stopped as a process group. This is exactly the bug that made the earlier
manual restarts unreliable.

**Seeded accounts were unusable.** They are created needing a password
change, which is correct, and the first integration run failed on it. Seeding
now completes that step the way a person would.

### The integration test

22 checks against a running environment, all passing, no hardware attached:
sign-in as three roles, the role boundaries in both directions, the station
online and reporting its pipelines, prediction, requesting, a decode with no
pipeline refused, an operator unable to approve their own pass, an admin able
to, the plan carrying 81 pointing steps and the configured frequency, the
station taking that plan, visibility, and cancellation.

It deliberately does **not** assert execution. When a satellite next flies
overhead is not something a test can arrange, and a test that waits hours is
not a test. Execution is covered by the Worker's own tests against stub
hardware, and for real by the hardware path.

### The hardware path is kept

`AAGASA_HARDWARE=real` runs the same environment against the attached G-550
and dongle. Simulated hardware proves the software is correct; only real
hardware proves the station is. Both are documented in docs/testing.md.

### Verification

The exit criterion is met: the complete application can be tested without
physical hardware, and the hardware path remains explicit. Go 265 tests,
client 104, Worker 158, prediction 39, plus 22 integration checks and 13
failure drills.

---

## 31. V18 production deployment hardening

V1 left a deployment skeleton that had never been run. Running it found four
faults, any one of which would have stopped a real station from starting.

### The Server could not do TLS at all

The Worker defaults to TLS and sends its shared secret on **every** gRPC call.
The Server had no TLS support, so a real deployment had two options: turn TLS
off on the Worker and put that credential on the wire in clear, or fail to
connect. Neither is a deployment.

`AAGASA_TLS_CERT_FILE` and `AAGASA_TLS_KEY_FILE` now encrypt both listeners,
TLS 1.2 floor. Half a configuration is refused at startup rather than
silently serving plaintext, and an unreadable path fails immediately instead
of at the first connection. Without TLS in `production` the Server warns that
the credential is exposed unless something in front terminates it. Verified
live: TLS 1.3 on the gRPC listener, a real login over HTTPS.

### Four faults found by running it

**Kube Play's `secretKeyRef` reads a Kubernetes Secret, not a Podman secret.**
The documented `podman secret create name -` from plain text is rejected at
pod start with "not valid JSON/YAML". The README's own instructions could
never have worked. This is why V18 abandoned Kube Play for Podman Compose,
where secrets are plain environment variables from a gitignored `deployment/.env`
generated by `deploy.sh`.

**The Server pod could not reach the databases.** They are separate pods so
they restart independently, which means `127.0.0.1` inside the Server is not
the host. Under Podman Compose the Server reaches the datastores by service
name on the compose network (no host networking; the Network table in
deployment/README.md lists what is actually published).

**The TLS key was unreadable.** A root-owned 0600 key stops a service that
drops privileges. The deployment is rootless now, so container root maps to
the invoking user and the key, owned by that user at 0600, is readable with no
uid juggling. Ownership is documented, not provisioned.

**The prediction image would never have started.** Its command was `uv run`,
which wants a writable cache under the service user's home and does not have
one. It now installs into a virtualenv at build time and runs the console
script directly - a container that resolves dependencies to start is a
container that cannot start when the network is down.

### What a clean machine now does

`deploy.sh server|worker|both` takes a host from nothing to healthy, all as
the invoking user and all inside `deployment/`: a directory layout (`data/`,
`tls/`, `web/`) that the containers bind mount, secrets that do not exist yet
in a gitignored `deployment/.env`, a self-signed certificate so the station is
encrypted from the first minute, images built, the schema migrated, services
started through Podman Compose, `readyz` polled. Idempotent, and it never
regenerates an existing secret - rotating the Worker's shared secret behind
its back would take the station off the air.

Ordering is explicit: the compose `depends_on` starts the datastores, the
migrator and then the Server; nginx intends to serve the web client once the
Server is ready. The Worker is independent of the Server service - it executes
an approved schedule through a Server outage and starts whether or not the
control plane is reachable. `restart: unless-stopped` brings a station that
reboots while its databases initialise back up rather than stranding it.

### Backup keeps what cannot be rebuilt

`backup.sh` dumps PostgreSQL and MongoDB and writes a **manifest** of the
recordings rather than their content: content is large and already the
authoritative copy on that host, and a nightly dump that includes it is a
dump nobody runs. Redis is skipped deliberately - a restored session is worth
nothing. Backups are pruned to the most recent fourteen, because an unbounded
backup directory is a second way to fill the disk the recordings live on.

`restore.sh` stops the Server first, demands the word "restore", and finishes
by naming the two things it could not do: recording content, and the station
possibly holding plans the restored Server has never seen.

### Verified, and what was not

Verified: the scripts and images have been exercised up to
`podman-compose config` resolving the full stack with mandatory
`AAGASA_POSTGRES_PASSWORD` and `AAGASA_WORKER_SHARED_SECRET` present and
failing fast without them; the Go binaries (server and migrator) build
clean. The stack is rootless: containers run as the invoking user, services
join the named compose network, all data lives under `deployment/`. Not verified
live: the Worker image, because it needs a SatDump package that is not
committed; a genuinely clean machine, because this one is not; and a real
certificate, because a self-signed one is what a fresh station gets. A live
containerised run to `status: ready` remains to be done on a station.

### Verification

Go 269 tests, client 104, Worker 158, prediction 39. Four new config tests
cover TLS being off by default, accepted when complete, refused when half
configured, and refused when the material cannot be read.

---

## 32. V19 acceptance pass

The seven critical scenarios, run against a live deployment rather than
argued from the code. The run itself is no longer reproduced (the acceptance
harness was removed in the ops cleanup), but its result stands:
**28 passed, 0 failed, 4 deferred.

### What the run actually established

The scenarios that had only ever been reasoned about now have evidence. Root
override cancels a held pass, names why, and leaves an audit record. A
conflicting window is refused without disclosing who holds it. A private pass
is invisible to a stranger and a public one hides its owner. The Server
survives losing its database; the station survives losing the Server and
reconnects on its own; the Server refuses new scheduling while the station is
away and reconciles when it returns. Every orbital-data provider failing
preserves the stored elements exactly.

### Deferred, and why that word

Four steps need a satellite overhead or a full disk. They are reported as
**DEFERRED**, never as passed, because a pass cannot be arranged on demand
and an acceptance run that pretended otherwise would be worse than no run at
all. Two of them - execution and upload after an outage - were proven live in
V10 and V12; the other two are covered by unit tests.

### Two bugs in the acceptance suite itself

`grep -c` prints `0` **and** exits non-zero when it finds nothing, so
`grep -c ... || echo 0` produced `0\n0` and an integer comparison failed. The
check was also weak: it inferred convergence from log lines. It now asks the
Server whether the station holds the current desired state, which is a fact
the Server reports.

The first re-run then failed at the first step, because passes from the
previous run still held their windows. The suite now cancels what an earlier
run left - cancels, not deletes, because history is never destroyed - and is
repeatable.

### The pass that closed two of them

The suite cannot wait for a satellite, so a NOAA 19 pass was approved and left
to run. Fifty minutes later it had executed unattended: capture started at the
recording margin, the antenna was commanded through the pass and parked, the
pass finished `completed`, and a 524288-byte `baseband.s16` reached the Server
and was acknowledged. An unrelated third account then downloaded it in full,
and the same URL without a token was refused.

That is scenario B end to end and scenario E's download, both on evidence
rather than inference.

**The pass exposed a bug in the simulated rotator.** It logged every real
command as unrecognised: its pattern expected three digits of azimuth where
the Worker sends four. Pointing still worked, because the Worker writes and
reads no reply - but the stand-in was recording nothing, so the one thing it
exists to demonstrate was invisible. Fixed and re-verified by feeding it
commands built by the Worker's own `format_command`, not a hand-typed string.

### The one thing standing between this and V1

A real pass on real hardware. The G-550 and the RTL-SDR are attached and both
respond; the rotator parks on command and the dongle enumerates. What has
never happened is a pass tracked, recorded and uploaded with them. V4 and V10
stay unsigned until it does, and that needs about ten minutes during a pass
window with someone willing to let the antenna move.

Everything else in the V1 contract has been demonstrated.

### Verification

Go 269 tests, client 104, Worker 158, prediction 39, plus 28 acceptance
checks, 22 integration checks and 13 failure drills - and one pass executed
end to end on simulated hardware, from request to stored recording.
