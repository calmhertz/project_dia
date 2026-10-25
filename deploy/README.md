# Deployment

Podman + Podman Kube Play YAML + systemd Quadlet, per spec.md section 24.

```text
deploy/
├── scripts/
│   ├── provision.sh              # a host from nothing to healthy
│   ├── backup.sh                 # PostgreSQL, MongoDB, recording manifest
│   └── restore.sh                # put a backup back
├── server/
│   ├── Containerfile             # Go control plane
│   ├── Containerfile.prediction  # internal Python computation service
│   ├── databases.yaml            # PostgreSQL, Redis, MongoDB pod
│   ├── server.yaml               # Kube Play pod
│   ├── aagasa-databases.kube     # Quadlet unit
│   ├── aagasa-server.kube        # Quadlet unit
│   └── env.example
└── worker/
    ├── Containerfile             # needs deploy/worker/satdump.deb present
    ├── worker.yaml               # Kube Play pod, with device mappings
    ├── aagasa-worker.kube        # Quadlet unit
    └── env.example
```

## Provisioning a host

```sh
# On the server host, once the images are present:
sudo deploy/scripts/provision.sh server

# On the station host:
sudo deploy/scripts/provision.sh worker
```

It is idempotent. It creates the directory layout, generates any secret that
does not already exist, generates a self-signed certificate if none is there,
installs the Quadlet units, caps the journal, starts the services and waits
for `readyz`. It never regenerates an existing secret: rotating the Worker's
shared secret behind its back would take the station off the air.

Copy `aagasa-worker-shared-secret` from the server host to the station rather
than letting each generate its own.

## Build

Images build from the repository root:

```sh
podman build -f deploy/server/Containerfile            -t aagasa-server:latest .
podman build -f deploy/server/Containerfile.prediction -t aagasa-prediction:latest .
podman build -f deploy/worker/Containerfile            -t aagasa-worker:latest .
```

The Worker image installs SatDump from `deploy/worker/satdump.deb`. That file is
not committed; download the release package for the target architecture and
place it there before building.

## Secrets

Never bake secrets into an image or a YAML file.

`provision.sh` creates them. Doing it by hand needs one detail that is easy to
get wrong: **Kube Play's `secretKeyRef` reads a Kubernetes Secret, not a raw
Podman secret.** A secret created with `podman secret create name -` from
plain text is rejected at pod start with "not valid JSON/YAML". Create them
from a Secret manifest instead:

```sh
cat > /tmp/aagasa-secrets.yaml <<YAML
apiVersion: v1
kind: Secret
metadata:
  name: aagasa-postgres-url
data:
  value: $(printf '%s' 'postgres://aagasa:...@127.0.0.1:5432/aagasa?sslmode=disable' | base64 -w0)
YAML
podman kube play /tmp/aagasa-secrets.yaml
shred -u /tmp/aagasa-secrets.yaml
```

The Worker needs the same `aagasa-worker-shared-secret` value on its own host.

## Layout on disk

| Path | Holds | Backed up by |
|---|---|---|
| `/var/lib/aagasa/postgres` | The schedule, users, audit trail | `backup.sh` |
| `/var/lib/aagasa/mongo` | Operational documents and history | `backup.sh` |
| `/var/lib/aagasa/recordings` | Recording content, `<station>/<pass>/<file>` | separately, see below |
| `/var/lib/aagasa/pipelines` | Uploaded custom pipeline JSON | `backup.sh` manifest only |
| `/etc/aagasa/tls` | Certificate and key | not backed up; regenerate or reissue |

Redis holds sessions only and is deliberately not persisted: a restored
session is worth nothing, and signing in again costs a moment.

## Backup and restore

```sh
sudo deploy/scripts/backup.sh              # to /var/backups/aagasa/<timestamp>
sudo deploy/scripts/restore.sh /var/backups/aagasa/20260828T140000Z
```

The backup takes PostgreSQL (the one that matters), MongoDB, and a **manifest**
of the recordings rather than their content. Recording content is large and is
already the authoritative copy on this host; putting it in a nightly dump makes
the dump too big to actually run. Back that directory up on its own schedule,
at whatever cadence its size allows, and use the manifest to see what a
restore is missing.

Run it from cron or a systemd timer. It keeps the most recent 14 by default
(`AAGASA_BACKUP_KEEP`), because an unbounded backup directory is a second way
to fill the disk the recordings live on.

