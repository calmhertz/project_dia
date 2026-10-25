# Aagasa — Incremental Implementation Plan

## Purpose

This document is the **execution plan** for implementing Aagasa without asking an agent to build the whole system at once.

Each phase produces a stable milestone. An implementation agent should complete and test one phase before moving to the next.

The intent is explicitly:

> **Do not build V1→VX as one giant task. Build one bounded capability at a time, verify it, then continue.**

The phase names below are intentionally incremental. Later component-specific specifications should refine the relevant phases without changing their order unnecessarily.

---

# Global Agent Rules

Before any phase:

1. Read `spec.md`.
2. Inspect the repository.
3. Inspect `REFERENEC/` before implementing related functionality.
4. Identify existing code worth preserving.
5. Do not rewrite unrelated working code.
6. Do not jump to future phases because a future feature seems convenient to implement early.
7. Every phase must leave the repository buildable/testable.
8. Prefer vertical slices with real tests over large amounts of scaffolding.
9. Keep secrets/configuration outside source code.
10. If implementation reveals a contradiction with `spec.md`, stop and document the conflict instead of silently changing the contract.

---

# V0 — Repository Reconnaissance and Architecture Baseline

## Goal

Understand the existing repository and establish the structure that all later work will use.

## Tasks

- inspect repository tree
- inspect `REFERENEC/`
- identify reusable Go code
- identify reusable Python code
- identify the Yaesu G-550 implementation
- identify any RTL-SDR/SatDump examples
- identify existing Flutter structure if present
- identify deployment artifacts if present
- identify existing protobuf definitions if present
- identify any existing database schemas
- document findings
- choose the minimal repository structure consistent with `spec.md`

## Do not implement yet

- no complete authentication system
- no complete scheduler
- no complete Worker
- no complete UI

## Exit criteria

- repository structure documented
- reference code understood
- build/development commands documented
- architecture decision notes captured
- `REFERENEC/` is clearly separated from production implementation

---

# V1 — Build and Runtime Foundation

## Goal

Make every major runtime independently buildable and deployable.

## Tasks

### Server

- initialize Go application
- initialize configuration loading
- initialize structured logging
- initialize PostgreSQL connection
- initialize Redis connection
- initialize MongoDB connection
- initialize basic health endpoint

### Python service

- initialize Python project with `uv`
- establish gRPC service skeleton
- add health method
- make Go↔Python gRPC communication work

### Worker

- initialize Python project with `uv`
- initialize configuration
- initialize logging
- initialize local state/storage directories
- basic health/telemetry process
- basic gRPC connection skeleton

### Client

- initialize Flutter Material 3 project structure
- dark-only theme
- seed `#487CE5`
- basic navigation shell
- basic connectivity/error state

### Deployment

- Podman files
- Podman Kube Play YAML
- Quadlet/systemd artifacts
- environment/config examples

## Exit criteria

- Server starts
- Python service starts
- Worker starts
- Flutter app starts
- Go can call Python over gRPC
- deployment artifacts can start the services
- health checks work

Do not proceed until all four major runtime pieces start cleanly.

---

# V2 — Database and Domain Model

## Goal

Create the durable domain model before adding complicated behavior.

## Implement

### PostgreSQL

- users
- roles
- permissions
- stations
- workers
- satellites
- TLE records/versions
- pass requests
- scheduled passes
- station configuration
- scheduling configuration
- audit records

### MongoDB

- pass execution document model
- telemetry/operational document model as appropriate

### Redis

- session/token primitives

### Filesystem

- recordings directory model
- uploaded pipeline directory model

## Requirements

Use migration/versioning tooling.

Add constraints and indexes deliberately.

Scheduling-related identifiers must be stable and auditable.

## Exit criteria

- clean database bootstrap
- migrations work from empty databases
- domain relations are documented
- integration tests can create/read domain records

---

# V3 — Authentication, RBAC, and First-Run Setup

## Goal

Make the platform securely usable by real users before implementing satellite operations.

## Implement

- Root bootstrap with `root/toor`
- forced first-login password change
- Argon2id password hashing
- session/access-token flow
- Redis-backed session support where used
- Root/Admin/Normal User roles
- Root protection from deletion/demotion
- Admin promotion by Root
- Root password recovery/change CLI
- first-run setup flow
- station configuration persistence

## Exit criteria

Test:

- first startup creates Root
- Root must change password
- old bootstrap credential stops working
- Root cannot be deleted
- Root cannot be demoted
- Admin cannot perform Root-only operation
- normal user cannot perform admin operation
- password hashes are Argon2id

