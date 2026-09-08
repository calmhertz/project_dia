# Aagasa REST API

Authentication, users, the satellite catalogue, first-run setup, scheduling,
passes, recordings, Workers and the operational views Admin and Root use.

Authorization is always enforced server-side. Client-side role checks are for
usability only (client-spec section 4).

---

## Conventions

Authenticated calls carry the session token:

```
Authorization: Bearer <token>
```

Errors share one shape and never leak internals:

```json
{ "error": "forbidden", "message": "not permitted" }
```

| `error` | Status | Meaning |
|---|---|---|
| `invalid_request` | 400 | Malformed or invalid body |
| `invalid_password` | 400 | Password fails policy |
| `unauthenticated` | 401 | No valid session |
| `invalid_credentials` | 401 | Wrong username or password |
| `forbidden` | 403 | Authenticated but not permitted |
| `password_change_required` | 403 | Change your password first |
| `root_protected` | 403 | Root cannot be deleted or demoted |
| `not_found` | 404 | No such record |
| `conflict` | 409 | Already exists |
| `already_initialized` | 409 | Setup has already run |
| `in_use` | 409 | Other records depend on this, so it cannot be removed |
| `station_not_configured` | 409 | A setting a pass plan needs is missing, named in the message |
| `invalid_state` | 409 | The pass is not in a state where that change is legal |
| `no_orbital_data` | 502 | No TLE provider had data for that satellite |
| `metadata_unavailable` | 502 | The metadata provider did not answer |
| `prediction_unavailable` | 502 | The prediction service did not answer |
| `not_initialized` | 409 | First-run setup has not been completed |
| `internal_error` | 500 | Logged server-side, opaque to the client |

Request bodies are capped at 1 MiB and unknown fields are rejected.

### TLS

`AAGASA_TLS_CERT_FILE` and `AAGASA_TLS_KEY_FILE` encrypt both the REST and the
gRPC listener, with TLS 1.2 as the floor. They must be set together: half a
configuration is refused at startup rather than quietly serving plaintext, and
an unreadable path fails immediately.

The Worker sends its shared secret on every gRPC call, so a deployment across
any untrusted network must have this on. See deployment/README.md.

### Browser clients (CORS)

The web build runs on its own origin, so the browser blocks every call unless
the Server says otherwise.

| Variable | Meaning |
|---|---|
| `AAGASA_ALLOWED_ORIGINS` | Comma-separated origins, matched exactly (scheme and port included) |

In `AAGASA_ENV=development` any **localhost** port is additionally allowed,
because the Flutter dev server picks one at random. Production names its
origins.

The session is a bearer token in a header, not a cookie, so
`Access-Control-Allow-Credentials` is never sent and the wildcard origin is
never used: the allowed origin is echoed back only when it matches, and the
reply always varies on `Origin`. A preflight from an unknown origin gets 403
with no CORS headers. `Content-Disposition` is exposed so a download can read
the file name.

---

## Health

| Method | Path | Auth | Notes |
|---|---|---|---|
| GET | `/health` | none | Liveness. Reveals nothing about internals. |
| GET | `/readyz` | none | 503 until PostgreSQL, Redis, MongoDB and prediction all respond. Also reports recording storage. |

`/readyz` includes a `storage` object: `recordings_free_bytes`,
`recordings_used_percentage` and `accepting_uploads`. A full disk stops
uploads but does **not** make the Server unready - scheduling, approving and
browsing still work.

## Authentication

### POST `/api/auth/login`

```json
{ "username": "root", "password": "..." }
```

```json
{
  "token": "...",
  "expires_at": "2026-08-25T09:00:00Z",
  "must_change_password": true,
  "user": { "id": "...", "username": "root", "role": "root",
            "must_change_password": true, "created_at": "..." }
}
```

An unknown user and a wrong password return the identical 401, and take
comparable time, so the response does not disclose whether an account exists.

### POST `/api/auth/change-password`

Auth required. **Reachable while `must_change_password` is set** — it is the
only way out of that state.

