# Station Hardware (V4, V10)

How the Worker drives the Yaesu G-550, detects the RTL-SDR and runs SatDump.

Code: `worker/src/aagasa_worker/hardware/`.

---

## Bring-up command

```sh
aagasa-hardware status       # what is present; exits non-zero if anything is missing
aagasa-hardware pipelines    # installed SatDump pipelines
aagasa-hardware park         # drive to the configured safe position
aagasa-hardware move 180 45  # drive to a commanded position
```

`status` is suitable as a deployment check because it exits non-zero when the
rotator, radio or SatDump is unavailable.

## Yaesu G-550

The command sequence comes from the working reference implementation, the only
source verified against real hardware:

```
W<azimuth:04d> <elevation:03d>\r      e.g.  W0180 045
park                                  W0000 000 by default
```

The port is opened per command and closed again, as the reference does, so the
Worker does not hold the device between passes. A lock serialises commands so
two writes cannot interleave on the wire.

### Ranges

Azimuth `0..359`, elevation `0..90` (spec.md section 9). Two paths:

- `move_to()` **rejects** an out-of-range angle. A caller asking for azimuth
  400 has a bug worth surfacing.
- `track_to()` **clamps**, because a real pass legitimately produces a
  slightly negative elevation near the horizon.

The configured park position is validated at construction, so an impossible
safe position is caught at startup rather than during recovery.

### Difference from the reference

`REFERENCE/project_dia/rotor.py:17-22` prints serial errors and continues,
which would let a pass proceed believing the antenna moved. Aagasa raises
`RotatorError` instead (RULES.md sections 13 and 15).

## RTL-SDR

V1 supports one radio and no generic SDR abstraction (spec.md section 10).
Detection shells out to `rtl_test -t` and parses the device list. Capture
itself is performed by SatDump, which opens the device directly.

No device attached is reported as an empty list, not an error; `check()` raises
only when the *configured* device is missing.

The station holds a capture lock, so a second concurrent capture is refused
outright: there is one radio (worker-spec section 12).

## SatDump

### Pipeline discovery

Pipeline files are read from, in order:

```
~/.local/share/satdump/pipelines
/usr/share/satdump/pipelines
/usr/local/share/satdump/pipelines
```

A user-installed pipeline overrides a system one of the same identifier. Each
file maps identifiers to definitions:

```json
{"gk2a_lrit": {"name": "GK-2A LRIT", "live": true, "work": {...}}}
```

**These files are JSONC, not JSON.** SatDump parses them with a reader that
allows comments, and the shipped files use `//` and `/* */` heavily. On the
verified installation here, 25 of 71 files are not strict JSON; parsing them
strictly yielded 81 pipelines instead of 185. `parse_pipeline_file` tries
strict JSON first and falls back to a comment-stripping parse that leaves
comment markers inside string literals alone.

A file that still will not parse is skipped with a warning rather than failing
discovery, so one bad pipeline cannot hide the rest.

### Running SatDump

Commands are always an argument list, never a shell string, so a pipeline name
or frequency can never be interpreted as a command (spec.md section 21).

```
satdump live <pipeline> <output_dir> --source rtlsdr --frequency N
             --samplerate N --gain N [--ppm_correction N] [--bias]

satdump record <output_path>  --source rtlsdr --frequency N
             --samplerate N --gain N --baseband_format ziq
```

Every run returns a `ProcessResult`: `succeeded`, `exit_code`, captured
`stdout`/`stderr` (bounded to 64 KiB), and whether the Worker stopped it on
purpose. Stopping asks politely first so SatDump can finalise its output, then
kills after a grace period.

Children start in a new session, so a signal to the Worker does not tear down
a capture before it can be finalised.

## Safe startup and shutdown

After any restart, expected or abnormal, the antenna position is unknown
(worker-spec sections 11.1 and 25).

**Startup** probes every device, then parks the rotator to establish a known
position. A missing device is reported, not raised: the Worker must keep
running and keep reporting. Every component is probed even when an earlier one
failed, so one absent device does not hide the state of the rest.