Do not begin pass scheduling until RBAC is stable.

---

# V4 — Station and Hardware Bring-Up

## Goal

Control the real ground-station hardware from the Worker independently of the rest of Aagasa.

## Implement

### Yaesu G-550

- serial-port configuration
- baud configuration
- command sending
- azimuth/elevation range enforcement
- park/safe behavior
- position/status handling as supported by reference implementation

Reuse and verify the working reference code.

### RTL-SDR

- device discovery/opening
- configured sampling
- basic test capture
- safe shutdown

### SatDump

- detect available pipelines from the installed SatDump environment
- invoke a known-safe test pipeline
- capture stdout/stderr/status
- return deterministic success/failure state

### Worker hardware safety

- safe startup
- safe shutdown
- abnormal restart handling

## Exit criteria

The Worker can physically:

1. connect to the G-550
2. move to a commanded az/el
3. interact with RTL-SDR
4. launch/stop a SatDump workflow
5. record a hardware execution result

No scheduling UI yet.

---

# V5 — Satellite Catalogue and TLE Pipeline

## Goal

Establish trustworthy satellite/orbital data before computing passes.

## Implement

- NORAD ID as canonical identity
- satellite records
- TLE version records
- TLE provider adapter
- CelesTrak provider
- SatNOGS/fallback provider if available/appropriate
- last-known-good TLE behavior
- 24-hour refresh policy
- source/epoch/fetched-at metadata
- basic satellite metadata enrichment
- Root-controlled catalogue administration

Validate actual external provider APIs/feeds during implementation.

Do not hard-code unverified third-party endpoint assumptions.

## Exit criteria

- Root can add/manage satellites
- TLEs are current or fallback safely
- old known-good TLE remains available when providers fail
- satellite metadata may be missing without breaking orbital functionality

---

# V6 — Pass Prediction Service

## Goal

Build a reliable server-side prediction engine.

## Implement

Python service using:

- Skyfield
- SGP4

Input:

- satellite/TLE
- station location
- configured minimum elevation
- prediction time range

Output at minimum:

- AOS
- LOS
- TCA/max elevation
- track information needed by a pass plan

Go calls Python through gRPC.

## Exit criteria

- deterministic prediction API
- unit tests for known sample TLE/station combinations
- errors are explicit
- Go can consume predictions
- predictions are not duplicated in another competing scheduler implementation

---

# V7 — Scheduling Core

## Goal

Implement authoritative scheduling before any sophisticated execution orchestration.

## Implement

- pass request creation
- pass state machine
- approval workflow
- minimum scheduling lead time
- pre/post scheduling buffers
- station resource reservation
- overlap detection
- PostgreSQL transactional conflict enforcement
- Root override
- audit records
- public/private visibility

## Critical rules to test

- any overlap on same station is a conflict
- concurrent requests cannot race through the conflict check
- lead-time restriction applies to Root too
- changing lead time cannot bypass the waiting rule
- Root can override an accepted conflicting pass
- overridden pass remains auditable
- station configuration mismatch produces warning, not automatic conflict

## Exit criteria

The scheduling engine can be tested without Worker hardware.

Do not connect it to real hardware yet.

---

# V8 — Executable PassPlan Generation

## Goal

Turn an approved schedule into a Worker-ready execution instruction.

## Implement

PassPlan contains everything necessary for execution, including as applicable:

- pass ID
- station
- satellite/NORAD ID
- selected TLE/version
- execution timing
- tracking information
- band
- radio settings
- recording mode
- recording margins
- SatDump pipeline
- custom pipeline JSON reference/content as designed

The Worker must not need to ask the Server for missing information during normal execution.

## Exit criteria

- approved pass generates deterministic executable PassPlan
- plan can be persisted
- plan can be serialized/deserialized through protobuf
- changing a future satellite's TLE does not corrupt historical plan identity

---

# V9 — Worker Scheduling and Local Durable Execution

## Goal

Teach the Worker to store and execute PassPlans independently of the Server.

## Implement

- local durable PassPlan store
- execution queue
- time-window execution
- pass state transitions
- safe startup recovery
- missed-pass handling
- future-pass execution
- local execution history

## Critical failure test

Turn off the Server after the Worker has synchronized several approved passes.

The Worker must continue executing those passes.

## Exit criteria

- Worker can execute synchronized schedule with no Server
- restart does not erase future schedule
- completed passes are not replayed
- missed passes are not replayed

---

# V10 — Real Pass Execution

## Goal

