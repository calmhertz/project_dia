# Aagasa — Project-Wide Global Specification

## 0. Purpose of This Document

Aagasa is a satellite ground-station automation platform. This document is the **global contract** for the entire project and defines the architecture, boundaries, domain rules, reliability requirements, security posture, technology choices, and non-negotiable behavior shared by the Server, Worker, and Client.

This is not the implementation specification for any one component. Later, create dedicated component specifications for:

- `server/spec.md`
- `worker/spec.md`
- `client/spec.md`

Those component specifications must refine this document, not contradict it.

An implementation agent must read this document before making architectural changes. When a component-specific decision is not yet specified, prefer the simplest implementation that preserves the global contracts and future extensibility described here.

---

# 1. Project Identity

**Project name:** Aagasa

**Primary goal:** Automate operations of a satellite ground station and allow the station to be monitored and controlled remotely.

Aagasa must allow an operator to:

- maintain a satellite catalogue
- maintain current orbital data/TLEs
- predict satellite passes
- request and approve passes
- prevent conflicting station reservations
- send executable pass plans to a physical ground-station Worker
- track satellites
- control the rotator
- receive signals through RTL-SDR
- record or process signals using SatDump
- transfer resulting data to the Server
- retain enough local state for continued Worker operation during Server outages
- monitor the Worker remotely
- expose a responsive Material 3 UI through Flutter Web, Android, and iOS

The system is intentionally split into a **Server control/orchestration plane** and a **Worker execution/edge plane**.

---

# 2. Core Architectural Principle

Aagasa consists of three logical layers:

```text
Flutter Client
    |
    | REST + WebSocket
    v
Server
    |
    | gRPC + WebSocket/telemetry channel as defined by implementation
    v
Worker
    |
    +--> Yaesu G-550 rotator
    +--> RTL-SDR
    +--> SatDump
```

## 2.1 Server is the authoritative control plane

The Server is responsible for the heavy lifting and global decision making:

- authentication and authorization
- users and roles
- ground-station configuration
- satellite catalogue
- orbital-data management
- pass prediction
- scheduling
- conflict detection
- approval workflow
- worker availability
- generating executable pass plans
- recording metadata and server-side storage
- audit history
- REST API
- Client-facing WebSockets
- Worker-facing gRPC

The Server may delegate Python-specific computation to an internal Python service.

## 2.2 Worker is the edge execution plane

The Worker is physically installed at the ground station and is intentionally simpler than the Server.

The Worker is responsible for:

- receiving the latest executable pass plans
- keeping durable local execution state
- waiting for pass times
- calculating/using the already-prepared tracking trajectory supplied by the Server or using minimal local tracking calculations required for execution
- controlling the Yaesu G-550 rotator
- controlling the RTL-SDR
- executing SatDump pipelines
- recording/processing signals
- maintaining local recordings
- sending operational state and telemetry
- synchronizing with the Server after connectivity is restored

The Worker must be able to continue operating without a live Server connection using the latest successfully synchronized local state.

## 2.3 Client is intentionally abstracted from internals

Normal users should see ground-station functionality rather than infrastructure implementation details.

Normal user-facing concepts include:

- satellites
- passes
- scheduling
- approval status
- execution status
- recordings
- public pass history

They should not need to understand:

- gRPC
- Podman
- Redis
- MongoDB
- internal Worker services
- internal Server services

Admins and Root may see more operational information.

---

# 3. V1 Deployment Topology

The architecture must support multiple workers/stations in the Server data model, but the first deployment is intentionally:

```text
1 Server
1 Worker
1 Ground Station
```

The Server data model should therefore include stable identifiers such as:

- `station_id`
- `worker_id`

but V1 UX and deployment can operate against one station/worker.

Do not build a complex multi-station orchestration UI in V1 merely because the data model is capable of supporting it.

The Worker itself must remain focused on one physical station.

---

# 4. Technology Stack

## 4.1 Worker

- Python
- `uv` for Python dependency/environment management
- Podman
- Podman Kube Play YAML
- systemd Quadlet
- SatDump
- PySerial
- RTL-SDR

