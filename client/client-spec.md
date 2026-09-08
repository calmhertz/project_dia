# Aagasa Client — Component Specification

## 0. Purpose

The Aagasa Client is the user interface for Aagasa.

It is implemented in Flutter and targets:

- Web
- Android
- iOS

The Client communicates only with the Server.

The Client must not communicate directly with the Worker, PostgreSQL, MongoDB, Redis, SatNOGS, CelesTrak, SatDump, or physical hardware.

---

# 1. Relationship to Global Specification

This document refines `spec.md` and must not contradict it.

Core rules:

- Material 3
- dark mode only
- theme seed `#487CE5`
- responsive UI
- simple and robust
- normal users are abstracted from infrastructure internals
- Admins see operational details needed to administer the station
- Root sees complete system controls appropriate to the role
- authorization is enforced by the Server, not trusted from Client UI state

---

# 2. Technology

- Flutter
- Dart
- Material 3
- Web
- Android
- iOS

Use platform-independent Flutter architecture wherever practical.

Do not create three independent UI implementations.

---

# 3. Visual Design

## Theme

Material 3 dark-only.

Initial theme seed:

```text
#487CE5
```

Implement the color through `ColorScheme.fromSeed` or the current Material 3 equivalent so the seed can be changed later in one place.

Do not implement a light theme in V1.

## Design goals

- readable
- operationally clear
- low visual noise
- responsive
- accessible enough for long-duration monitoring
- strong status indication
- avoid unnecessary animation

Do not imitate an enterprise dashboard full of unrelated widgets.

---

# 4. User Roles and UI Scope

## Normal User

The Normal User should mainly think in terms of:

- satellites
- passes
- scheduling requests
- approval state
- execution state
- recordings
- public history

Do not expose internal infrastructure details such as:

- gRPC
- database names
- Redis
- MongoDB
- container state
- service topology

## Admin

Admin may see:

- Worker status
- station status
- approval queues
- scheduling constraints
- satellite administration
- operational warnings
- recording/processing state
- relevant system health information

## Root

Root may see:

- complete station configuration
- Worker configuration/status
- user management
- role promotion
- scheduling configuration
- antenna/RF configuration
- system initialization/settings
- audit information
- operational details

The Client must still enforce UI-level visibility for usability, but Server authorization remains authoritative.

---

# 5. Navigation Model

Keep navigation simple.

A reasonable V1 structure is:

```text
Authenticated
├── Dashboard
├── Satellites
├── Schedule / Passes
├── My Passes
├── Public History
├── Recordings
└── Administration (role dependent)
```

The exact navigation pattern may differ by platform and screen width.

Mobile and web should preserve the same information architecture even if their navigation controls differ.

---

# 6. Authentication UI

## Login

Provide:

- username
- password
- submit
- clear validation/error states

## First-run Root setup

After the bootstrap account is created and authenticated, Root must be guided through password change/setup.

The Client should not expose `root/toor` as a normal permanent login choice.

Server determines whether the first-run setup requirement remains active.

## Session behavior

The Client should:

- securely store the chosen session/refresh mechanism
- restore a valid session when appropriate
- handle expiration
- redirect to login when session is invalid
- never log tokens/secrets

The exact token mechanism is dictated by the Server API.

---

# 7. Dashboard

Dashboard should present information appropriate to the role.

Normal user examples:

- upcoming accessible/relevant passes
- their pending approvals
- recent completed passes
- recent public results
- useful station availability indication

Admin/Root examples:

- Worker online/offline
- station active RF mode
- current pass
- current hardware/recording status
- approval queue
- important operational alerts

Do not overload the dashboard with low-value telemetry.

---

# 8. Satellite Catalogue

The satellite list should present human-readable metadata.

At minimum, where available:

- display name
- NORAD ID
- band/frequency information
- useful source metadata
- availability for scheduling

Because metadata is enrichment, the UI must gracefully handle incomplete metadata.

Satellite identity should be represented by NORAD ID, not a mutable TLE string.

---

# 9. Satellite Detail

Satellite detail can show:

- name
- NORAD ID
- TLE freshness/status where useful
- orbital/pass information
- downlink/frequency information where available
- source information where appropriate
- available SatDump pipelines/options where relevant

Normal users do not need raw provider/API internals.

---

# 10. Pass Prediction View

A user should be able to inspect an upcoming pass before requesting it.

Show useful operational information such as:

- date/time
- AOS
- LOS
- maximum elevation
- duration
- required band
- useful frequency information
- current station RF mode
- warnings

