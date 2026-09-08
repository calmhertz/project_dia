# Deployment

Rootless Podman + podman-compose, driven by a single script, per spec.md
section 24. Everything lives inside `deployment/` and needs no privileges: no sudo,
no `/var/lib`, no `/etc`. All runtime data is directory-mounted into the
containers from this one tree.

```text
deployment/
├── compose.yaml               # the whole stack: datastores, server, nginx, worker
├── .env.example               # template for deployment/.env (secrets; never committed)
├── data/                      # created and bound into the containers at deploy time
│   ├── postgres/ ...          # PostgreSQL data dir
│   ├── mongo/ ...             # MongoDB data dir
│   ├── state/ ...             # Worker state
│   ├── recordings/ ...        # authoritative recording store (Server writes)
│   ├── worker-recordings/ ... # station staging copy (Worker writes)
│   ├── pipelines/ ...         # uploaded custom pipeline JSON
│   └── backups/ ...           # backup.sh output (backup.sh <dir> to override)
├── tls/                       # created by deploy.sh; tls.crt + tls.key
├── web/                       # deploy.sh copies the built client here
├── nginx/
│   └── default.conf           # reverse proxy: web on /, API on /api
├── scripts/
│   ├── backup.sh              # PostgreSQL, MongoDB, recording manifest
│   └── restore.sh             # put a backup back
├── server/
│   ├── Containerfile          # Go control plane + schema migrator
│   └── Containerfile.prediction  # internal Python computation service
└── worker/
    └── Containerfile          # needs deployment/worker/satdump.deb present
```

## Deploying

One command per host, no sudo:

```sh
# On the server host (control plane + databases + web):
./deploy.sh server

# On the station host (ground station only):
./deploy.sh worker

# Everything on one host:
./deploy.sh both

# Stop whatever this project runs on this host:
./deploy.sh down
```

`deploy.sh` is idempotent. It builds the images it needs, creates the directory
layout, generates any secret that does not already exist, generates a
self-signed certificate if none is there, applies the schema and starts the
services. It never regenerates an existing secret: rotating the Worker's shared
secret behind its back would take the station off the air.

Two prerequisites must be satisfied before `deploy.sh server` / `both` runs:

- The Flutter web client must be built already:
  `cd client && flutter build web --release` (deploy.sh then copies the result
  into `deployment/web`)
- The Worker image installs SatDump from `deployment/worker/satdump.deb`. That file
  is not committed; download the release package for the target architecture
  and place it there before building the Worker. `deploy.sh worker` and
  `deploy.sh both` require it.

`make proto` must also have been run once after cloning: the Go server and both
Python services import the generated protobuf stubs, which are not committed.
The stubs are built into the images from your working tree, and `deploy.sh`
fails with a clear message if they are missing.

Copy `aagasa-worker-shared-secret` from the server host to the station rather
than letting each generate its own. On the station, also copy the server's
self-signed certificate to `deployment/tls/tls.crt` (the Worker trusts it for the
gRPC link).

## Rootless

The stack runs as the user who invokes `deploy.sh`:

- Podman maps container root back to that user, so nothing the containers
  write needs `chown`, and everything under `deployment/data` is genuinely yours.
- No port below 1024 is bound; the only published ports are 50000 (web),
  8080 on the loopback (health) and 9090 (gRPC, for the station host).
- The migrator and the backup/restore scripts join the compose network or use
  `podman exec`; nothing needs host networking or a privileged port.
- The Worker reaches the rotator and the SDR through `devices:` in
  `compose.yaml`; access is decided by the host user's groups (dialout), as
  set up by `scripts/install-dependencies.sh`.
- On SELinux hosts (the default on Fedora) `deploy.sh` relabels every bind
  mounted directory to `container_file_t`, because `podman-compose` drops the
  `:z` volume option when it mounts them. nginx is force-recreated on every
  deploy since `deployment/web` is rebuilt and a running container would keep
  a stale mount to the removed files. The stack runs unconfined on hosts
  without SELinux.