Do not introduce a generic SDR abstraction in V1.

Do not introduce a generic rotator framework in V1.

V1 targets:

- RTL-SDR
- Yaesu G-550

## 4.2 Server

Primary backend:

- Go

Python is used for tasks that materially benefit from Python, particularly:

- Skyfield
- SGP4-related calculations
- satellite pass prediction
- other satellite-domain calculations where Python libraries provide a clear advantage

Go and the Python service communicate through **gRPC**.

Use `uv` for Python dependency/environment management in Server-side Python as well.

Server deployment uses the same general Podman-based deployment strategy as the Worker.

## 4.3 Databases and storage

### PostgreSQL

Authoritative structured relational data, including:

- users
- credentials/authentication metadata
- roles/permissions
- stations
- workers
- satellites
- NORAD identity
- TLE metadata/version information
- pass requests
- scheduled passes
- approval decisions
- station configuration
- scheduling configuration
- audit information

PostgreSQL is the authoritative source for scheduling and resource-conflict prevention.

### MongoDB

Flexible operational/pass-related documents where schema flexibility is useful, such as:

- pass execution metadata
- telemetry/operational documents
- flexible SatDump metadata
- other pass-derived documents that do not benefit from rigid relational modeling

Do not move core transactional scheduling state into MongoDB.

### Redis

Use only where ephemeral/fast state is useful, such as:

- session/token support
- temporary locks
- cache
- transient coordination/pub-sub if needed

Do not use Redis as the system-of-record for durable business state.

### Filesystem

Use filesystem storage for large binary content such as:

- recordings
- images
- uploaded SatDump pipeline JSON
- other large files

A recording's metadata belongs in the application data store; recording content belongs in file storage.

---

# 5. Communication Architecture

## 5.1 Client ↔ Server

Use:

- REST/HTTP for request-response operations
- WebSocket for real-time updates and telemetry

Examples of REST responsibilities:

- login/session operations
- catalogue retrieval
- scheduling requests
- pass approval/rejection
- configuration
- recordings metadata
- downloads

Examples of WebSocket responsibilities:

- Worker online/offline status
- current pass state
- pass execution progress
- telemetry
- upload progress
- notifications relevant to the logged-in user

## 5.2 Server ↔ Python computation service

Use **gRPC**.

The Python service is an internal service, not a public API.

## 5.3 Server ↔ Worker

Use gRPC for durable/control operations and state synchronization.

Realtime telemetry may be streamed through a Worker↔Server realtime channel; the implementation should use the simplest reliable mechanism that satisfies the global requirement for realtime communication. A WebSocket channel is acceptable and preferred for continuously changing telemetry/metrics.

The protocol must be versioned and backwards-compatible enough to permit controlled rolling upgrades.

---

# 6. Server/Worker Failure Independence

This is a **core requirement**, not an optional enhancement.

## 6.1 Server offline, Worker online

The Worker must continue to execute its latest successfully synchronized approved pass plans.

It must not stop merely because the Server is unreachable.

While disconnected, the Worker continues to:

- execute already-received pass plans
- track satellites
- operate the rotator
- use the RTL-SDR
- run SatDump
- record/process data
- maintain local recordings
- monitor local hardware
- maintain local execution state
- periodically attempt reconnection to the Server
- refresh TLEs independently according to the Worker TLE policy

The Worker does **not** create new user schedules while offline. It simply executes the last known valid local desired state.

## 6.2 Server reconnects

When connectivity is restored:

1. Worker reconnects.
2. Worker reports current local state and outstanding durable execution records.
3. Server compares/reconciles state with the authoritative latest desired state.
4. Worker receives the latest desired state.
5. Pending recordings are uploaded.
6. Uploads are retried safely if necessary.
7. Both sides converge on the latest valid state.

V1 should use **latest-state reconciliation**, not a complicated event-sourcing platform.

The design should support a simple version/hash/generation identifier so each side can efficiently determine whether its state is current.

## 6.3 Worker offline, Server online

