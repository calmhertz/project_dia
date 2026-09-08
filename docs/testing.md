# Testing Aagasa

Unit tests for every component, plus real-hardware verification. There is no
simulated-hardware or whole-system integration harness: the system is run on a
real station and exercised manually through the client.

Start with `scripts/install-dependencies.sh --check`, which reports what is
missing without installing anything.

| Layer | Command | Needs |
|---|---|---|
| Unit and component | `make test` | nothing |
| Real hardware | see below | a G-550 and an RTL-SDR, plus a deployed stack |

## Unit tests

```sh
make test
```

Runs the Go server, Python prediction service, Python Worker and Flutter client
test suites. These need no databases, no containers and no hardware:

- `server/go` — `go test ./...`
- `server/python` — `uv run pytest -q`
- `worker` — `uv run pytest -q`
- `client` — `flutter test`

## Real hardware

The only remaining end-to-end test is the system itself running on the station.

```sh
make build                # builds the server, prediction, worker and web client
./deploy.sh both          # the whole stack, one host, real rotator and dongle
```

Then sign in at <http://127.0.0.1:50000> and operate it: create accounts,
request a pass on a satellite that is due overhead, approve it, and watch the
Worker log through the pass. What to expect, and what each step should print,
is in [docs/hardware.md](hardware.md).

`aagasa-hardware status` reports the rotator and the SDR before deploying
(`cd worker && uv run aagasa-hardware status`, needs the real devices attached);
inside the deployment, the Worker container reports them in its heartbeat
(`podman logs -f aagasa-worker`).