```json
{ "current_password": "...", "new_password": "..." }
```

`204` on success. The current session is revoked, so the client signs in again.

Policy: at least 12 characters, and the bootstrap password `toor` may never be
chosen.

### GET `/api/auth/me`

Auth required; allowed while a password change is owed, so the client can
discover why it is blocked. Returns the user, never the password hash.

### POST `/api/auth/logout`

Auth required. `204`, and the token stops working.

## Users

Every route below also requires the caller to have changed their password.

| Method | Path | Minimum role | Notes |
|---|---|---|---|
| GET | `/api/users` | admin | |
| POST | `/api/users` | admin | Admin may create normal users; only Root may create Admins. Nobody may create a Root. |
| PATCH | `/api/users/{id}` | root | Role change only. |
| DELETE | `/api/users/{id}` | root | `409 in_use` when the account has passes on record |

New accounts are created with `must_change_password: true`.

Root is undeletable and undemotable, no one can be promoted to Root, and Root
cannot delete itself. The database enforces the Root rules independently of
the API.

## Satellites

Reading the catalogue needs any authenticated account; changing it is Root
only (spec.md section 17.1).

| Method | Path | Minimum role | Notes |
|---|---|---|---|
| GET | `/api/satellites` | any | |
| GET | `/api/satellites/{id}` | any | Includes `current_tle` when orbital data exists |
| GET | `/api/satellites/{id}/tles` | any | Up to 20 versions, newest first |
| POST | `/api/satellites` | root | Add by catalog number |
| PATCH | `/api/satellites/{id}` | root | `name`, `description`, `is_schedulable` |
| DELETE | `/api/satellites/{id}` | root | `409` when passes reference it |
| POST | `/api/satellites/refresh-tles` | admin | Force a catalogue-wide refresh |
| POST | `/api/satellites/{id}/refresh-metadata` | root | Re-run enrichment |
| GET | `/api/satellites/{id}/passes` | any | Predict upcoming passes |

### POST `/api/satellites`

```json
{ "norad_id": 25544, "name": "optional override" }
```

The Server fetches orbital data before creating the record, because a
satellite without it cannot be tracked. If no provider has data the request
returns `502 no_orbital_data` and nothing is stored.

Display metadata is fetched best effort. A satellite with valid orbital data
is trackable whether or not enrichment succeeded (spec.md section 11.2); use
`refresh-metadata` to fill it in later.

### POST `/api/satellites/refresh-tles`

```json
{ "updated": 1, "kept_last_known_good": 2, "failed": 0, "results": [ ... ] }
```

- `updated` - a newer element set was stored
- `kept_last_known_good` - every provider failed, the stored record stands
- `failed` - no provider answered and nothing was stored previously

A total provider outage produces `kept_last_known_good`, not `failed`, and
never discards stored orbital data.

### GET `/api/satellites/{id}/passes`

Read-only pass prediction over the configured station. It reserves nothing;
requesting and approving a pass is V7.

| Query | Default | Range |
|---|---|---|
| `hours` | 48 | 1..336 |
| `track_step_seconds` | 0 (no track) | 0..600 |

```json
{
  "norad_id": 25544,
  "minimum_elevation_degrees": 10,
  "station_active_band": "vhf",
  "tle_epoch": "2026-08-24T10:26:46Z",
  "passes": [
    {
      "aos": "...", "tca": "...", "los": "...",
      "aos_azimuth_degrees": 189.7,
      "tca_azimuth_degrees": 130.2,
      "los_azimuth_degrees": 60.9,
      "max_elevation_degrees": 31.0,
      "duration_seconds": 359,
      "track": [
        { "at": "...", "azimuth_degrees": 189.7,
          "elevation_degrees": 10.0, "range_km": 1481 }
      ]
    }
  ]
}
```

The prediction uses the exact stored element set, reported as `tle_epoch`, so
it is reproducible against that TLE version. Minimum elevation comes from the
station's effective scheduling configuration.