Connect PassPlan execution to the physical station.

## Implement

Execution sequence should include as needed:

1. validate pass is still executable
2. initialize/verify hardware
3. apply band/radio configuration
4. track antenna
5. start recording/process pipeline around configured recording margins
6. collect operational state
7. stop capture after post-LOS margin
8. finalize recording/output
9. finalize pass state

Use the simplest reliable Worker execution loop.

## Exit criteria

A real test pass can go from:

```text
APPROVED
→ EXECUTING
→ COMPLETED
```

with real G-550 + RTL-SDR + SatDump involvement.

---

# V11 — Recording Management and Idempotent Upload

## Goal

Move completed recordings safely from Worker to Server.

## Implement

- deterministic recording IDs
- recording metadata
- local recording retention
- 12-hour minimum normal retention policy
- >90% disk pressure cleanup
- upload API
- idempotent upload semantics
- upload acknowledgement
- retry behavior
- server filesystem storage
- recording status tracking

## Failure tests

- upload request duplicated
- acknowledgement lost
- Server unavailable during upload
- Worker restarted during pending upload
- disk exceeds 90%

## Exit criteria

No logical duplicate recording is created by retries.

---

# V12 — Server/Worker State Synchronization

## Goal

Make reconnection deterministic and boring.

## Implement

- Worker identity
- Worker online/offline state
- heartbeat
- current-state generation/version/hash
- latest desired Server state
- Worker local current state
- reconciliation endpoint/protocol
- reconnect behavior
- outstanding execution record sync
- recording sync coordination

## Required scenario

1. Server online.
2. Worker syncs.
3. Server disappears.
4. Worker continues.
5. Worker executes passes.
6. Worker stores new state/results locally.
7. Server returns.
8. Worker reconnects.
9. Server and Worker reconcile to latest state.
10. Recordings/results are uploaded safely.

## Exit criteria

The complete offline/online cycle is testable without manual DB surgery.

---

# V13 — Client: Core User Experience

## Goal

Give normal users a useful, stable platform UI.

Implement in this order:

1. Login
2. Dashboard
3. Satellite catalogue
4. Satellite detail
5. Pass prediction/view
6. Pass scheduling request
7. My passes
8. Pass detail
9. recording/result access
10. public pass history

Use:

- Material 3
- dark only
- seed `#487CE5`
- responsive layouts

Do not expose internal infrastructure details to normal users.

## Exit criteria

A normal user can:

- sign in
- choose a satellite
- inspect a pass
- request a pass
- understand approval status
- see conflicts
- see pass result
- download permitted recordings

---

# V14 — Client: Admin Operations

## Goal

Give Admin and Root operational control without mixing it with the normal-user UX.

Implement:

- approval queue
- pass review
- station status
- Worker status
- scheduling configuration view
- satellite management
- TLE refresh status
- recording management
- operational history

Admin should not receive Root-only controls.

## Exit criteria

Admin can perform authorized operational work without access to Root-only actions.

---

# V15 — Client: Root Setup and Administration

## Goal

Expose the full Root control surface.

Implement:

- first-run setup UI
- station RF mode
- VHF/UHF antenna settings
- scheduling buffers
- minimum lead time
- recording margins
- rotator settings
- RTL-SDR settings
- system settings
- user management
- Admin promotion
- Root-sensitive operations
- audit history
- Root override actions

## Exit criteria

Root can administer the complete V1 station without database edits.

The emergency root-password CLI remains available even if the UI is unavailable.

---

# V16 — Observability, Recovery, and Operational Hardening

## Goal

Make the system reliable enough for unattended use.

Implement/refine:

- structured logs
- clear health states
- Worker heartbeat behavior
- reconnect/backoff
- restart-safe state
- safe hardware shutdown/startup
- error classification
- audit completeness
- storage-pressure behavior
- deployment health checks

Do not add an elaborate monitoring platform unless later required.

## Exit criteria

Simulated failure tests pass for:

- Server crash
- Worker crash
- Worker reboot
- DB reconnect
- network outage
- TLE provider outage
- SatDump failure
- SDR failure
- rotator communication failure
- recording upload failure

---

# V17 — Integration Test Environment

## Goal

Create a repeatable end-to-end development/test environment.

Provide test fixtures for:

- users/roles
- stations
- satellites/TLEs
- sample pass plans
- simulated Worker
- simulated SatDump execution where physical hardware is unavailable
- recording upload
- state synchronization

## Exit criteria

The complete application can be tested without requiring physical hardware for every CI run, while preserving an explicit hardware-integration test path.

