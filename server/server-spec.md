# Aagasa Server — Component Specification

## 0. Purpose

The Aagasa Server is the authoritative control/orchestration plane.

It owns users, authorization, station configuration, satellite catalogue data, orbital data, prediction, pass scheduling, approvals, worker state, recording metadata/storage coordination, client APIs, and audit history.

The Server performs the heavy/global work so the Worker can remain a lightweight edge executor.

---

# 1. Relationship to Global Specification

This document refines `spec.md` and must not contradict it.

Core invariants:

- PostgreSQL is authoritative for scheduling/resource conflicts.
- Python satellite computation is exposed to Go via gRPC.
- Worker receives executable PassPlans.
- Server is authoritative desired state.
- Worker remains functional during Server outages.
- One-worker/one-station is the initial deployment, but the model supports multiple workers/stations.

---

# 2. Technology

Primary backend:

- Go
- REST/HTTP
- WebSocket
- gRPC
- PostgreSQL
- MongoDB
- Redis
- filesystem storage

Internal Python service:

- Python
- `uv`
- Skyfield
- SGP4 and other required satellite libraries
- gRPC

Deployment:

- Podman
- Podman Compose (`deployment/compose.yaml`), rootless
- a single idempotent `deploy.sh server|worker|both|down`

---

# 3. Logical Server Components

A sensible logical decomposition is:

```text
server
├── HTTP/REST API
├── client websocket gateway
├── authentication/session
├── users/RBAC
├── stations
├── workers
├── satellites
├── TLE service
├── metadata enrichment
├── prediction client
├── scheduling
├── approvals
├── PassPlan generation
├── worker gRPC client/service
├── recording management
├── audit
└── configuration
```

These are logical boundaries, not a requirement to build many separate deployments.

Go should remain the primary process.

The Python service should remain a focused internal computation service.

---

# 4. Server as Authority

The Server is authoritative for:

- user identity
- role/permissions
- station configuration
- satellite catalogue
- latest canonical TLE metadata
- pass requests
- approval decisions
- scheduling conflicts
- PassPlan generation
- desired Worker state
- public/private visibility
- audit records

The Worker is authoritative only for local execution facts until those facts are synchronized back.

---

# 5. Authentication and RBAC

## Roles

### Root

Highest authority.

- created during first-run bootstrap
- initial credentials are `root` / `toor`
- forced to change password immediately
- cannot be deleted
- cannot be demoted
- can promote users to Admin
- can perform critical system configuration
- can override scheduling decisions according to global rules

### Admin

Second priority.

- manages normal operational/admin tasks
- reviews/approves normal-user pass requests
- manages satellites where authorized
- sees operational Worker/station information
- cannot perform Root-only critical operations

### Normal User

- schedule/request passes
- view own passes
- view public passes
- access recordings according to visibility rules
- see sanitized conflict/result information

All passwords use Argon2id hashing.

---

# 6. Root Bootstrap and Recovery

On first initialization:

1. Detect uninitialized system.
2. Create Root from `root` / `toor` bootstrap credentials.
3. Require password change before normal use.
4. Run Root setup.
5. Persist initialization completion.

The bootstrap password must not remain usable after password change.

Provide a CLI or equivalent secure server-side mechanism to change/reset the Root password in a compromised/recovery scenario.

Never log passwords or credentials.

---

# 7. First-Run Setup

Root setup should include at minimum:

- station identity/name
- location
- latitude/longitude/altitude
- timezone if needed for UI
- active RF mode
- VHF antenna/configuration
- UHF antenna/configuration
- Yaesu G-550 serial settings
- RTL-SDR settings
- minimum scheduling lead time
- pre-pass scheduling buffer
- post-pass scheduling buffer
- recording pre-roll
- recording post-roll
- TLE provider configuration as appropriate
- initial satellite catalogue selection

The setup UI may be built by the Client, but Server must enforce all resulting constraints.

---

# 8. Station and Worker Model

Server domain entities should include stable IDs for:

- Station
- Worker

A Worker belongs to a station in its active configuration.

The V1 deployment uses one station/worker.

The data model should not assume only one forever.

A station has one active RF mode at a time:

- VHF
- UHF

