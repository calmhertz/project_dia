# Server

Authoritative control plane. See `server-spec.md` and `../spec.md`.

- `go/` - Go control plane. Module `aagasa`, `go.mod` at `server/go/`.
  Owns REST, client WebSocket, RBAC, scheduling, approvals, PassPlan
  generation, worker gRPC, recording metadata, audit.
- `python/` - internal Skyfield/SGP4 prediction service, uv project, reached
  from Go over gRPC. Computation only; it owns no user, session, or
  scheduling state.

Both directories are empty until V1.