The Server must clearly report that the Worker is offline.

New pass scheduling for that station must be disabled while the Worker is offline.

Existing scheduled/approved passes remain historical application state; if a required execution cannot occur because the Worker is unavailable, the pass is eventually represented as `MISSED` rather than being retroactively executed.

## 6.4 Missed passes

A pass that reaches its execution window while the Worker is unavailable is marked `MISSED`.

Never replay a missed pass retroactively.

---

# 7. Online/Heartbeat Policy

Use a simple heartbeat model.

Recommended V1 behavior:

- Worker maintains a persistent realtime connection where possible.
- Worker sends heartbeat/telemetry approximately every 5 seconds.
- Server considers the Worker offline after approximately 15 seconds without a valid heartbeat.
- Worker automatically retries connection.

The exact implementation may use slightly different timing if required by platform/network constraints, but the behavioral model must remain simple and deterministic.

Track at minimum:

- `last_seen`
- connection state
- last successful sync time
- Worker software version
- station association

---

# 8. Ground Station Model

The ground station is treated as a single active RF chain in V1.

Supported operational RF modes:

- VHF
- UHF

Only one is actively configured/used at a time because only one RTL-SDR is present.

Root can switch the station between VHF and UHF operation and change the corresponding station settings/antenna configuration.

A pass declares the band it requires.

The scheduler must reject **overlapping station usage** because the station is a single resource.

However, the scheduler does not reject a pass merely because the station is currently configured for the other band. The pass may still be scheduled, but the UI must clearly warn that the current station RF configuration may not match the pass and reception may be poor.

This is a **warning**, not a scheduling conflict.

The station model should retain enough configuration to represent at least:

- station name
- latitude
- longitude
- altitude
- timezone if required by presentation
- active RF mode
- VHF antenna/configuration
- UHF antenna/configuration
- rotator serial port
- rotator baud rate
- RTL-SDR operational configuration
- scheduling buffers
- recording margins
- minimum scheduling lead time

Root configures these during initial setup.

---

# 9. Rotator Requirements

V1 specifically targets the **Yaesu G-550**.

A working serial implementation exists in `REFERENEC/` and must be inspected before implementation.

The required command behavior includes:

```python
def set_rotor(port, baud, azimuth, elevation):
    az = max(0, min(359, int(azimuth)))
    el = max(0, min(90, int(elevation)))
    _send(port, baud, f"W{az:04d} {el:03d}")
```

The Worker must enforce the valid ranges:

- azimuth: `0..359`
- elevation: `0..90`

After abnormal Worker shutdown, the rotator should be driven toward the configured safe/park behavior during recovery.

Do not invent unnecessary generic rotator support in V1.

---

# 10. RTL-SDR Requirements

V1 supports one RTL-SDR.

VHF and UHF cannot be captured simultaneously.

Do not create a generalized SDR driver abstraction in V1.

The implementation should use the reference code and configuration present in `REFERENEC/` where useful.

The Server should represent the radio parameters required for a pass, but avoid exposing unimportant low-level configuration to normal users.

Relevant V1 radio configuration may include:

- frequency
- sample rate
- gain
- PPM correction
- bandwidth where required
- band
- modulation/demodulation parameters where these are part of the selected SatDump workflow

Only include parameters that materially affect execution.

---

# 11. Satellite Identity and Orbital Data

## 11.1 Canonical satellite identity

The **NORAD/catalog identifier** is the stable satellite identity.

A TLE is orbital data, not the permanent identity of the satellite.

Do not use the raw TLE text as the primary identity of a satellite because TLEs change over time.

## 11.2 Satellite metadata

Satellite metadata is useful but is not a prerequisite for core execution.

Possible metadata includes:

- display name
- description
- links
- downlink frequency
- amateur-radio information
- other source metadata

The system should enrich satellite records from reliable external sources such as SatNOGS and/or CelesTrak where useful.

Missing metadata must not prevent tracking if the system has:

- valid satellite identity
- valid orbital data
- valid pass configuration
- valid execution/pipeline configuration

## 11.3 TLE sources