The Root can switch this mode and change corresponding station settings/antenna configuration.

The pass's required band is a scheduling/execution parameter, but mismatch with current station mode is a **warning**, not automatically a conflict.

---

# 9. Scheduling Configuration

Root configures:

- `minimum_schedule_lead_time`
- `pre_pass_buffer`
- `post_pass_buffer`
- `recording_pre_roll`
- `recording_post_roll`
- minimum elevation for pass prediction

## Lead-time rule

A pass can only be scheduled when sufficiently far in the future according to `minimum_schedule_lead_time`.

The rule applies to every role, including Root.

If Root changes the minimum lead time, the new value cannot be used to bypass the waiting period specified by the global project requirement.

## Buffer rule

For resource reservation:

```text
reserved_start = AOS - pre_pass_buffer
reserved_end   = LOS + post_pass_buffer
```

Changing buffer settings affects new scheduling decisions.

Existing approved passes retain their existing reservation semantics.

---

# 10. Pass Lifecycle

Recommended lifecycle:

```text
PENDING_APPROVAL
APPROVED
REJECTED
CANCELLED
EXECUTING
COMPLETED
FAILED
MISSED
```

Additional internal states may exist if required, but do not create an unnecessarily large state machine.

Meaning:

- `PENDING_APPROVAL`: requested, waiting for authorization
- `APPROVED`: accepted and executable
- `REJECTED`: denied
- `CANCELLED`: approved/requested pass removed from future execution
- `EXECUTING`: Worker has begun execution
- `COMPLETED`: execution ended successfully
- `FAILED`: execution started but failed
- `MISSED`: required execution window passed without successful Worker execution

Historical records must not be destroyed to hide state transitions.

---

# 11. Scheduling Algorithm

The Server is the only authoritative scheduler.

For every scheduling request:

1. authenticate user
2. validate permissions
3. resolve satellite
4. validate orbital data
5. predict pass
6. validate minimum lead time
7. calculate station reservation interval using configured buffers
8. check Worker/station availability
9. transactionally check overlapping reservations
10. apply approval rules
11. persist request/result
12. generate/update desired PassPlan state when approved

Do not perform non-transactional check-then-insert conflict logic.

Use PostgreSQL transactional guarantees such as:

- appropriate exclusion/constraint strategy
- locks/advisory locks where useful
- correct transaction isolation

The exact implementation should choose the simplest robust PostgreSQL-native method.

---

# 12. Overlap Rule

For the same station, **any overlapping reservation is a conflict**, regardless of:

- satellite
- VHF/UHF band
- user

Example:

```text
A: 10:00–10:15
B: 10:10–10:20
```

B must not be accepted for the same station.

The system must correctly reject concurrent attempts too.

---

# 13. Worker Offline Rule

When the Worker is offline:

- show Worker offline status
- disable new scheduling for that station

Existing future approved passes remain in history/schedule state.

If the pass reaches its required execution window while the Worker is unavailable, mark it `MISSED`.

Never replay a missed pass.

---

# 14. Root Override

Root is allowed to override a conflicting scheduled pass.

If Root creates an overriding schedule:

1. identify conflicting pass(es)
2. mark them cancelled due to Root override
3. preserve their history/audit information
4. create/approve Root's replacement schedule
5. update Worker desired state

Do not physically delete old pass rows merely to resolve the conflict.

Suggested cancellation reason:

```text
CANCELLED_BY_ROOT_OVERRIDE
```

---

# 15. Approval Rules

Normal users create `PENDING_APPROVAL` requests.

Admin can approve/reject according to permissions.

Root has final authority and can override an Admin decision.

Every privileged decision should create an audit record.

Root can change scheduling/system configuration, but global lead-time restrictions still apply.

---

# 16. PassPlan Generation

An approved pass becomes a fully executable PassPlan.

It must contain enough information for the Worker to execute without asking the Server for missing information.

Conceptually:

```text
pass_id
station_id
satellite_id / NORAD ID
TLE/version
AOS/LOS or execution start/end
tracking information
band
frequency/radio parameters
recording mode
recording pre/post margins
SatDump pipeline
custom pipeline JSON/reference
configuration generation/version
```