Restoring stops the Server first: putting a database back underneath a running
control plane produces a state neither of them agrees with.

## Upgrade

```sh
sudo deploy/scripts/backup.sh                      # first, always
podman build -f deploy/server/Containerfile -t aagasa-server:latest .
podman build -f deploy/server/Containerfile.prediction -t aagasa-prediction:latest .

sudo systemctl restart aagasa-server.service       # picks up the new image
curl -sk https://127.0.0.1:8080/readyz             # confirm before walking away
```

Migrations run separately and forward only:

```sh
podman run --rm --network host \
  -e AAGASA_POSTGRES_URL=... -e AAGASA_MONGO_URL=... \
  localhost/aagasa-server:latest aagasa-migrate up
```

The datastores are a separate unit precisely so a Server upgrade does not
restart them. The Worker upgrades independently and can be left until after a
pass: it executes its stored schedule whatever the Server is doing.

## Rollback

```sh
podman tag localhost/aagasa-server:previous localhost/aagasa-server:latest
sudo systemctl restart aagasa-server.service
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
| 8080 | HTTPS | operators' browsers | the client and the REST API |
| 9090 | gRPC over TLS | the station host only | Worker synchronisation |
| 5432, 6379, 27017 | — | nothing | bound to `127.0.0.1` by the pod |

Outbound: HTTPS to Celestrak and SatNOGS for orbital data. A station with no
outbound access keeps working on its last known good elements; it simply
cannot refresh them.

## Logs

Container output goes to journald. `provision.sh` caps it at 2 GB with a
month's retention, because an uncapped journal fills the same disk the
recordings are on.

```sh
journalctl -u aagasa-server.service -f
journalctl -u aagasa-worker.service -f
```

## Install by hand

`provision.sh` does all of this; it is spelled out here because a deployment
nobody can follow by hand is a deployment nobody can debug.

```sh
sudo cp deploy/server/databases.yaml deploy/server/server.yaml \
        deploy/server/aagasa-databases.kube deploy/server/aagasa-server.kube \
        /etc/containers/systemd/
sudo cp deploy/worker/worker.yaml deploy/worker/aagasa-worker.kube /etc/containers/systemd/
sudo systemctl daemon-reload
sudo systemctl start aagasa-databases.service
sudo systemctl start aagasa-server.service     # Requires= the databases
sudo systemctl start aagasa-worker.service
```

The Server unit `Requires=` and is `After=` the databases, so a boot brings
them up in the right order. The Worker deliberately depends on neither: it
executes an approved schedule through a Server outage, so it must start
whether or not the control plane is reachable.

Both restart on failure with a ten second pause and no start limit, so a
station that reboots while its databases are still initialising still comes
up rather than exhausting its restart budget and staying down.

The Server pod uses `hostNetwork`. It is a separate pod from the datastores so
the two restart independently, and that means `127.0.0.1` inside it would not
be the host: without host networking the database URLs cannot resolve at all.

## Worker device access

The Worker pod maps the rotator serial port (`/dev/ttyUSB0`) and the RTL-SDR USB
bus (`/dev/bus/usb`), and runs privileged to reach them. Adjust the serial path
to match the station. These mappings are deliberately visible here rather than
hidden in setup notes.

## Health

- `GET /health` - liveness, always minimal.
- `GET /readyz` - readiness; 503 until PostgreSQL, Redis, MongoDB and the
  prediction service all respond.

## TLS

Both listeners are encrypted when `AAGASA_TLS_CERT_FILE` and
`AAGASA_TLS_KEY_FILE` point at readable material; the Server refuses to start
if only one is set, or if either path cannot be read. The Worker defaults to
TLS, so a deployment without it has to disable TLS on the Worker as well,
which puts the shared secret on the wire in clear.

The Server image runs as uid **10001** and drops privileges, so the key must be
readable by that uid:

```sh
sudo chown 10001:10001 /etc/aagasa/tls/tls.key /etc/aagasa/tls/tls.crt
sudo chmod 0600 /etc/aagasa/tls/tls.key
```

`provision.sh` does this, and generates a self-signed certificate so a fresh
station is encrypted from the first minute. Replace it with a real one:
the Server reads whatever is at those paths when it starts, so renewal is
"replace the files, restart the service".