`409 no_orbital_data` when the satellite has no TLE, `409 not_initialized`
before first-run setup, and `502 prediction_unavailable` when the prediction
service is down.

## Passes

| Method | Path | Minimum role | Notes |
|---|---|---|---|
| GET | `/api/passes` | any | `?scope=mine\|public\|all`, `?status=` |
| POST | `/api/passes` | any | Request a pass |
| GET | `/api/passes/{id}` | any | Subject to visibility |
| POST | `/api/passes/{id}/approve` | admin | Builds the executable plan first; refuses rather than half-approving |
| POST | `/api/passes/{id}/reject` | admin | |
| POST | `/api/passes/{id}/cancel` | owner, admin or root | |
| GET | `/api/passes/{id}/plan` | admin | The generated executable plan |
| GET | `/api/passes/{id}/recordings` | any | Subject to the pass's visibility |
| GET | `/api/scheduling-config` | admin | The rules in force |
| PUT | `/api/scheduling-config` | root | New configuration version |

### POST `/api/passes`

```json
{
  "satellite_id": "...",
  "aos": "2026-08-25T18:11:27Z",
  "band": "vhf",
  "recording_mode": "raw",
  "visibility": "private",
  "override": false
}
```

`aos` identifies which predicted pass the user picked. **The Server
re-predicts and uses its own timings**; client-supplied values are never
stored. A requested time more than two minutes from any predicted pass gives
`404 pass_not_found`.

`override: true` is Root-only and cancels conflicting passes
(spec.md section 13.7).

```json
{ "pass": { ... }, "warnings": [ ... ], "overridden_pass_ids": [ ... ] }
```

`requested_by` is present only for the owner, an Admin or Root.

### Scheduling refusals

| `error` | Status | Meaning |
|---|---|---|
| `station_conflict` | 409 | Overlaps a live reservation |
| `lead_time` | 409 | Starts inside the minimum lead time |
| `worker_offline` | 409 | The station cannot execute |
| `not_schedulable` | 409 | The satellite is closed for scheduling |
| `pass_not_found` | 404 | No predicted pass at that time |

A conflict reports the occupied window and nothing about who holds it:

```json
{
  "error": "station_conflict",
  "message": "This time is already occupied.",
  "occupied_from": "...", "occupied_to": "..."
}
```

Reading someone else's private pass returns **404, not 403** — revealing that
it exists would itself disclose their booking.

### Rules the scheduler enforces

- **Any overlap on the station conflicts**, whatever the satellite, band or
  user. Enforced by a PostgreSQL exclusion constraint, so concurrent requests
  cannot race past it.
- **Reservation** = AOS minus the pre-pass buffer to LOS plus the post-pass
  buffer. Distinct from recording margins.
- **The lead-time rule binds every role, Root included.** Override bypasses
  conflicts, never lead time.
- **A band mismatch warns**, it does not block.
- **Terminal passes release the station**; cancelled and rejected rows stay
  for history and are never deleted.

### GET `/api/scheduling-config`

Admin or Root. The configuration **in force now**, not the newest saved one:

```json
{
  "minimum_lead_time_seconds": 1800, "pre_pass_buffer_seconds": 120,
  "post_pass_buffer_seconds": 120, "recording_pre_roll_seconds": 10,
  "recording_post_roll_seconds": 10, "minimum_elevation_degrees": 10,
  "effective_from": "...", "created_at": "..."
}
```

An Admin approves passes, so they must be able to read the rules those
approvals are judged by; changing them stays with Root.

When a change has been saved but is not yet in force, the response also
carries it:

```json
{ "pending": { "minimum_lead_time_seconds": 600, "effective_from": "..." } }
```

Without that, saving a change and reloading shows the old values returning,
which is indistinguishable from a save that failed.

**Precedence.** The configuration in force is the most recently *created* one
whose `effective_from` has passed - not the one with the latest
`effective_from`. Saving a long delay and then a short one leaves two pending
changes; when the long one's moment arrives it must not undo the later
decision.

### PUT `/api/scheduling-config`