---

# V18 — Production Deployment Hardening

## Goal

Make deployment reproducible on the target environment.

Implement/document:

- persistent volume layout
- DB backup strategy
- recording storage layout
- log rotation
- Quadlet ordering/dependencies
- service restart policy
- firewall/network assumptions
- secret/config injection
- TLS deployment
- upgrade procedure
- rollback procedure

## Exit criteria

A clean machine can be provisioned using the documented deployment procedure and reach a healthy state.

---

# V19 — Acceptance Pass / V1 Freeze

## Goal

Prove the project satisfies the global V1 contract before feature expansion.

Run the critical scenarios end-to-end:

### Scenario A — Server offline

- Worker has last approved schedule
- Server is shut down
- Worker executes existing pass
- Worker stores recording
- Worker retains local data
- Server returns
- Worker synchronizes
- recording uploads
- both converge

### Scenario B — Normal operation

- multiple users request passes
- conflicting station windows are rejected
- approvals work
- approved pass reaches Worker
- Worker executes
- recording stored on Server

### Scenario C — Worker offline

- Worker disconnected
- Server shows Worker offline
- new scheduling disabled
- Worker later reconnects
- state reconciles

### Scenario D — Root override

- User A has approved pass
- Root creates conflicting pass
- User A pass becomes cancelled-by-root-override
- audit record exists
- Root pass becomes active

### Scenario E — Public/private

- private pass hidden from unrelated normal user
- public pass visible
- public recording downloadable
- owner identity not unnecessarily exposed

### Scenario F — TLE provider failure

- primary source unavailable
- fallback attempted
- last known-good TLE preserved

### Scenario G — Storage pressure

- Worker storage >90%
- oldest eligible local recordings cleaned safely

## Exit criteria

Only after these scenarios pass should Aagasa be considered **V1 complete**.

---

# V20+ — Future Expansion

Do not implement these during V1 unless explicitly promoted into the active roadmap:

- multiple physical ground stations in active production use
- multiple simultaneous SDRs
- simultaneous VHF/UHF chains
- advanced scheduling optimization
- autonomous priority arbitration beyond Root override
- generic SDR abstraction
- generic rotator ecosystem
- richer signal-processing orchestration
- automated satellite discovery beyond approved catalogue workflows
- advanced analytics
- advanced observability platforms
- large-scale multi-tenant deployment

Future work should be added as a new phase rather than silently inserted into an earlier phase.

---

# Recommended Agent Operating Mode

When asking an implementation agent to work on Aagasa, use the following pattern:

```text
Read spec.md and plan.md.

Implement ONLY Vx.

Before coding:
- inspect current repository state
- inspect relevant REFERENEC/ code
- identify what is already complete

During coding:
- do not implement later phases
- preserve existing working behavior
- add tests for every new domain rule

After coding:
- run relevant unit/integration tests
- run formatting/linting/build checks
- report exactly what was changed
- report anything intentionally deferred
- do not claim later phases are complete
```

This should be used repeatedly rather than giving the agent the entire project as one undifferentiated coding request.

---

# Phase Dependency Graph

```text
V0  Recon
 |
 v
V1  Runtime Foundation
 |
 v
V2  Data Model
 |
 +----> V3 Auth/RBAC/Setup
 |
 +----> V5 Satellite/TLE
 |
 +----> V4 Hardware
 |
 v
V6  Prediction
 |
 v
V7  Scheduling
 |
 v
V8  PassPlan
 |
 v
V9  Worker Local Execution
 |
 v
V10 Real Pass Execution
 |
 +----> V11 Recording/Upload
 |
 +----> V12 Sync/Recovery
 |
 v
V13 Client Core UX
 |
 v
V14 Admin UX
 |
 v
V15 Root UX
 |
 v
V16 Hardening
 |
 v
V17 Integration Test Environment
 |
 v
V18 Production Deployment Hardening
 |
 v
V19 V1 Acceptance / Freeze
 |
 v
V20+ Future
```

Some phases can be developed in parallel by separate agents once their dependencies are stable, but do not merge parallel work without integration tests.

---

# Final Principle

The project should evolve as:

```text
foundation
→ durable data
→ identity/security
→ hardware
→ orbital data
→ prediction
→ scheduling
→ executable plans
→ Worker execution
→ recording
→ synchronization
→ user UI
→ admin/root UI
→ hardening
→ acceptance
```

Do not reverse this order merely because a later feature is more visually interesting.

The safest path is to make the domain and execution contracts correct first, then expose them through the UI.
