# Aagasa Data Model (V2)

The durable domain model. No behaviour depends on it yet: authentication (V3),
satellite/TLE ingestion (V5), scheduling (V7) and recordings (V11) arrive later.

Authoritative definition: `server/go/internal/store/migrations/00001_initial_schema.sql`.
Go mirrors: `server/go/internal/domain`.

---

## 1. Storage split

| Store | Holds | Why |
|---|---|---|
| PostgreSQL | users, stations, configuration, workers, satellites, TLEs, passes, audit | Authoritative for scheduling and conflict prevention |
| MongoDB | pass execution documents, worker telemetry | Shape varies with pipeline and hardware |
| Redis | session tokens | Ephemeral only; never a system of record |
| Filesystem | recordings, uploaded pipeline JSON | Large binary content |

## 2. Relational structure

```text
users ──requested_by──────────────┐
  │                               │
  │ created_by                    │
  ▼                               ▼
scheduling_configs ──────────► passes ◄──── satellites ◄──── tle_records
  ▲                            │  ▲   ▲                          │
  │ station_id                 │  │   └──────tle_record_id───────┘
  │                            │  └──── satdump_pipelines
stations ◄────station_id───────┘
  ├── station_band_configs      (one row per band: vhf, uhf)
  ├── station_hardware_configs  (1:1, rotator and SDR)
  └── workers                   (one active worker per station in V1)

audit_records                   (append-only, references the acting user)
```

### Cardinality

| Relation | Cardinality | On delete |
|---|---|---|
| station → station_band_configs | 1 : 0..2, unique per band | cascade |
| station → station_hardware_configs | 1 : 0..1 | cascade |
| station → scheduling_configs | 1 : many versions | cascade |
| station → workers | 1 : many (V1 uses one) | restrict |
| satellite → tle_records | 1 : many versions | cascade |
| pass → station, satellite, user, tle_record, scheduling_config | many : 1 | restrict |
| pass → satdump_pipeline | many : 0..1 | restrict |

`RESTRICT` on everything a pass points at is deliberate: history must survive,
so a satellite or user with passes cannot be deleted out from under them
(spec.md section 21).

## 3. Invariants the database enforces

These hold no matter which code path writes, which is the point of putting them
in the schema rather than in Go.

| Invariant | Mechanism | Spec |
|---|---|---|
| Any overlapping reservation on one station conflicts | `EXCLUDE USING gist (station_id WITH =, reservation WITH &&)` over live statuses | 13.3 |
| Concurrent requests cannot race past that rule | The exclusion constraint is checked by PostgreSQL under concurrency | 29.2 |
| Passwords are Argon2id | `CHECK (password_hash LIKE '$argon2id$%')` | 21 |
| Exactly one Root exists | `CREATE UNIQUE INDEX ... WHERE role = 'root'` | 17.1 |
| Audit history cannot be rewritten | `BEFORE UPDATE OR DELETE` trigger raises | 22 |
| Rotator park position is within G-550 limits | `CHECK` az 0..359, el 0..90 | 9 |
| A reservation always covers its own pass | `CHECK (reserved_from <= aos_at AND reserved_to >= los_at)` | 13.4 |
| LOS follows AOS | `CHECK (los_at > aos_at)` | - |
| A cancelled pass carries a reason, and only a cancelled pass does | `CHECK ((status = 'cancelled') = (cancellation_reason IS NOT NULL))` | 13.7 |
| Re-fetching an unchanged TLE does not duplicate it | `UNIQUE (satellite_id, line1, line2)` | 11.3 |
| Latitude, longitude, elevation are physically possible | range `CHECK`s | 8 |
| A retried pass execution write does not duplicate | MongoDB unique index on `(pass_id, started_at)` | 19.3 |

### The station overlap constraint

```sql
reservation tstzrange GENERATED ALWAYS AS
    (tstzrange(reserved_from, reserved_to, '[)')) STORED

CONSTRAINT passes_no_station_overlap EXCLUDE USING gist (
    station_id WITH =,
    reservation WITH &&
) WHERE (status IN ('pending_approval', 'approved', 'executing'))
```

