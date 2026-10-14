# Contributing

Keep one binary and one versioned model. Preserve the separation between source,
requested route, runtime-reported choice and actual response evidence. Never
turn a monitor-side permission/API error into a target outage.

Run `go fmt ./...`, `go vet ./...`, `go test -race ./...` and the complete
`scripts/verify.py` suite before publishing a release. Add a named regression
case to [SCENARIOS.md](docs/SCENARIOS.md) for every semantic bug. Tests must use temporary
files, local listeners and synthetic credentials. A mocked adapter must never
be described as a production deployment test.

Use bounded contexts/reads/queues, do not log raw upstream errors or secrets,
and keep configuration changes explicit. Adding an outbound protocol must not
introduce silent direct fallback. Adding an API GET endpoint still requires a
side-effect review. Do not add remote execution permissions to viewer/worker
roles. Keep the original observation timestamp during replay.

No third-party runtime dependencies are currently required. A future storage
or TUI library change must retain the wire contract, migration/recovery tests,
cross-builds and resource-budget behavior. JSON topology is the sole writable
configuration authority in version 0.2.