The system must support reliable TLE retrieval with fallback behavior.

The implementation should provide a provider/adapter design so sources can be replaced or extended.

Preferred behavior:

1. Retrieve current TLE from the configured primary source, normally CelesTrak.
2. If unavailable, try a configured fallback source such as SatNOGS where an appropriate current TLE is available.
3. If all remote providers fail, keep and use the last known-good TLE.
4. Store provider/source, epoch, fetch timestamp, and the TLE itself.

The system must verify current provider/API/feed capabilities during implementation rather than hard-coding assumptions about third-party endpoints.

The Server refreshes TLEs at least every 24 hours.

The Worker must be capable of refreshing TLE data independently at least every 24 hours when operating without the Server.

TLE refresh must not unnecessarily break an already-executing pass.

---

# 12. Pass Prediction

Pass prediction is a Server responsibility.

Use Python where practical, especially:

- Skyfield
- SGP4

The Server's prediction service should derive at least:

- AOS
- LOS
- maximum elevation / TCA
- track information needed by the Worker

The Root-configured minimum elevation determines the usable AOS/LOS threshold.

The exact prediction implementation belongs to the Server-specific specification, but the global requirement is:

> Scheduling decisions are computed centrally by the Server, not independently invented by the Worker.

---

# 13. Pass Scheduling

## 13.1 Meaning of scheduling a pass

A scheduled pass is an instruction for the Worker to execute a satellite operation during a specific time window.

The execution window is based on the satellite pass and configured station scheduling buffers.

A scheduled pass ultimately becomes a **fully executable PassPlan**.

## 13.2 PassPlan

The Server must send the Worker enough information to execute the pass without consulting the Server during normal execution.

A PassPlan should contain, as applicable:

- pass ID
- station ID
- satellite/NORAD ID
- TLE/version used for planning
- scheduled start/end
- tracking information
- band
- frequency/radio settings
- relevant station execution parameters
- recording mode
- recording pre/post margins
- selected SatDump pipeline
- custom pipeline JSON if applicable
- priority/state
- identifiers required for later reconciliation

The exact schema belongs to the protocol/component specifications.

## 13.3 No overlapping station passes

V1 has one station and one SDR/rotator resource.

Therefore **any overlapping reservation on the same station conflicts**, regardless of satellite identity or band.

Example:

```text
Pass A: 10:00–10:15
Pass B: 10:10–10:20
```

Pass B cannot be accepted for the same station.

This must be enforced transactionally in PostgreSQL so concurrent requests cannot race into an invalid state.

## 13.4 Scheduling buffers

Root configures:

- pre-pass scheduling buffer
- post-pass scheduling buffer

For conflict detection, a pass reserves:

```text
reservation_start = AOS - pre_pass_buffer
reservation_end   = LOS + post_pass_buffer
```

These are **resource reservation buffers**, not recording margins.

## 13.5 Recording margins

Recording has separate configurable margins:

```text
recording_start = AOS - recording_pre_roll
recording_end   = LOS + recording_post_roll
```

The defaults may be a few seconds, but Root configures the values.

## 13.6 Minimum scheduling lead time

Root configures `minimum_schedule_lead_time`.

No user, including Root, can create a pass whose scheduling request violates the current cutoff.

A request must satisfy the equivalent of:

```text
request_time + minimum_schedule_lead_time <= pass_start
```

Root may change the configuration, but the newly chosen value must not become an immediate bypass around the rule. After changing the value, the new scheduling policy becomes usable only after the configured new lead-time period has elapsed from the configuration change.

Existing approved passes are not retroactively invalidated just because the lead-time configuration changes.

## 13.7 Root scheduling override

Root is allowed to override an already scheduled conflicting pass.

When Root does so:

- the old pass is not physically deleted
- the old pass is marked with an explicit root-override cancellation state
- audit information is retained
- Root's pass becomes the authoritative scheduled operation

The UI must make the consequence visible to the affected user.

## 13.8 Configuration changes

Changing station configuration, buffers, or related scheduling configuration does not silently invalidate existing approved passes.