Saves a new configuration version and returns when it becomes usable:

```json
{ "minimum_lead_time_seconds": 60, "effective_from": "..." }
```

**A shorter lead time waits; a longer or unchanged one applies at once.**

Shortening could be used to book sooner than the old rule allowed, so
`effective_from` is now plus the newly chosen lead time
(spec.md section 13.6). Lengthening is a tightening and cannot be a bypass of
anything, so delaying it would only leave the looser rule in force - and an
operator who repeats the change while waiting would never see it take effect.

Existing approved passes keep the configuration they were accepted under.

## SatDump pipelines

| Method | Path | Minimum role | Notes |
|---|---|---|---|
| GET | `/api/pipelines` | any | What a pass may be given |
| POST | `/api/pipelines` | admin | Upload a custom definition |

```json
{
  "pipelines": [
    { "id": "...", "name": "noaa_apt", "is_custom": false },
    { "id": "...", "name": "My APT", "is_custom": true,
      "checksum_sha256": "..." }
  ],
  "reported_by_station": true
}
```

Standard pipelines are whatever the station's SatDump provides; the Worker
reports them when it registers and the Server does not invent any.
`reported_by_station: false` means no station has connected yet, which is not
the same as a station with no pipelines.

`POST` takes `{ "name": "...", "definition": "<pipeline JSON>" }`, capped at
256 KiB. Validation is **structural only**: valid JSON, an object, at least
one named pipeline whose value is an object. Anything else is
`400 invalid_pipeline` with the reason. SatDump's own semantics are not
checked, because the Server cannot honestly check them.

A pass with `recording_mode` of `process` or `raw_and_process` **must** carry
a `pipeline_id`, or the request is refused with `400 pipeline_required`.
Without it the Worker would fall back to a raw capture and quietly produce
something other than what was asked for.

## Recordings

| Method | Path | Minimum role | Notes |
|---|---|---|---|
| GET | `/api/recordings` | any | `?scope=mine\|public\|all` |
| GET | `/api/passes/{id}/recordings` | any | The recordings of one pass |
| GET | `/api/recordings/{id}/download` | any | Streams the stored file |

A recording has no visibility of its own: **the right to it is derived from
its pass every time**, so a download link that leaks is still refused. An
unrelated user gets `404`, matching the pass itself.

```json
{
  "recordings": [{
    "recording_id": "<sha256 hex>", "pass_id": "...",
    "relative_path": "iss/raw.wav", "size_bytes": 2048,
    "checksum_sha256": "...", "status": "stored",
    "received_at": "...",
    "download_url": "/api/recordings/<recording_id>/download"
  }]
}
```

The server-side path is never published; clients follow `download_url`.
`recording_id` is a 64-character lowercase hash and anything else is rejected
with `400 invalid_request` before any lookup.

The download responds `200` with `application/octet-stream`, `Content-Length`
and `Content-Disposition: attachment` using only the base name. If the
metadata outlives the file, the answer is `404 content_missing` rather than an
empty body.

## Workers

| Method | Path | Minimum role | Notes |
|---|---|---|---|
| GET | `/api/workers` | admin | Station reachability and sync state |

```json
{
  "workers": [{
    "name": "worker-1", "connection_state": "online",
    "in_sync": true, "pending_uploads": 0, "pending_reports": 0,
    "last_seen_at": "...", "last_sync_at": "...", "seconds_since_seen": 2.9
  }],
  "heartbeat_interval_seconds": 5,
  "offline_after_seconds": 15
}
```

A Worker registers by name over gRPC; the Server owns the identifiers. While a
worker is offline, new scheduling for its station is refused with
`409 worker_offline`.

## Station configuration (Root)

| Method | Path | Minimum role | Notes |
|---|---|---|---|
| GET | `/api/station/config` | root | The complete configuration |
| PATCH | `/api/station` | root | Identity, location, active band |
| PUT | `/api/station/bands/{band}` | root | One band's antenna and radio settings |
| PUT | `/api/station/hardware` | root | Rotator and receiver |
| GET | `/api/audit` | root | The audit trail |

