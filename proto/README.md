# proto

Shared gRPC contracts. Empty until V1.

Two contract surfaces live here:

- Server (Go) <-> prediction service (Python), internal computation only.
- Server (Go) <-> Worker (Python), control and state synchronization.

A `.proto` change is a change to a contract shared by two components. Per
RULES.md section 17, update the definition, every affected implementation, and
the tests together. Do not silently change the meaning of an existing field.

Generated code is not committed; see `.gitignore`.