Existing approved operations continue according to their established plan/state.

Relevant users should receive an informational event when a station configuration change may affect their pass.

---

# 14. Pass Approval and Lifecycle

Suggested lifecycle:

```text
PENDING_APPROVAL
      |
      +--> APPROVED ------> EXECUTING ------> COMPLETED
      |                         |
      |                         +------------> FAILED
      |
      +--> REJECTED

APPROVED ----------------------> CANCELLED

APPROVED + Worker unavailable at execution time -> MISSED
```

A `CANCELLED` state should support reasons, including root override.

The exact implementation may use additional internal states, but the externally meaningful lifecycle must retain these semantics.

Normal users can request passes.

Passes must be approved by Admin or Root before becoming executable.

Root has final authority and may override an Admin decision.

---

# 15. Scheduling Conflict Experience

When a second user attempts an overlapping schedule:

1. The Server rejects the conflicting schedule request.
2. The user is explicitly informed that the period is occupied.
3. The user may see limited scheduling information necessary to understand the conflict.
4. The identity or private information of the existing pass owner must not be exposed.

For subsequent users, the system may show the later pass's execution outcome/status/history without revealing the original user's identity.

A normal user must never gain access to another user's private pass solely because a conflict occurred.

---

# 16. Pass Privacy and Public History

Each pass has a visibility choice:

- `PRIVATE`
- `PUBLIC`

## Private

Visible to:

- pass owner
- Admin
- Root
- other users only to the limited extent necessary to report a scheduling conflict or other non-private operational result

Private recordings are not available to unrelated normal users.

## Public

Visible to platform users through an appropriate past/public pass history experience.

Public passes may expose:

- pass information intended for public display
- execution status
- processing/output status
- resulting recording
- recording download

The owner's personal/private account information must not be unnecessarily exposed.

Admins and Root may access all passes regardless of privacy setting.

---

# 17. Users and RBAC

There are three primary roles.

## 17.1 Root User

First-run credentials:

```text
username = root
password = toor
```

On first successful login:

- immediately require password change
- invalidate the bootstrap credential once the password is changed
- complete first-run station/system setup

Root:

- cannot be deleted
- cannot be demoted
- has highest authority
- can promote users to Admin
- can configure the station
- can modify critical system settings
- can override Admin decisions
- can override scheduled passes as defined above

A server-side CLI or equivalent recovery mechanism must exist for changing the Root password in a recovery/compromise scenario.

Use **Argon2id** for password hashing throughout the system.

Do not store plaintext passwords.

## 17.2 Admin User

Admin is below Root in authority.

Admins can perform privileged operational tasks defined by the application but cannot override Root's authority or perform Root-only critical configuration actions.

Admins can approve normal-user pass requests within their authority.

## 17.3 Normal User

Normal users can:

- authenticate
- view allowed satellite information
- request passes
- track their pass lifecycle
- view their permitted recordings/results
- view public history

Normal users cannot perform administrative operations.

---

# 18. Authentication and Session Management

Client authentication should remain simple in V1.

Use:

- username/password
- Argon2id password hashing
- access/session token model backed by Redis where useful

Do not over-engineer authentication infrastructure in V1.

Worker↔Server communication does not need a full PKI/mTLS framework for V1.

Use:

- TLS for transport where deployed over a network
- a simple configured shared Worker credential/secret

The Worker and Server addresses are configured through environment/configuration.

No dynamic service discovery is required in V1.

---

# 19. Recording and Processing

The Worker supports the operational requirement that a pass may:

- record raw signal
- process using SatDump
- or perform the configured combination supported by the chosen workflow

The chosen behavior must be represented in the executable PassPlan.

## 19.1 SatDump pipelines

The standard SatDump installation provides a set of pipelines that Aagasa should expose in the scheduling UI.

A pass can select:

1. a standard available pipeline
2. a custom pipeline JSON supplied by the user/authorized operator

Do not build a GUI pipeline designer in V1.

For custom pipelines:

