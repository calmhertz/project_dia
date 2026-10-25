Done. Three files created:

| File | Purpose |
|------|---------|
| `scripts/deploy-real.sh` | Single deploy script for real hardware |
| `deploy/server/.env.server` | All server config in one place |
| `deploy/worker/.env.worker` | All worker/hardware config in one place |

---

### Tomorrow at the ground station — 3 steps:

**1. Edit the two config files** (fill in `CHANGE_ME` values):
```bash
# Generate a 32-char secret once, use in BOTH files:
openssl rand -hex 32

# Edit both:
vim deploy/server/.env.server
vim deploy/worker/.env.worker
```

Critical values to set:
- `AAGASA_WORKER_SHARED_SECRET` (same in both)
- `AAGASA_POSTGRES_URL` password
- `AAGASA_ROOT_PASSWORD`
- `AAGASA_ROTATOR_SERIAL_PORT` (check `ls /dev/ttyUSB*`)
- `AAGASA_SERVER_GRPC_ADDRESS` (server IP if separate machines)

**2. Verify hardware is detected:**
```bash
scripts/deploy-real.sh --hwtest
```
Should show: `rotator: OK`, `sdr: OK <device>`, `satdump: OK <path>`, `pipelines: XX`

**3. Deploy:**
```bash
# Same machine (server + worker):
scripts/deploy-real.sh --both

# OR separate machines:
# On server machine:  scripts/deploy-real.sh --server
# On worker machine:  scripts/deploy-real.sh --worker
```

**4. Test a pass:**
- Open `http://<server-ip>:8080` → login `root` / your password → change password
- Complete first-run setup (station config, VHF/UHF freq, rotator park, etc.)
- Add a satellite → request pass → approve as Admin/Root → watch it execute

---

### Commands you'll need:
```bash
scripts/deploy-real.sh --status   # check everything running
scripts/deploy-real.sh --logs worker   # tail worker log
scripts/deploy-real.sh --stop   # clean shutdown
```

Logs go to `~/.local/share/aagasa/real/logs/`.

The Worker code already handles: rotator clamp/park, RTL-SDR probe, SatDump JSONC pipelines, capture lock (one SDR = one capture), disk pressure cleanup, offline execution + reconciliation. You're testing the **hardware integration path**, not the software logic.
