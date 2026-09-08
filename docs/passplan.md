# PassPlan (V8)

The executable instruction the Server hands the Worker.

Contract: `proto/aagasa/worker/v1/passplan.proto`.
Generator: `server/go/internal/passplan`.

---

## The rule that shapes it

The Worker may be running through a Server outage when a pass comes up, so a
plan carries **everything execution needs and nothing that requires a call
back** (spec.md section 13.2). A custom SatDump pipeline therefore travels
inline as JSON, not as a reference to fetch.

## Contents

| Field | Notes |
|---|---|
| `plan_version` | Lets a Worker refuse a plan it cannot fully understand |
| `pass_id`, `station_id`, `worker_id` | Identity for reconciliation |
| `norad_id`, `satellite_name` | Canonical satellite identity |
| `elements` | The exact TLE lines, epoch, and the `tle_record_id` they came from |
| `aos`, `tca`, `los`, `max_elevation_degrees` | Execution timing |
| `recording_start`, `recording_end` | Already widened by the recording margins |
| `band`, `radio` | Source, frequency, sample rate, gain, PPM, bias tee |
| `recording_mode`, `pipeline` | What to produce and how |
| `track` | Pointing timeline, AOS to LOS inclusive, 5s spacing |
| `generation`, `generated_at` | Content hash and when it was built |

### Deliberately absent

**Scheduling buffers.** The pre- and post-pass buffers are how the Server
protects the station as a resource; the Worker only needs the recording
window. Sending both would invite the Worker to use the wrong one.

**Rotator serial port and baud.** Worker-local hardware configuration
(worker-spec section 5). Sending it from the Server would create two sources
of truth for the same setting.

## Resolving the track-versus-TLE tension

`spec.md` section 2.2 and worker-spec section 10 say the Worker executes a
Server-supplied pointing timeline. `spec.md` section 11.3 and worker-spec
section 22 say the Worker refreshes TLEs by itself during a Server outage.
Taken literally these conflict: if pointing is fixed at plan time, a Worker
TLE refresh changes nothing.

This was flagged in V0 and is resolved in the schema as follows:

> The plan carries **both** the pre-computed pointing timeline **and** the
> orbital elements it was computed from. The Worker executes the timeline
> normally. Through a long outage it may recompute **pointing only** from
> fresher elements. It must never recompute timing, re-select a pass, or
> schedule.

That keeps the Server the sole scheduler while making the Worker's offline
TLE refresh meaningful. It is recorded in the proto comments so the
constraint travels with the contract.

## Determinism

`generation` is SHA-256 over the marshalled plan with `generated_at` and
`generation` itself cleared. The same pass, elements, station and
configuration always produce the same value, so:

- regenerating an unchanged plan is a no-op and does not churn storage
- a Worker can compare one string to know whether its desired state moved
- `PassPlanSet.generation` hashes the members into one value for the whole
  desired state (spec.md section 6.2)

## Immunity to TLE refresh

A plan is built from `pass.tle_record_id`, not from "the newest TLE". Storing
newer elements for the same satellite leaves every existing plan
byte-identical. Verified live: after inserting a newer TLE, the generation
hash and the referenced `tle_record_id` were unchanged.

## Lifecycle

A plan is generated when a pass is **approved**, including by Root override,
and stored in `pass_plans`. Generation failure fails the approval: a pass left
approved with no plan would be one the Worker silently never runs.

Only approved passes appear in the desired state, so cancelling a pass removes
it from that set without deleting the stored plan, which stays for audit.

## Inspection

`GET /api/passes/{id}/plan` (Admin or Root) decodes the stored plan, including
the full pointing timeline. `404 no_plan` when the pass is not approved.

## Verified live

A real pass over the SJCIT station, from a real CelesTrak TLE, through the
real prediction service:

```text
plan_version    : 1
encoded bytes   : 3654
satellite       : 25544 ISS
AOS/TCA/LOS     : 18:11:27 18:14:26 18:17:26
recording window: 18:11:17 -> 18:17:41     (10s pre-roll, 15s post-roll)
band / mode     : RF_BAND_VHF / RECORDING_MODE_PROCESS
radio           : rtlsdr 137100000 Hz, 2048000 sps, gain 40
track points    : 73     (10.0 deg -> 10.0 deg, azimuth 189.6 -> 60.9)
```