### GET `/api/station/config`

```json
{
  "station_id": "...", "name": "...", "latitude_degrees": 13.394944,
  "longitude_degrees": 77.729444, "altitude_m": 915,
  "timezone": "Asia/Kolkata", "active_rf_band": "vhf",
  "bands": [{ "band": "vhf", "antenna_description": "Turnstile",
              "sample_rate_hz": 2048000, "gain_db": 40,
              "ppm_correction": 0, "bias_tee_enabled": false }],
  "hardware": { "rotator_serial_port": "/dev/ttyUSB0",
                "rotator_baud_rate": 9600,
                "rotator_park_azimuth_degrees": 0,
                "rotator_park_elevation_degrees": 0,
                "sdr_device_identifier": "rtlsdr" },
  "upcoming_passes": 2
}
```

### PATCH `/api/station` and PUT `/api/station/hardware`

Every field is optional; an absent field is left as it was. Both answer with
the saved values plus:

```json
{ "upcoming_passes_unchanged": 2 }
```

**That count is a report, not a refusal.** A configuration change never
rewrites a pass that is already pending or approved (spec.md section 13.8);
the passes keep the settings they were accepted under, and Root is told how
many there are.

Validation happens before any write: latitude -90..90, longitude -180..180,
band `vhf` or `uhf`, positive baud rate and sample rate, and the G-550's own
limits for the park position - azimuth 0..359, elevation 0..90
(spec.md section 9).

### GET `/api/audit`

`?action=`, `?entity_type=`, `?entity_id=`, `?limit=` (1..200, default 200).

```json
{
  "records": [{
    "id": 41, "action": "station.updated", "entity_type": "station",
    "entity_id": "...", "actor_user_id": "...", "created_at": "..."
  }],
  "limit": 200
}
```

The recorded before-and-after state is **not** returned. It is written for
forensic reading at the database and names configuration that should not be
browsable over HTTP. There is no write, edit or delete counterpart: the trail
is append-only and the database enforces it.

## Operational views

Admin or Root. These report how the station is **operating**; how it is
**configured** is the Root surface (`POST /api/setup` today, more in V15).

| Method | Path | Minimum role | Notes |
|---|---|---|---|
| GET | `/api/station` | admin | Where the station is and what it is on |
| GET | `/api/tle-status` | admin | Orbital-data freshness across the catalogue |

### GET `/api/station`

```json
{
  "station_id": "...", "name": "SJCIT Ground Station",
  "latitude_degrees": 13.394944, "longitude_degrees": 77.729444,
  "altitude_m": 915, "timezone": "Asia/Kolkata",
  "active_rf_band": "vhf", "initialized_at": "...",
  "configured_bands": ["vhf", "uhf"],
  "antenna_descriptions": ["Turnstile", "Yagi"]
}
```

Serial ports, baud rates, gains, PPM and the SDR device identifier are
deliberately absent. `409 not_initialized` before first-run setup.

### GET `/api/tle-status`

```json
{
  "satellites": [{
    "satellite_id": "...", "norad_id": 25544, "name": "ISS (ZARYA)",
    "has_orbital_data": true, "epoch": "...", "source": "celestrak",
    "fetched_at": "...", "age_hours": 6.2, "stale": false
  }],
  "stale_after_hours": 72, "missing_count": 0, "stale_count": 1
}
```

**Missing and stale are separate states.** A satellite that has never had
orbital data reports `has_orbital_data: false` with no `age_hours`, and is
counted in `missing_count`, not `stale_count`. The threshold is returned so
the client does not hard-code it.

## First-run setup

| Method | Path | Role | Notes |
|---|---|---|---|
| GET | `/api/setup` | any authenticated | `{ "initialized": bool, "initialized_at": ... }` |
| POST | `/api/setup` | root | Runs once; `409` afterwards. |

`POST /api/setup` writes the station, its per-band RF configuration, the
rotator and SDR configuration, the initial scheduling configuration and the
station's Worker **in a single transaction**, then marks the system
initialized. A rejected setup leaves nothing behind.

