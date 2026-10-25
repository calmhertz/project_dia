# Aagasa

Satellite ground-station automation. A Flutter client, a Go control plane, and
a Python edge executor that points a Yaesu G-5500 rotator and records with an
RTL-SDR through SatDump.

The station keeps working when the network does not: the Worker holds its
approved schedule locally and executes passes through a Server outage, then
reconciles when the Server returns.

```text
Client (Flutter)  --REST-->  Server (Go)  --gRPC-->  Worker (Python)
                                  |                       |
                                  +--gRPC--> prediction    +--> rotator, SDR, SatDump
                                             (Python)
```

## Getting started

```sh
scripts/install-dependencies.sh --check   # see what is missing
scripts/install-dependencies.sh           # install it
make test                                 # every component, no databases needed
scripts/dev-env.sh up                     # a complete environment
```

`dev-env.sh` brings up the databases, applies the schema, starts every service
and seeds fixtures. The Worker runs against **simulated hardware** by default,
so the whole system works on a machine with nothing attached. Sign in at
<http://127.0.0.1:5001>; the credentials are printed when it finishes.

With a rotator and a dongle attached:

```sh
AAGASA_HARDWARE=real scripts/dev-env.sh up
```

## Layout

| Path | What it is |
|---|---|
| `server/go` | Control plane: REST for the client, gRPC for the station |
| `server/python` | Pass prediction over gRPC (Skyfield/SGP4) |
| `worker` | Edge executor: rotator, SDR, SatDump, durable local schedule |
| `client` | Flutter application, dark Material 3 |
| `proto` | Shared gRPC contracts; generated code is not committed |
| `deploy` | Containerfiles, Kube Play YAML, Quadlet units, provisioning |
| `scripts` | Development environment, tests, drills, acceptance |
| `docs` | How it is built and why |

## Documentation

| Document | Covers |
|---|---|
| [spec.md](spec.md) | The global contract. Authoritative. |
| [plan.md](plan.md) | The phase plan, V0 to V19 |
| [RULES.md](RULES.md) | Engineering rules |
| [docs/architecture-baseline.md](docs/architecture-baseline.md) | What was built, what broke, and why each decision was made |
| [docs/api.md](docs/api.md) | The REST surface |
| [docs/testing.md](docs/testing.md) | Four layers, from unit tests to real hardware |
| [docs/acceptance.md](docs/acceptance.md) | The V1 acceptance run |
| [docs/hardware.md](docs/hardware.md) | G-5500, RTL-SDR, SatDump, running a pass |
| [deploy/README.md](deploy/README.md) | Deploying, backing up, upgrading, rolling back |

## Testing

```sh
make test                # unit and component, nothing external
make test-integration    # against real PostgreSQL and Redis
make integration-test    # the whole API against a running environment
make drills              # take the system apart, check it recovers
make acceptance          # the V1 contract scenarios
```

## Status

V0 through V19 are complete. 269 Go tests, 158 Worker, 104 client, 39
prediction, plus 28 acceptance checks, 22 integration checks and 13 failure
drills.

Two things are outstanding, and are recorded rather than hidden:

- **A pass on real hardware.** Both devices are attached and respond, and a
  full pass has run end to end on simulated hardware. Tracking and recording a
  real satellite has not happened yet, so V4 and V10 are unsigned.
- **Realtime updates and notifications** (client-spec sections 21 and 31).
  Specified, assigned to no phase, not built; the client polls instead.

Smaller known gaps are listed at the end of [docs/acceptance.md](docs/acceptance.md).

## Security

Passwords are Argon2id. Sessions are bearer tokens in Redis, never cookies.
The Worker authenticates with a shared secret over TLS. Recordings are reached
only through the pass that produced them, so a leaked link is refused. The
audit trail is append-only and the database enforces it.

Never commit a secret: `deploy/README.md` explains how they are injected, and
`.gitignore` is written to catch the obvious mistakes.