- upload the JSON
- validate basic structure/syntax
- securely store it
- associate it with the pass or reusable pipeline record as designed by the component specification

Do not invent satellite-name-based pipeline generation.

Satellite metadata may help present recommendations, but the supplied pipeline JSON remains authoritative for custom pipelines.

## 19.2 Worker local retention

Worker keeps local recording copies for at least 12 hours under normal conditions.

If Worker storage exceeds 90%, it may delete the oldest eligible local recordings first, even if they have not reached 12 hours, subject to safe-preservation rules.

After a successful server-side upload/acknowledgement, recordings become eligible for ordinary retention cleanup.

The server keeps the authoritative long-term copy.

## 19.3 Upload semantics

Uploads must be **idempotent**.

If the Worker retries an upload because acknowledgement was lost, the Server must not create duplicate logical recordings.

V1 does not require a sophisticated distributed resumable-chunk upload protocol, but the API must support safe retries and deterministic recording identity.

---

# 20. Worker Recovery Behavior

If the Worker process or machine restarts:

- restore latest durable local state
- initialize hardware safely
- move the rotator to the configured safe/park behavior when required
- re-establish Server connectivity
- reconcile latest state
- do not replay already-completed passes
- do not retroactively execute missed passes
- continue with future valid passes if possible

If a pass is actively executing when the Worker crashes, the Worker should not blindly resume midway. V1 should favor a safe recovery path and then continue with the next valid pass/execution state.

---

# 21. Security and Safety Principles

Non-negotiable:

- passwords hashed with Argon2id
- no plaintext password persistence
- no hardcoded operational secrets in source code
- sensitive credentials configured via environment/secret configuration
- validate uploaded pipeline JSON
- restrict filesystem paths for uploaded/recorded content
- avoid command injection through SatDump parameters or shell execution
- authenticate Worker connections with configured credentials
- use TLS in deployed network communication
- preserve audit records for privileged actions
- preserve historical pass records rather than destructive deletion when business meaning is cancellation/override
- validate rotator bounds before serial transmission

The Worker must default toward safe hardware behavior after abnormal failure.

---

# 22. Auditability

The system must retain audit information for security- and scheduling-relevant actions, including as appropriate:

- login/security events
- password changes/recovery operations
- role changes
- approvals/rejections
- Root overrides
- scheduling configuration changes
- station configuration changes
- worker state changes
- pass cancellation
- recording deletion decisions where material

At minimum an audit entry should identify:

- actor
- action
- target/entity
- timestamp
- previous state where useful
- new state where useful
- optional reason/details

---

# 23. First-Run Setup

On first initialization:

1. create initial Root account using `root` / `toor`
2. force immediate password change
3. enter setup flow
4. configure the initial ground station
5. configure location
6. configure active RF mode and VHF/UHF antenna settings
7. configure rotator serial connection
8. configure RTL-SDR settings
9. configure pass prediction/scheduling values including minimum lead time and buffers
10. configure recording margins/retention values
11. configure TLE/metadata source settings where necessary
12. initialize required storage/database state
13. mark the system initialized

Root can later change these settings.

---

# 24. Deployment Principles

Podman is the deployment foundation for both Server and Worker.

Use:

- Podman containers
- Podman Kube Play YAML
- systemd Quadlet

The deployment should be reproducible and should not require a developer workstation to manually start processes.

Runtime configuration should be externalized through environment/config files.

Do not bake deployment secrets into images.

---

# 25. Reference Codebase Rules

A directory named `REFERENEC/` contains reference implementations/code.

Implementation agents must:

1. inspect the directory before implementing corresponding functionality
2. understand what each relevant reference does
3. reuse working logic where appropriate
4. verify assumptions against actual code
5. not assume reference code is production-perfect
6. not blindly copy architecture simply because it exists there
7. improve/modernize the design where this specification requires it

Reference code is **input**, not the final architecture.

In particular, the Yaesu G-550 serial implementation in the reference directory is considered valuable because it contains a working hardware command sequence.

---

# 26. UI/UX Global Rules

Flutter targets:

- Web
- Android
- iOS