The only host-level setup that still needs root is the one-time preparation in
`scripts/install-dependencies.sh` (distribution packages, subuid/subgid ranges,
the dialout group, the DVB-T driver blacklist).

## Building by hand

Images build from the repository root, and are built by `deploy.sh`:

```sh
podman build -f deployment/server/Containerfile            -t aagasa-server:latest .
podman build -f deployment/server/Containerfile.prediction -t aagasa-prediction:latest .
podman build -f deployment/worker/Containerfile            -t aagasa-worker:latest .
```

## Secrets

Never bake secrets into an image or a YAML file.

`deploy.sh` writes `deployment/.env` (or reads an existing one) and the compose
stack reads secrets from it. The file is chmod 0600 and gitignored.

| Variable | Role | Meaning |
|---|---|---|
| `AAGASA_POSTGRES_PASSWORD` | server, both | Postgres datastore password |
| `AAGASA_WORKER_SHARED_SECRET` | all | Shared secret the Worker presents on gRPC; both hosts must agree |
| `AAGASA_SERVER_GRPC_ADDRESS` | worker | Server's reachable gRPC address (ignored on a `both` host) |

## Layout on disk

All paths are relative to `deployment/`. The containers read these directories
through bind mounts, so what you see on the host is exactly what they see.

| Path | Holds | Backed up by |
|---|---|---|
| `data/postgres` | The schedule, users, audit trail | `backup.sh` |
| `data/mongo` | Operational documents and history | `backup.sh` |
| `data/recordings` | Recording content served to users, `<station>/<pass>/<file>` | separately, see below |
| `data/worker-recordings` | The station's local staging copy, until retention reclaims it | not backed up; it is a cache |
| `data/pipelines` | Uploaded custom pipeline JSON | `backup.sh` manifest only |
| `tls` | Certificate and key | not backed up; regenerate or reissue |

Redis holds sessions only and is deliberately not persisted: a restored
session is worth nothing, and signing in again costs a moment.

## Backup and restore

```sh
deployment/scripts/backup.sh                # to deployment/data/backups/<timestamp>
deployment/scripts/restore.sh deployment/data/backups/20260828T140000Z
```

The backup takes PostgreSQL (the one that matters), MongoDB, and a **manifest**
of the recordings rather than their content. Recording content is large, and
the Worker's own staging copy under `data/worker-recordings` is a cache that
retention reclaims, so it is deliberately not part of a restore set: the
authoritative copy of what users can play is the Server's `data/recordings`
directory. Back that directory up on its own schedule, at whatever cadence
its size allows, and use the manifest to see what a restore is missing.

Run it from cron or a systemd **user** timer. It keeps the most recent 14 by
default (`AAGASA_BACKUP_KEEP`), because an unbounded backup directory is a
second way to fill the disk the recordings live on.

Restoring stops the Server first: putting a database back underneath a running
control plane produces a state neither of them agrees with.

The container names the scripts use by default (`aagasa-databases-postgres`,
`aagasa-databases-mongo`, `aagasa-server`) are declared in `compose.yaml` so
backup and restore keep working unchanged.

## Upgrade

```sh
deployment/scripts/backup.sh                             # first, always
podman build -f deployment/server/Containerfile        -t aagasa-server:latest .
podman build -f deployment/server/Containerfile.prediction -t aagasa-prediction:latest .
cd deployment && podman-compose up -d server nginx     # picks up the new image
curl -sk https://127.0.0.1:8080/readyz             # confirm before walking away
```

Migrations run separately and forward only:

```sh
cd deployment
podman-compose up -d postgres redis mongo
podman run --rm --network aagasa \
  -e AAGASA_POSTGRES_URL=... -e AAGASA_MONGO_URL=... \
  localhost/aagasa-server:latest aagasa-migrate up
```