The exact protobuf schema should be stable and versioned.

A historical PassPlan should remain reproducible even when current satellite metadata/TLE changes later.

---

# 17. Python Prediction Service

Python service responsibilities:

- Skyfield setup
- SGP4 usage
- pass prediction
- AOS/LOS
- maximum elevation/TCA
- track/pointing data required by Worker

Go communicates with it using gRPC.

The Python service should be focused on computation and should not own user/session/scheduling state.

Errors must be explicit and machine-readable enough for Go to return useful API responses.

---

# 18. Satellite Catalogue

Canonical satellite identity:

- NORAD/catalog ID

A TLE is versioned orbital data.

Satellite metadata can include:

- display name
- description
- links
- downlink frequency
- amateur-radio metadata
- source metadata

Metadata is useful for presentation but is not a core prerequisite for tracking.

A satellite remains executable if reliable TLE + executable pass configuration exists even when enrichment metadata is incomplete.

---

# 19. TLE Management

Server refreshes TLEs at least every 24 hours.

Provider model should allow:

- CelesTrak
- SatNOGS or another reliable fallback where appropriate
- future providers

Behavior:

```text
preferred provider
    ↓ failure
fallback provider
    ↓ failure
last known-good TLE
```

Store at least:

- NORAD ID
- TLE lines
- epoch
- source/provider
- fetched-at
- version/identity metadata

Do not discard a last known-good TLE merely because refresh failed.

Third-party API/feed details must be verified during implementation rather than guessed.

---

# 20. Metadata Enrichment

The Server may query SatNOGS/CelesTrak for display metadata.

This information should be cached/stored so user interfaces are not dependent on live provider availability.

Metadata refresh failure should degrade gracefully.

Do not make SatDump pipeline validity dependent on metadata completeness.

---

# 21. SatDump Pipelines

The Server is responsible for exposing available standard SatDump pipeline options to the Client.

The Client should present a simple selection UI.

For custom pipelines:

- accept pipeline JSON
- validate syntax/structure as reasonably possible
- persist it as a versioned file/resource
- associate it with the pass
- pass it to the Worker

Do not build a pipeline editor.

Do not infer a pipeline solely from satellite metadata.

---

# 22. Worker Communication

Use gRPC for:

- Worker identification
- desired-state synchronization
- PassPlan delivery
- state/result reporting
- durable control operations
- synchronization acknowledgements

Use a realtime channel for:

- telemetry
- heartbeats
- pass progress
- hardware status
- upload progress

V1 trust model:

- configured Server/Worker endpoints
- TLS
- simple shared Worker credential/secret

Do not introduce a complex certificate management system.

---

# 23. Desired State Model

The Server maintains the authoritative desired state for the Worker.

The desired state includes:

- active station configuration applicable to Worker
- approved future PassPlans
- configuration generations
- cancellation/override changes

A simple generation/version/hash lets the Worker report which state it has.

On reconnect:

```text
Worker → current generation/state summary
Server → latest desired state
Worker → reconcile
Worker → acknowledge
```

Do not build a full distributed event-sourcing framework in V1.

---

# 24. Worker Availability

Recommended policy:

- telemetry/heartbeat approximately every 5 seconds
- Worker considered offline after roughly 15 seconds without heartbeat
- connection state shown to admins/Root and relevant user flows

Track:

- last seen
- last sync
- Worker version
- station association
- current pass
- current Worker state

---

# 25. Recording Management

Server owns authoritative recording metadata and server-side file storage.

Worker uploads completed outputs.

Uploads must be idempotent.

The Server must not create duplicate logical recordings when the Worker retries an upload.

A V1 API may accept complete file uploads.

The server should persist recording status such as:

```text
PENDING_UPLOAD
UPLOADING
STORED
FAILED
```

The exact state machine may be smaller if implementation proves sufficient.

---

# 26. Visibility and Privacy

A pass may be:

- private
- public

Private pass visibility:

- owner
- Admins
- Root

Public pass visibility:

- all platform users may view allowed historical information
- recordings/downloads are available according to the public-pass policy

For a user whose own scheduling request conflicts with an existing pass, show only sanitized information such as:

