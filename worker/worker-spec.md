# Aagasa Worker — Component Specification

## 0. Purpose

The Aagasa Worker is the edge execution agent physically located at the satellite ground station.

Its primary design goal is **reliable, simple execution**, not orchestration.

The Worker receives fully executable PassPlans from the Server, persists them locally, executes them against the physical station, records/processes the resulting signal data, reports state/telemetry, and synchronizes results with the Server when connectivity exists.

The Worker must continue operating for already-synchronized work when the Server is unavailable.

The Worker must not become a second scheduling system.

---

# 1. Relationship to Global Specification

This document refines `spec.md`.

It must never contradict the global project specification.

Global non-negotiables include:

- one Worker + one station in the initial deployment
- Server is authoritative for scheduling
- Worker executes approved PassPlans
- Worker can continue without the Server
- V1 hardware is one RTL-SDR and one Yaesu G-550
- VHF and UHF cannot be used simultaneously
- SatDump is the recording/processing engine
- Python + `uv`
- Podman + Podman Compose (rootless)
- latest-state reconciliation rather than a complex event-sourcing system

---

# 2. Responsibilities

## 2.1 Worker owns

- local configuration
- local durable PassPlan store
- pass execution queue
- pass execution state
- station hardware control
- G-550 serial control
- RTL-SDR operation
- SatDump process lifecycle
- recording/output lifecycle
- local storage management
- local telemetry collection
- connection/heartbeat management
- server synchronization
- local recovery after restart

## 2.2 Worker does not own

- user authentication
- user/role management
- global pass approval
- authoritative scheduling
- cross-user conflict resolution
- public/private authorization policy
- global satellite catalogue administration
- authoritative station configuration policy
- server-side storage ownership
- client sessions

---

# 3. Technology

- Python 3.x version chosen by project baseline
- `uv` for dependency/environment management
- gRPC for Server↔Worker control/state RPC
- WebSocket or equivalent realtime Worker↔Server telemetry channel as defined by the global protocol
- PySerial
- RTL-SDR tooling required by the chosen SatDump/SDR workflow
- SatDump
- Podman
- Podman Compose (`deployment/compose.yaml`), rootless
- a single idempotent `deploy.sh server|worker|both|down`

Avoid unnecessary Python frameworks and abstractions.

---

# 4. Worker Process Model

Prefer a small number of explicit processes/components rather than a sprawling microservice architecture.

A logical decomposition is:

```text
worker
├── configuration
├── grpc client
├── realtime connection / telemetry
├── local state store
├── pass executor
├── tracker
├── rotator controller
├── SDR/SatDump controller
├── recording manager
├── storage manager
└── recovery/synchronization manager
```

These may be implemented as modules inside one Worker application where that is simpler.

Do not split each item into a container merely for architectural aesthetics.

---

# 5. Configuration

Worker configuration must come from environment/config files/secrets, not hard-coded source values.

At minimum:

- Worker ID
- station ID
- Server address
- Server authentication credential/shared secret for V1
- TLS configuration
- gRPC endpoint configuration
- realtime endpoint configuration
- local state directory
- recording directory
- retention/storage thresholds
- TLE provider configuration for offline operation
- G-550 serial port
- G-550 baud rate
- RTL-SDR configuration
- SatDump executable/path configuration

Configuration must support safe defaults where possible, but hardware-specific values must be explicit.

---

# 6. PassPlan Contract

The Worker receives an executable PassPlan.

A PassPlan must contain everything required to execute the operation without a Server request during normal execution.

Conceptually:

```text
PassPlan
├── pass_id
├── station_id
├── satellite_id / NORAD ID
├── TLE/version information
├── execution start/end
├── tracking data/parameters
├── RF band
├── frequency/radio parameters
├── recording mode
├── recording pre-roll
├── recording post-roll
├── SatDump pipeline identity
├── pipeline JSON/reference where applicable
└── generation/version metadata
```

The exact protobuf schema is authoritative once implemented.

The Worker must persist the complete executable plan before treating synchronization as successful.

---

# 7. Local Desired State

The Worker maintains a durable local copy of the latest valid desired execution state.

This includes:

- future PassPlans
- their lifecycle state
- execution results that have not yet synchronized
- recording metadata that has not yet synchronized
- synchronization generation/version

The Worker must not depend on an in-memory queue alone.

A Worker restart must not erase future work.

---

# 8. Execution State Machine

Recommended Worker-side states:

```text
RECEIVED
READY
EXECUTING
COMPLETED
FAILED
MISSED
CANCELLED
```

The Server's application-level pass state may expose a subset/normalized form.

Important rules:

- a completed pass is never replayed automatically
- a missed pass is never replayed retroactively
- a cancelled pass must not start
- a future approved PassPlan remains executable through a Server outage
- a failure must preserve useful diagnostic information