Use:

- Material 3
- dark mode only
- responsive layouts
- simple, robust information architecture
- clear status communication

Material 3 color seed for V1:

```text
#487CE5
```

Use the Flutter theme seed mechanism so it can be changed later easily.

Normal user UX should stay abstract and task-oriented.

Admin UX may expose operational status.

Root UX may expose full station/system administration.

Do not create an unnecessarily complicated dashboard just to display infrastructure internals.

---

# 27. Global Non-Goals for V1

Do not build these unless explicitly required by a later specification/change:

- multi-SDR simultaneous operation
- simultaneous VHF + UHF reception
- generic SDR driver framework
- generic rotator driver marketplace/framework
- GUI SatDump pipeline builder
- full event-sourcing architecture
- complex distributed workflow engine
- service discovery infrastructure
- multi-region deployment
- Kubernetes cluster orchestration
- ML-based signal detection
- automatic satellite identification from arbitrary signals
- automatic scheduling optimization beyond explicit conflict prevention and approved-pass execution

---

# 28. Implementation Discipline for Coding Agents

An implementation agent must:

- read `spec.md` first
- read the relevant component specification before changing a component
- inspect `REFERENEC/` before replacing known working hardware logic
- preserve the boundary between Server orchestration and Worker execution
- keep the Worker lightweight
- keep scheduling authoritative on the Server
- make operations durable before adding UI polish
- avoid speculative abstractions that are not needed by V1
- add tests around every safety-critical domain rule
- prefer small, reviewable changes
- avoid rewriting unrelated modules
- update documentation when behavior changes

Do not silently reinterpret a global invariant for local convenience.

If a new requirement conflicts with this document, record the conflict explicitly and resolve it before implementation rather than quietly changing behavior.

---

# 29. Required Global Test Properties

The final system must be able to demonstrate at least these properties:

1. A pass cannot be scheduled if its station reservation overlaps another accepted pass.
2. Concurrent scheduling requests cannot bypass the conflict rule.
3. A pass cannot be scheduled inside the configured minimum lead time.
4. Root cannot bypass the lead-time rule merely by being Root.
5. Root can change the lead-time configuration, with the agreed delay before the new value can be used.
6. Root can override a conflicting scheduled pass and the old pass remains auditable.
7. Worker can execute previously synchronized passes while Server is offline.
8. Worker reconnects and reconciles to the latest Server state.
9. Worker does not replay missed passes.
10. Worker marks recordings/data appropriately and safely cleans local storage above 90% usage.
11. Recording uploads are idempotent.
12. Worker offline state blocks new scheduling for that station.
13. Changing active VHF/UHF station configuration does not silently destroy existing approved passes.
14. Users do not see another user's private identity information through scheduling conflicts.
15. Public pass recordings can be viewed/downloaded by allowed users.
16. Private pass recordings are not exposed to unrelated normal users.
17. Yaesu G-550 serial commands remain within valid azimuth/elevation bounds.
18. Root cannot be deleted/demoted.
19. Initial `root/toor` bootstrap requires immediate password change.
20. All passwords use Argon2id.
21. The Worker is deployable independently from the Server.
22. The Server and Worker can be restarted without corrupting durable schedule state.
23. Current TLE retrieval has fallback behavior and preserves a last known-good TLE.
24. The Server's Python computation service communicates with Go through gRPC.

---

# 30. Final Architectural Summary

Aagasa should feel like:

```text
             SERVER
     authoritative planner
       + scheduler + API
       + satellite/TLE data
       + approvals + storage
                |
          executable plans
                |
             WORKER
      lightweight edge executor
        + rotator + SDR
        + SatDump + recording
                |
         physical station
```

The core design philosophy is:

> **The Server decides what should happen. The Worker reliably performs it.**

When the Server disappears, the Worker keeps doing what it already knows how to do.

When the Worker disappears, the Server stops making promises that require an unavailable station.

When connectivity returns, both reconcile to the latest valid state.

The implementation should favor correctness, durability, operational safety, and clear boundaries over premature abstraction.