The range is half-open, so a pass ending exactly when the next begins does not
conflict. Only live statuses hold the resource: `rejected`, `cancelled`,
`completed`, `failed` and `missed` rows stay for history but release the slot.

A pending request holds the reservation, which is what makes the conflict
message in spec.md section 15 possible.

## 4. Versioned rather than mutated

Two tables append instead of updating, because history has to stay truthful.

**`scheduling_configs`** — a Root change inserts a new row with an
`effective_from`. spec.md section 13.6 requires a delay before a newly chosen
lead time may be used, and section 13.8 requires existing approved passes to
keep the terms they were accepted under. Each pass stores the
`scheduling_config_id` it was built from.

The `effective_from` value is computed and enforced by the Server in V7: the
required delay depends on the *previous* configuration's lead time, which no
row-level check can see.

**`tle_records`** — refreshes append. The newest row by epoch is the current
TLE and also the last known-good one when providers fail. A pass records the
`tle_record_id` it was planned from, so a later refresh cannot rewrite the
orbital data a historical pass was built on.

## 5. MongoDB collections

| Collection | Indexes |
|---|---|
| `pass_executions` | unique `(pass_id, started_at)`; `(station_id, started_at desc)` |
| `worker_telemetry` | `(worker_id, recorded_at desc)`; TTL on `recorded_at`, 14 days |

The TTL index keeps an unattended station from filling its disk with heartbeat
documents.

## 6. Filesystem layout

```text
<recordings_root>/<station_id>/<pass_id>/     directories 0700
<pipelines_root>/<pipeline_id>.json           files 0600
```

Identifiers are validated against `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$` and the
resolved path is confirmed to stay under its root, so a crafted id cannot
traverse out (spec.md section 21). Owner-only permissions matter because
recordings may belong to private passes.

## 7. Redis session keys

```text
aagasa:session:<sha256(token)>  ->  JSON {user_id, role, created_at}, with TTL
```

The key holds a hash of the token, not the token, so a keyspace dump does not
yield usable credentials. Login, TTL policy and role checks are V3.

## 8. Applying the schema

Migrations are an explicit operational step, never a side effect of starting
the Server (RULES.md section 18).

```sh
export AAGASA_POSTGRES_URL=postgres://...
export AAGASA_MONGO_URL=mongodb://...
make migrate                            # or:
go run ./cmd/aagasa-migrate up
go run ./cmd/aagasa-migrate mongo-init
go run ./cmd/aagasa-migrate version
go run ./cmd/aagasa-migrate down-to 0
```

Both bootstrap steps are idempotent.

## 9. Deviations from the plan.md V2 list

Three consolidations, each to avoid structure with no current purpose
(RULES.md section 9). Flagged for confirmation.

**No `roles` or `permissions` tables.** spec.md section 17 fixes exactly three
roles with fixed authority. A join table with no dynamic permissions would be
structure without a requirement. Role is an enum on `users`. If per-permission
grants are ever wanted, that is a migration.

**One `passes` table, not `pass_requests` plus `scheduled_passes`.** spec.md
section 14 describes a single lifecycle from `PENDING_APPROVAL` onward. Two
tables would duplicate every column and need synchronising.

**Station configuration is split by concern** rather than one blob:
`stations` for identity and location, `station_band_configs` per RF band,
`station_hardware_configs` for rotator and SDR, `scheduling_configs` versioned
separately because only that part needs history.

## Deleting a user (migration 00006)

`passes.requested_by` is `ON DELETE RESTRICT`: an account that requested a
pass cannot be deleted, because the pass and its history outlive it.

`audit_records.actor_user_id` has **no** foreign key. It once had one with
`ON DELETE SET NULL`, which fought the trail's append-only trigger and made
every account that had ever logged in undeletable. Keeping the raw identifier
is also the better audit behaviour: the record still says who acted, even
when that account is gone.