Do not make raw Skyfield/SGP4 implementation details visible.

---

# 11. Scheduling Flow

A simple scheduling flow should look approximately like:

```text
Choose satellite
      ↓
Choose available pass
      ↓
Review pass details
      ↓
Select recording/processing mode
      ↓
Select SatDump pipeline
      ↓
Optional custom pipeline JSON
      ↓
Set public/private
      ↓
Review warnings
      ↓
Submit request
```

The exact number of screens may vary.

Do not put all settings on one extremely dense screen.

---

# 12. SatDump Pipeline Selection

Show available standard pipelines supplied by the Server based on the installed/known SatDump environment.

Allow:

- selection of one standard pipeline
- upload of custom pipeline JSON

Do not build a graphical pipeline editor.

For custom pipelines:

- show file name
- validate basic file type/size
- submit JSON to Server
- show Server validation result

The Client should not try to fully validate SatDump semantics itself.

---

# 13. Radio Configuration UI

Expose useful operational parameters, but do not overwhelm normal users with every SDR implementation detail.

Depending on what the Server requires, display things such as:

- band
- frequency
- sample rate
- gain
- PPM
- bandwidth
- relevant modulation information

Prefer server-provided satellite defaults where available.

Allow only fields that the user's role is permitted to edit.

---

# 14. Recording/Processing Selection

The user should be able to select a supported operational mode, for example:

- raw recording
- processing
- raw + processing where supported

The UI should clearly explain what will be produced.

Recording timing margins should normally remain station configuration rather than a normal-user scheduling field unless the Server explicitly exposes them as user-configurable.

---

# 15. Scheduling Conflict UX

If a requested period overlaps an existing reservation, the Client must clearly communicate:

```text
This time is already occupied.
```

Show sanitized information such as:

- occupied time
- satellite/pass output/status where appropriate

Do not display:

- original user's name
- username
- email
- private profile information
- internal identity information

The UI should make it obvious that the new request was not accepted.

---

# 16. Station RF Mismatch Warning

The station can currently be configured for VHF or UHF.

A pass requiring the other band may still be scheduled.

The Client should display a warning similar in meaning to:

```text
The station is currently configured for UHF, while this pass requires VHF.
Reception may be poor unless the station configuration is changed before execution.
```

This is a warning, not a conflict message.

Do not automatically block scheduling on this basis unless the Server says the station is unavailable for another reason.

---

# 17. Approval Queue

For Admin/Root:

Show:

- pending pass request
- requester identity where authorized
- satellite
- time
- station
- estimated conflict information
- band
- selected pipeline
- recording mode
- public/private selection
- warnings

Actions:

- approve
- reject
- view details

Root must have final override capability.

Every decision is confirmed by Server and reflected in realtime UI state.

---

# 18. Pass Detail

Pass detail should provide a clear lifecycle/status representation.

Possible states:

- Pending approval
- Approved
- Rejected
- Cancelled
- Executing
- Completed
- Failed
- Missed

For users who are not authorized to see private owner data, omit owner-sensitive fields.

---

# 19. Public/Private Passes

A pass can be public or private.

## Private

Visible to:

- owner
- Admin
- Root

## Public

Normal users may access the allowed historical information, including recording/download according to the global policy.

Public-pass pages should not expose unnecessary internal user data.

---

# 20. Recordings

Recording UI should support:

- list available recordings
- pass association
- status
- file type/output information where available
- preview/metadata where practical
- download

For public passes, normal users can access and download public recordings.

For private passes, only authorized users can access the recording.

The Client should not assume a recording exists merely because a pass completed; show actual server recording state.

---

# 21. Realtime Updates

Use Server WebSocket updates for:

- Worker online/offline state
- pass status changes
- execution progress
- upload progress
- approval results
- relevant station status
- notifications

The UI should update incrementally rather than repeatedly polling large resource collections.

WebSocket disconnect must degrade gracefully.

A reconnect should restore correct state from the Server rather than assuming no updates occurred during the disconnect.

---

# 22. Offline Client Behavior

The Client is not the execution authority.

Client-side offline operation may support local UI state/cache, but it must not pretend to schedule or approve a pass while the Server is unreachable.

When a write operation cannot reach the Server:

- do not show it as accepted
- show a clear failure/pending-network message
- refresh authoritative state after reconnect

---

# 23. Error UX

Distinguish at least:

- invalid input
- authentication failure
- permission denied
- scheduling conflict
- Worker offline
- stale data
- external/provider issue reported by Server
- processing/recording failure
- unexpected Server error