```json
{
  "station_name": "SJCIT Ground Station",
  "latitude": 13.394944, "longitude": 77.729444, "altitude_m": 915,
  "timezone": "Asia/Kolkata",
  "active_rf_band": "vhf",
  "bands": [
    { "band": "vhf", "antenna_description": "Turnstile",
      "sample_rate_hz": 2048000, "gain_db": 40 },
    { "band": "uhf", "antenna_description": "Yagi",
      "sample_rate_hz": 2048000, "gain_db": 40 }
  ],
  "rotator_serial_port": "/dev/ttyUSB0",
  "rotator_baud_rate": 9600,
  "rotator_park_azimuth_degrees": 0,
  "rotator_park_elevation_degrees": 0,
  "sdr_device_identifier": "rtlsdr",
  "minimum_lead_time_seconds": 1800,
  "pre_pass_buffer_seconds": 120,
  "post_pass_buffer_seconds": 120,
  "recording_pre_roll_seconds": 10,
  "recording_post_roll_seconds": 10,
  "minimum_elevation_degrees": 10,
  "worker_name": "worker-1"
}
```

Rotator park values are validated against the G-550 ranges (azimuth 0..359,
elevation 0..90) before any write, and again by the database.

---

## First start

1. Start the Server against a migrated database. It creates `root` / `toor`
   with `must_change_password` set and logs a warning. The credential itself
   is never logged.
2. Sign in as `root` / `toor`. Everything except `/api/auth/me`,
   `/api/auth/change-password` and `/api/auth/logout` returns 403.
3. `POST /api/auth/change-password`. `root`/`toor` stops working immediately.
4. Sign in with the new password and `POST /api/setup`.

## Root password recovery

For a lockout or compromise, from the server host (spec.md section 17.1):

```sh
AAGASA_POSTGRES_URL=postgres://... aagasa-rootpw
```

It prompts twice without echo, or reads one line from stdin when piped, so the
password never enters shell history, process arguments or logs. The reset is
recorded in the audit trail; the password is not.

## Audited actions

`user.bootstrap_created`, `auth.login`, `auth.login_failed`,
`auth.password_changed`, `auth.password_change_failed`,
`auth.root_password_reset_via_cli`, `user.created`, `user.role_changed`,
`user.deleted`, `system.initialized`, `satellite.added`, `satellite.updated`,
`satellite.deleted`, `satellite.metadata_refreshed`, `tle.refreshed`,
`pass.requested`, `pass.approved`, `pass.rejected`, `pass.cancelled`,
`pass.cancelled_by_root_override`, `pass.root_override_created`,
`scheduling_config.updated`.

Audit rows are append-only; the database rejects updates and deletes.

## Not implemented in V3

Login rate limiting and account lockout. spec.md section 18 says not to
over-engineer V1 authentication, so this is deferred rather than forgotten —
it belongs with the V16 hardening pass.

---

## TLE providers

Verified live on 2026-08-24. Both answer unauthenticated.

| Provider | Endpoint | Unknown satellite |
|---|---|---|
| CelesTrak (primary) | `gp.php?CATNR=<id>&FORMAT=TLE` | `404` with body `No GP data found` |
| SatNOGS (fallback) | `/api/tle/?norad_cat_id=<id>` | `200` with `[]` |

CelesTrak returns CRLF line endings and pads the title line with trailing
spaces; SatNOGS prefixes the title with `0 `. Both are normalised on parse.

Order is CelesTrak, then SatNOGS, then the last known-good record already
stored (spec.md section 11.3). Endpoints are overridable via
`AAGASA_CELESTRAK_URL` and `AAGASA_SATNOGS_URL`.

Every element set is checksum-validated before storage, so a corrupted
response is refused and the stored record survives.

The Server sweeps hourly and refreshes anything older than 24 hours. Set
`AAGASA_TLE_REFRESH_ENABLED=false` to disable the loop.
