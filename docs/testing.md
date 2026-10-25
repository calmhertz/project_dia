# Testing Aagasa

Four layers, from fastest to most real. Each one exists because the layer
below it cannot answer the same question honestly.

Start with `scripts/install-dependencies.sh --check`, which reports what is
missing without installing anything.

| Layer | Command | Needs |
|---|---|---|
| Unit and component | `make test` | nothing |
| Store and API integration | `make test-integration` | PostgreSQL, Redis |
| Whole-system integration | `make env-up && make integration-test` | podman |
| Failure recovery | `make drills` | a running environment |
| Hardware | see below | a G-550 and an RTL-SDR |

---

## A complete environment, without hardware

```sh
make env-up          # databases, schema, services, station, fixtures
make env-status
make env-down        # stop the services, keep the data
make env-reset       # stop everything and delete the data
```

`scripts/dev-env.sh` brings up PostgreSQL, Redis and MongoDB in containers,
applies the schema, starts the prediction service, the Server, a Worker and
the built web client, and seeds fixtures through the REST API.

The Worker runs against **simulated hardware** by default, so the whole
system works on a machine with nothing attached:

- `scripts/simulated-hardware/rotator.py` opens a pseudo-terminal and answers
  G-550 commands on it. The Worker opens a real serial device and cannot tell
  the difference; every commanded angle is logged.
- `scripts/simulated-hardware/satdump` accepts the two invocations the Worker
  makes, writes output of a plausible size, and runs until it is stopped.
- `scripts/simulated-hardware/rtl_test` reports one device in the real format.
- `scripts/simulated-hardware/pipelines/` is a fixed pipeline set, so what the
  station reports is identical on a machine with SatDump installed and one
  without.

### Fixtures

`aagasa-seed` creates them **through the REST API**, so seeding exercises the
paths an operator uses and cannot produce a state the API would refuse. It is
idempotent, and it walks each account through the forced first password
change, so the fixtures are usable and the rule is exercised on every run.

| Account | Role | Password |
|---|---|---|
| `root` | root | `development-root-password` |
| `approver` | admin | `approver-development-password` |
| `operator` | user | `operator-development-password` |

Plus a station at SJCIT's coordinates with both bands configured, and the ISS,
NOAA 19 and METEOR-M2 2 with real orbital data.

These are development credentials. They are printed on every run and must
never exist anywhere real.

### Several environments at once

Each name gets its own containers, ports and data, so a test run cannot
disturb a developer's:

```sh
AAGASA_ENV_NAME=test AAGASA_PORT_OFFSET=100 make env-up
AAGASA_PORT_OFFSET=100 scripts/integration-test.sh
AAGASA_ENV_NAME=test AAGASA_PORT_OFFSET=100 make env-reset
```

## The integration test

`scripts/integration-test.sh` drives the REST API as all three roles and
checks the control plane end to end: sign-in, the role boundaries, the station
being online and reporting its pipelines, prediction, requesting, the refusal
of a decode with no pipeline, approval by an admin and not by the requester,
the generated plan carrying a pointing timeline and the configured frequency,
the station taking that plan, visibility, and cancellation.

It does **not** assert execution. When a satellite next flies overhead is not
something a test can arrange, and waiting hours is not a test. Execution is
covered by the Worker's own tests with stub hardware, and for real by the
hardware path below.

## Failure drills

`scripts/failure-drills.sh` takes the running system apart: it stops the
database, kills the Server, kills the Worker, and checks each one recovers.
See docs/hardware.md.

## The hardware path

The one thing none of the above can tell you is whether the station actually
works. With a G-550 and an RTL-SDR attached:

```sh
AAGASA_HARDWARE=real make env-up
cd worker && uv run aagasa-hardware status
```

Then request a pass on a satellite that is due overhead, approve it, and watch
the Worker log through the pass. What to expect, and what each step should
print, is in docs/hardware.md.

This path is deliberately kept: simulated hardware proves the software is
correct, and only real hardware proves the station is.