`deploy.sh` runs the migration step for you, joining the named compose network
(`aagasa`) so it can reach the datastores by service name. The datastores are
their own compose services precisely so a Server upgrade does not restart them.
The Worker upgrades independently and can be left until after a pass: it
executes its stored schedule whatever the Server is doing.

## Rollback

```sh
podman tag localhost/aagasa-server:previous localhost/aagasa-server:latest
cd deployment && podman-compose up -d server
```

Keep the previous image tagged before every upgrade; that is what makes this
possible. If the upgrade included a migration, roll the schema back **first**
(`aagasa-migrate down` one step at a time, checking each), or restore the
backup taken before the upgrade. An older Server against a newer schema is not
a supported combination and will fail in ways that are hard to read.

## Network

The station is expected to sit on a trusted network with a firewall in front.
What must be reachable, and from where:

| Port | Protocol | From | Why |
|---|---|---|---|
| 50000 | HTTP | operators' browsers | the web client and, behind the reverse proxy, the REST API |
| 8080 | HTTPS | server host (loopback) | the API directly: `readyz`, migrations, backup restore |
| 9090 | gRPC over TLS | the station host only | Worker synchronisation |

The datastores are **not** published: the Server and the migrator reach them by
service name on the compose network, and backup/restore use `podman exec`. All
ports here are at or above 1024, which is part of what keeps the whole stack
rootless.

The web client and the API share one origin: nginx serves the built client on
`/` and forwards `/api` (plus `/health`, `/readyz`) to the Server, so the
browser never triggers CORS. The Server's own HTTP listener stays TLS and is
only on the loopback.

Outbound: HTTPS to Celestrak and SatNOGS for orbital data. A station with no
outbound access keeps working on its last known good elements; it simply
cannot refresh them.

## Logs

Container output goes to Podman's journal, readable with:

```sh
podman logs -f aagasa-server          # control plane
podman logs -f aagasa-worker          # ground station
```

`deploy.sh down` stops the project's containers; data under `deployment/data` is
kept.

## Worker device access

The Worker container maps the rotator serial port (`/dev/ttyUSB0`) and the
RTL-SDR USB bus (`/dev/bus/usb`) in `compose.yaml`. The container runs as root,
which rootless Podman maps to the invoking user, so what matters is the host
user's ability to open the devices: membership in the `dialout` group (the
rotator) and the DVB-T driver blacklist (the dongle), both set up by
`scripts/install-dependencies.sh`. Adjust the serial path in `deployment/.env`
(`AAGASA_ROTATOR_SERIAL_PORT`) to match the station. These mappings are
deliberately visible in `compose.yaml` rather than hidden in setup notes.

## Health

- `GET /health` - liveness, always minimal.
- `GET /readyz` - readiness; 503 until PostgreSQL, Redis, MongoDB and the
  prediction service all respond.

Both are reachable through the proxy at `http://<host>:50000/` as well as
directly.

## TLS

Both Server listeners are encrypted when `AAGASA_TLS_CERT_FILE` and
`AAGASA_TLS_KEY_FILE` point at readable material; the Server refuses to start
if only one is set, or if either path cannot be read. The Worker defaults to
TLS, so a deployment without it has to disable TLS on the Worker as well,
which puts the shared secret on the wire in clear.

The image runs as root inside, which rootless Podman maps to the invoking
user, so no uid juggling is involved; `deploy.sh` merely makes the key
owner-only and the certificate world-readable:

```sh
chmod 0600 deployment/tls/tls.key
chmod 0644 deployment/tls/tls.crt
```

`deploy.sh` does this, and generates a self-signed certificate so a fresh
station is encrypted from the first minute. The Worker trusts it via
`GRPC_DEFAULT_SSL_ROOTS_FILE_PATH` (set in `compose.yaml`). Replace it with a
real one: the Server reads whatever is at those paths when it starts, so
renewal is "replace the files, restart the service".

The nginx proxy is plain HTTP on 50000 by design for now; add TLS in
`deployment/nginx/default.conf` when the station stops trusting its own network.