Error messages should be useful to the current role without exposing internal stack traces.

---

# 24. Loading and Empty States

Every major screen should have explicit:

- initial loading state
- empty state
- error state
- retry path where appropriate

Avoid indefinite spinners.

---

# 25. Responsive Design

Support:

- desktop web
- tablet/mobile web where practical
- Android phone/tablet
- iOS phone/tablet

Use responsive layouts rather than hard-coded screen sizes.

For dense operational data:

- use cards/lists on smaller screens
- use tables/details where the viewport supports them

Do not duplicate business logic by platform.

---

# 26. State Management

Choose one straightforward Flutter state-management approach and use it consistently.

Prioritize:

- predictable state
- testability
- clear separation between API/state/UI
- low ceremony

Do not build a framework inside the app.

Keep API/domain models separate from visual widgets where reasonable.

---

# 27. API Integration

The Client should have a clean API layer for:

- authentication
- users
- stations
- Workers
- satellites
- predictions
- scheduling
- approvals
- passes
- recordings
- configuration

The REST contract should be generated/shared from authoritative API definitions where practical rather than duplicated manually.

Never trust Client-side role checks as authorization.

---

# 28. WebSocket Integration

The Client WebSocket layer should:

- authenticate
- subscribe to authorized events
- route events to appropriate state stores
- reconnect automatically
- avoid duplicate event handling
- resync authoritative state after reconnect

Never display an event merely because it arrived; the event must already have been authorized by the Server.

---

# 29. Root Administration UI

Root configuration areas should include:

- station identity/location
- VHF/UHF mode/configuration
- antenna configuration
- G-550 serial configuration
- RTL-SDR configuration
- scheduling lead time
- scheduling pre/post buffers
- recording pre/post margins
- minimum elevation
- satellite catalogue
- user/admin management
- audit information
- relevant Worker configuration/status

Root must receive clear warnings when a configuration change may affect already scheduled operations.

A configuration change must not silently rewrite an already-approved pass without Server-side policy/state change.

---

# 30. Admin UI

Admin should have:

- approval queue
- satellite catalogue administration where authorized
- station/Worker monitoring
- pass history
- recording management where authorized
- operational warnings

Admin must not see or perform Root-only controls unless the Server authorizes them.

---

# 31. Notifications

Useful notifications include:

- pass approved
- pass rejected
- pass cancelled
- Root override
- Worker offline
- Worker online again
- pass executing
- pass completed
- pass failed
- recording ready
- upload complete
- relevant station configuration change

Avoid notification spam for every low-level telemetry change.

---

# 32. Accessibility and Usability

Use sufficient contrast within the dark theme.

Status must not be conveyed only by color.

Interactive controls need understandable labels.

Use readable time/date formatting.

Do not hide important warnings behind obscure interactions.

---

# 33. Security Rules

Never store:

- plaintext passwords
- server secrets
- Worker shared credentials
- refresh/access tokens in logs

Do not expose privileged API operations through UI without Server authorization.

Do not embed external provider credentials into the Flutter app.

The Client talks only to the Aagasa Server.

---

# 34. Testing Requirements

## Widget/unit tests

- login validation
- role-based navigation visibility
- scheduling form validation
- conflict presentation
- public/private visibility presentation
- pass state rendering
- loading/error/empty states
- realtime state updates

## Integration tests

- login flow
- first-run Root setup
- schedule pass
- conflict response
- approval flow
- Root override flow
- Worker offline warning
- recording availability/download authorization
- WebSocket reconnect/resync

## Responsive/manual validation

Validate major workflows on:

- desktop web
- narrow web viewport
- Android-sized viewport
- iOS-sized viewport

---

# 35. Implementation Rules for Claude Code

1. Read global `spec.md` and this file before modifying Client code.
2. Read the current phase in `plan.md` before implementation.
3. Keep the UI simple and operationally focused.
4. Do not leak backend implementation concepts into normal-user UX.
5. Do not put authorization logic solely in the Client.
6. Do not communicate directly with Worker/hardware/external TLE providers.
7. Use Material 3 consistently.
8. Keep dark mode only in V1.
9. Use `#487CE5` as the theme seed in one centralized location.
10. Handle loading, empty, error, and disconnected states explicitly.
11. Make realtime updates resilient to reconnects.
12. Do not build a SatDump pipeline editor.
13. Add tests as UI/domain behaviors are implemented.
14. Implement only the current phase unless explicitly instructed to advance.
15. Avoid speculative features that are not in the global/component specification.