---

# 9. Pass Timing and Buffers

Scheduling buffers are part of the Server's resource reservation model.

The Worker should respect the executable PassPlan's actual execution timing rather than independently inventing reservations.

Recording has independent margins:

```text
recording_start = AOS - recording_pre_roll
recording_end   = LOS + recording_post_roll
```

The Worker executes recording/processing according to the PassPlan.

---

# 10. Tracking

The Worker is responsible for physical tracking execution, but not global scheduling.

The Worker must have enough tracking information in the PassPlan to perform the pass without depending on the Server.

The Worker should use the simplest implementation that is reliable on the target hardware.

It may perform small local calculations required to interpolate/execute antenna pointing, but it must not become a second independent scheduler or approval system.

If the Server supplies a track/pointing timeline, the Worker should execute that timeline directly.

---

# 11. Yaesu G-550

V1 hardware target: Yaesu G-550.

A working serial implementation exists under `REFERENEC/` and must be inspected first.

Required command behavior:

```python
def set_rotor(port, baud, azimuth, elevation):
    az = max(0, min(359, int(azimuth)))
    el = max(0, min(90, int(elevation)))
    _send(port, baud, f"W{az:04d} {el:03d}")
```

The Worker must enforce:

- azimuth `0..359`
- elevation `0..90`

Do not invent generic rotator support for V1.

## 11.1 Safe behavior

After abnormal shutdown or startup recovery:

1. initialize serial connection
2. validate configuration
3. establish a known operational state
4. move to configured safe/park position when required
5. only then resume safe future execution

A hardware failure must fail safely rather than silently continuing.

---

# 12. RTL-SDR

V1 supports one RTL-SDR.

The station cannot operate VHF and UHF simultaneously.

Do not implement a generic SDR abstraction in V1.

The Worker should provide only the configuration/control necessary for the supported RTL-SDR/SatDump workflow.

The Worker must refuse to start two simultaneous SDR captures.

---

# 13. SatDump

SatDump is the execution engine for signal recording/processing.

The Worker must:

- discover/validate installed SatDump availability
- execute selected standard pipelines
- accept a custom pipeline JSON supplied by the Server/user workflow
- capture stdout/stderr/status
- detect process failure
- stop processes cleanly
- finalize outputs
- associate outputs with the relevant pass/recording ID

Do not build a pipeline-builder UI or pipeline-authoring system in the Worker.

The Worker receives a pipeline definition or validated reference.

---

# 14. Recording Modes

V1 supports the global concept of:

- raw recording
- processing
- raw + processing where the selected workflow permits it

The exact supported modes should be based on the SatDump installation and pipeline behavior.

Recording window:

```text
AOS - recording_pre_roll
       →
LOS + recording_post_roll
```

The recording margins are independent from scheduling buffers.

---

# 15. Recording Files

Each recording/output must have a deterministic application-level recording ID.

Metadata should include at least:

- recording ID
- pass ID
- Worker ID
- station ID
- file/output path
- size
- checksum where practical
- creation/finalization time
- upload state
- processing state

The Worker must not delete a recording merely because an upload attempt failed.

---

# 16. Local Storage Policy

Normal retention:

- retain local copies for at least 12 hours

Storage-pressure rule:

- when storage usage exceeds 90%, delete the oldest eligible recordings first

The cleanup mechanism must avoid deleting actively written files.

Prefer deleting recordings that are successfully confirmed on the Server before deleting anything else.

However, the 90% pressure policy is allowed to remove older local data to preserve Worker operation.

Cleanup must be deterministic and logged.

---

# 17. Idempotent Upload

Recording/result uploads must be idempotent.

A retry must not create duplicate logical recordings.

Use the recording ID (and any required content identity/checksum) as the idempotency key.

The Worker should persist upload state so a process restart does not lose knowledge of an already-uploaded recording.

A V1 upload may be a complete file transfer rather than a sophisticated chunked protocol.

Do not add multipart/chunk protocol complexity until real recording sizes prove it necessary.

---

# 18. Server Connectivity

The Worker should maintain:

- gRPC control/state connection
- realtime telemetry connection where applicable
- heartbeat
- reconnect loop

Recommended heartbeat:

- approximately every 5 seconds

The Worker must tolerate transient network errors without crashing.

Backoff reconnects rather than tight-loop retrying.

---

# 19. Authentication / Trust for Server Connection

V1 should remain simple.

Use:

- configured Server address
- TLS
- simple configured shared Worker credential/secret

Do not introduce a full PKI/mTLS management system in V1.

The credential must never be hard-coded or logged.

---

# 20. Synchronization

Use latest-state reconciliation.

The Worker reports at least:

