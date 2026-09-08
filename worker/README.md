# Worker

Edge execution plane, one physical station. See `worker-spec.md` and
`../spec.md`.

`src/` holds the Worker Python package (uv project). Empty until V1.

The Worker executes Server-supplied PassPlans and must keep running when the
Server is unreachable. It is never a second scheduler.