**Shutdown** stops any running capture, then parks. It is best effort
throughout, because a failure safing one device must not skip another.

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `AAGASA_ROTATOR_SERIAL_PORT` | required | No default: guessing a serial port is a hardware risk |
| `AAGASA_ROTATOR_BAUD_RATE` | `9600` | |
| `AAGASA_ROTATOR_PARK_AZIMUTH` | `0` | Validated 0..359 |
| `AAGASA_ROTATOR_PARK_ELEVATION` | `0` | Validated 0..90 |
| `AAGASA_SDR_DEVICE_INDEX` | `0` | Zero-based |
| `AAGASA_RTL_TEST_BIN` | `rtl_test` | |
| `AAGASA_SATDUMP_BIN` | `satdump` | |
| `AAGASA_SATDUMP_PIPELINES_DIR` | unset | Unset searches the standard locations |

Deployment maps `/dev/ttyUSB0` and `/dev/bus/usb` into the Worker pod; see
`deploy/worker/worker.yaml`.

## Verified, and not

Verified on this machine against real tooling: pipeline discovery against a
genuine SatDump install (185 pipelines), `rtl_test` invocation and its
no-device path, real `satdump live` execution through the process control with
argv construction, output capture and a deterministic non-zero exit, pyserial
write path via a loopback transport, and Worker startup/shutdown with hardware
absent.

**Not verified, because no G-550 and no RTL-SDR are attached to this machine:**

- Whether the G-550 physically moves to a commanded position, and how long it
  takes to slew.
- Whether `--source rtlsdr` is the correct source string for this SatDump
  build. With no device attached SatDump reports `Could not find a handler for
  source type : rtlsdr`, even though the `rtlsdr_sdr_support` plugin loads.
  That is the expected no-device failure, but it does mean the source string
  itself is unconfirmed. Confirm during real bring-up before relying on V10.
- Real capture throughput, sample rates the hardware sustains, and whether the
  stop grace period is long enough for SatDump to finalise a large recording.

---

# Pass execution (V10)

`worker/src/aagasa_worker/pass_execution.py` turns a PassPlan into station
activity. The executor from V9 still decides *when* a pass runs; this decides
*how*.

## Sequence

1. **Validate** the plan: schema version, window still open, a pointing
   timeline present, usable radio settings.
2. **Probe hardware.** A missing rotator or radio is recorded and the pass
   continues; missing SatDump fails immediately, because there is nothing to
   capture with and slewing the antenna would be pointless.
3. **Pre-position** the antenna at the first track point.
4. **Start capture** at `recording_start`, the configured pre-roll before AOS.
5. **Track** the satellite along the plan's timeline.
6. **Wait** for the post-roll to elapse.
7. **Stop** the capture and collect its result.
8. **Park**, always, in a `finally`.

## Choices worth knowing

**Pointing is skipped when the antenna would move less than one degree.** A
slow pass would otherwise stream redundant serial writes at the rotator.

**A pointing failure is reported once, not per track point.** A broken serial
link fails at every point; keying deduplication on the message text does not
work because each message names a different angle, so the count is tracked
explicitly.

**A broken rotator does not by itself fail the pass.** A recording made with a
stuck antenna is still worth keeping. A capture that never started does fail
it.

**SatDump is given `--timeout` as well as being stopped by the Worker.** The
flag is confirmed present on this build for both `live` and `record`. It means
a Worker crash cannot leave a capture running indefinitely.

**`raw_and_process` runs as live decode only.** One RTL-SDR cannot feed two
SatDump processes, and this build offers no verified way to save baseband
while decoding live. The shortfall is recorded in the execution detail rather
than silently ignored. spec.md section 19 permits this: raw plus processing is
"where the selected workflow permits it".

## Verified without hardware

The whole sequence runs against the **real** `satdump` binary with a recording
serial transport in place of the G-550:

```text
satdump: /usr/bin/satdump | pipelines: 185
rotator commands: W0180 010, W0200 020, W0220 030, W0240 040, W0180 005
capture_exit=1   problems=sdr unavailable: no RTL-SDR device detected
succeeded: False
```

That is the correct outcome with no radio attached: the antenna tracked and
parked, a real SatDump process was launched and its failure captured, and the
pass was reported failed rather than falsely completed.

## Still needs hardware

- Whether the G-550 physically tracks the commanded timeline, and whether five
  second spacing is fine enough for its slew rate.
- Whether `--source rtlsdr` is the correct source string for this build (flagged
  in V4 and still unconfirmed).
- A real capture: signal quality, sustained sample rates, real output sizes,
  and whether the 15 second SatDump grace is enough to finalise a large one.
- The V10 exit criterion, a real pass reaching `COMPLETED` with a decoded
  product.

## Running the whole stack locally

```sh
# Databases
podman run -d --name aagasa-pg -e POSTGRES_USER=aagasa -e POSTGRES_PASSWORD=aagasa \
  -e POSTGRES_DB=aagasa -p 5432:5432 docker.io/library/postgres:17-alpine
podman run -d --name aagasa-redis -p 6379:6379 docker.io/library/redis:7-alpine
podman run -d --name aagasa-mongo -p 27017:27017 docker.io/library/mongo:7

export AAGASA_POSTGRES_URL='postgres://aagasa:aagasa@127.0.0.1:5432/aagasa?sslmode=disable'
export AAGASA_REDIS_URL='redis://127.0.0.1:6379/0'
export AAGASA_MONGO_URL='mongodb://127.0.0.1:27017'
export AAGASA_WORKER_SHARED_SECRET='<at least 32 characters>'
export AAGASA_RECORDINGS_DIR="$HOME/.local/share/aagasa/recordings"
export AAGASA_PIPELINES_DIR="$HOME/.local/share/aagasa/pipelines"

cd server/go && go run ./cmd/aagasa-migrate up && go run ./cmd/aagasa-migrate mongo-init
cd server/python && uv run aagasa-prediction &     # gRPC on 127.0.0.1:9091
cd server/go && go run ./cmd/aagasa-server &       # HTTP 8080, gRPC 9090
cd client && flutter run -d chrome --dart-define=AAGASA_SERVER_URL=http://127.0.0.1:8080
```

Sign in as `root` / `toor`; the Server requires a password change before
anything else works, then Root runs first-run setup.

The shared secret is a real credential: generate one rather than reusing the
example, and never commit it.

Serving the client from anywhere other than localhost also needs
`AAGASA_ALLOWED_ORIGINS` set to that origin, or the browser blocks every call.

## Failure drills

`scripts/failure-drills.sh` takes the running system apart and checks it puts
itself back together (plan.md V16). It stops and starts processes and the
database container; it never deletes data.

```sh
# Point it at how to restart things, from a file: the drill kills by pattern
# and a command line naming them would match itself.
cat > /tmp/drill-env.sh <<'ENV'
export AAGASA_SERVER_CMD="source /path/to/env.sh; exec /path/to/aagasa-server"
export AAGASA_WORKER_CMD="cd worker && source /path/to/worker-env.sh && exec uv run aagasa-worker"
export AAGASA_WORKER_LOG=/path/to/worker.log
ENV
source /tmp/drill-env.sh && scripts/failure-drills.sh
```

It covers the database going away and returning, the Server crashing while
the Worker keeps executing, and the Worker crashing and reloading its durable
state. Hardware and provider failures are covered by unit tests instead,
where they can be provoked honestly.

## Simulated hardware

When no rotator or dongle is attached, `scripts/dev-env.sh` runs the Worker
against stand-ins that behave like the real devices: a pseudo-terminal
answering G-550 commands, a SatDump that writes plausible output, and an
`rtl_test` reporting one device. See docs/testing.md.

Use `AAGASA_HARDWARE=real` to run the same environment against the attached
hardware instead. Simulated hardware proves the software is correct; only the
real thing proves the station is.