- Worker identity
- local desired-state generation/version/hash
- active execution state
- outstanding execution records
- recording upload state
- current health/telemetry

Server provides the latest authoritative desired state.

The Worker then reconciles its local state.

A simple generation/version value should allow the Worker to say, effectively:

```text
I currently have desired state generation N.
```

and the Server to respond with the newest state.

No distributed event-sourcing framework is required.

---

# 21. Offline Operation

## Server unavailable

Worker continues executing already synchronized future passes.

It may independently refresh TLEs at least every 24 hours using configured external providers/fallbacks.

It stores execution results locally.

It stores recordings locally.

It continues hardware monitoring.

It does not create new user schedules.

## Server returns

Worker:

1. reconnects
2. reports state
3. uploads pending results
4. uploads pending recordings
5. reconciles to the latest Server desired state
6. continues normal operation

## Worker unavailable

The Server controls the user-visible offline state and prevents new scheduling for that station.

---

# 22. TLE Refresh on Worker

The Worker must be capable of refreshing orbital data at least every 24 hours while disconnected.

Preferred source order is configured by the application.

At minimum:

1. preferred external provider
2. fallback provider
3. last known-good local TLE

A TLE refresh failure must not destroy a valid last known-good local orbital record.

A currently executing pass must not be destabilized by an unrelated refresh failure.

---

# 23. Telemetry

Realtime telemetry is intentionally practical rather than exhaustive.

Include at minimum where supported:

- CPU usage
- memory usage
- storage usage
- temperature
- network status
- Worker online state
- current pass ID
- current pass state
- current satellite
- current azimuth/elevation
- rotator state
- SDR state
- SatDump state
- current recording/upload state

Telemetry must be rate-limited and must not overwhelm the Server.

---

# 24. Logging and Diagnostics

Use structured logs.

Every pass/recording/hardware operation should have a correlatable identifier where practical.

Logs should answer:

- which pass was running?
- which satellite?
- which Worker/station?
- which SatDump pipeline?
- which hardware command/process failed?
- what was the recovery decision?

Never log secrets.

---

# 25. Restart and Recovery

Worker restart must:

- load durable local state
- restore future PassPlans
- detect stale/incomplete executions
- avoid replaying completed work
- mark genuinely missed passes correctly
- restore upload queue
- reinitialize hardware safely
- park/safe the rotator where appropriate

A restart must not require the Server to reconstruct all future work.

---

# 26. Worker API Surface

The exact API lives in protobuf definitions shared with the Server.

Conceptual operations include:

### Control/state

- Register/identify Worker
- Get status
- Heartbeat
- Get current state generation
- Synchronize desired state
- Push execution result
- Acknowledge configuration/state

### Realtime

- telemetry stream
- active pass status
- hardware status
- upload progress

Do not expose raw hardware-control RPCs to normal Client users. Worker hardware commands are Server-controlled and authorization belongs to the Server.

---

# 27. Testing Requirements

Unit tests:

- rotator command formatting
- az/el clamping
- execution state transitions
- scheduling-window handling
- storage cleanup ordering
- idempotency keys
- synchronization reconciliation
- TLE fallback behavior

Integration tests:

- gRPC connection to a test Server
- local state persistence
- recording upload/retry
- restart recovery

Hardware tests:

- G-550 serial command
- RTL-SDR open/capture
- SatDump test pipeline

Failure tests:

- Server disappears during idle
- Server disappears during pass
- Server disappears during upload
- Worker restarts before pass
- Worker restarts during pass
- disk >90%
- rotator failure
- SDR failure
- SatDump failure

---

# 28. Deployment

Worker deployment must support:

- Podman
- Podman Compose (`deployment/compose.yaml`), rootless

The Worker should restart automatically on process/container failure.

Hardware device access must be explicitly configured in deployment files.

Do not hide required device mappings/permissions in undocumented setup steps.

---

# 29. Implementation Rules for Claude Code

1. Read global `spec.md` and this document before modifying Worker code.
2. Inspect `REFERENEC/` before touching G-550, RTL-SDR, or SatDump integration.
3. Reuse proven reference behavior where possible.
4. Do not blindly copy reference code; adapt it to the Aagasa contracts.
5. Do not build server-side scheduling logic into the Worker.
6. Do not add generic hardware abstractions in V1 without a concrete requirement.
7. Keep local state durable.
8. Prefer deterministic state machines.
9. Make network operations retry-safe.
10. Never lose recordings merely because connectivity is unavailable.
11. Keep secrets out of source code.
12. Add tests with each behavior, not after the entire Worker is built.
13. Implement only the current phase in `plan.md` unless explicitly instructed to advance.
14. When a hardware behavior is uncertain, inspect `REFERENEC/` before inventing it.