- occupied period
- satellite/pass output/status where appropriate

Do not show the first user's identity or private user information.

After a pass has completed, subsequent/other users may see its execution output/status as allowed, without revealing the original owner's identity.

---

# 27. REST API Principles

Use REST for request-response operations.

The API should have clear resource-oriented endpoints for:

- authentication/session
- current user
- users/admin operations
- stations
- Workers
- satellites
- TLE/status
- pass predictions
- scheduling requests
- pass approvals
- pass details
- recordings
- configuration
- audit/log views where authorized

Rules:

- validate all input server-side
- enforce RBAC server-side
- return useful HTTP status codes
- use consistent error structures
- never trust Client-side authorization decisions

Exact route design belongs to the implementation/protocol phase and should be documented once chosen.

---

# 28. Client WebSocket

WebSocket delivers user-relevant realtime data such as:

- Worker online/offline
- pass state changes
- execution progress
- recording/upload progress
- approval/result notifications
- relevant station status

Do not broadcast sensitive private-pass details to unauthorized clients.

The Server is responsible for filtering events by authorization and visibility.

---

# 29. Audit Trail

Record privileged and important state changes, including:

- Root password/security actions
- user role changes
- pass approval/rejection
- Root overrides
- pass cancellations
- station configuration changes
- TLE/catalogue administrative changes
- relevant Worker configuration changes

Audit entries should include, where useful:

- actor
- action
- target
- timestamp
- previous state
- new state
- reason/context

Do not store secrets in audit logs.

---

# 30. Error Handling

The Server should distinguish:

- invalid user input
- permission errors
- scheduling conflict
- Worker offline
- external provider failure
- prediction failure
- Worker execution failure
- recording storage failure
- internal Server error

Never leak stack traces or secrets through public APIs.

Internal logs should preserve diagnostics.

---

# 31. Concurrency and Consistency

Critical mutations must be transactional.

Especially:

- scheduling
- approval state transitions
- Root override
- configuration changes with scheduling impact
- recording deduplication metadata

Avoid race-prone application-only conflict checks.

---

# 32. Testing Requirements

## Unit

- RBAC
- lead-time logic
- buffer calculation
- overlap detection
- Root override
- pass state transitions
- TLE provider fallback
- PassPlan generation
- visibility filtering
- idempotency handling

## Integration

- PostgreSQL migrations
- Redis sessions
- MongoDB operational documents
- Go↔Python gRPC
- Server↔Worker gRPC
- REST API
- WebSocket authorization/filtering
- filesystem recording storage

## Scenario tests

1. first-run bootstrap
2. normal scheduling
3. overlapping scheduling
4. concurrent overlapping scheduling
5. Admin approval
6. Root override
7. Worker online/offline
8. Server outage with Worker executing
9. Worker reconnect
10. recording retry/deduplication
11. TLE provider outage
12. station VHF/UHF mismatch warning
13. lead-time restriction
14. configuration change notification
15. public/private recording access

---

# 33. Deployment

Server deployment should follow the same Podman-oriented project strategy:

- Podman
- Podman Compose (`deployment/compose.yaml`), rootless
- a single idempotent `deploy.sh server|worker|both|down`

Configuration/secrets must come from deployment configuration/environment/secret mechanisms.

Do not hard-code database passwords, session secrets, Worker credentials, or external API credentials.

---

# 34. Implementation Rules for Claude Code

1. Read global `spec.md` and this file before modifying Server code.
2. Read the current phase in `plan.md` before implementing work.
3. Inspect `REFERENEC/` for related existing implementations.
4. Keep Go as the primary Server runtime.
5. Keep Python focused on satellite computation.
6. Communicate Go↔Python through gRPC.
7. Keep PostgreSQL authoritative for scheduling.
8. Do not move core scheduling logic into MongoDB/Redis.
9. Do not make the Worker a hidden second scheduler.
10. Do not let Client-side validation replace Server-side validation.
11. Preserve historical state; avoid destructive deletion for audit-relevant domain records.
12. Add tests as each domain rule is implemented.
13. Implement only the current phase unless explicitly instructed to advance.
14. Prefer simple robust mechanisms over infrastructure complexity.
