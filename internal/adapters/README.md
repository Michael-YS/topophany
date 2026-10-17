# Software adapters

Each supported software integration owns a directory here, including its
configuration validation, runtime parsing, tests and compatibility notes.
Adapters depend on `model`, `secure` and the small shared contract in this
package. They must not import `probe`, `config`, the collector or the UI.

`probe` supplies explicit HTTP transports and bounded command execution. It
owns scheduling, cancellation, elapsed time, the observation envelope and
error classification. Adapters fill software-specific facts and policy
outcomes. Keep runtime reports distinct from measured application responses.

To add an adapter:

1. Add its package, pure configuration validator and synthetic regression
   fixtures. Keep all software-specific parsing and endpoint restrictions here.
2. Add typed options in `model`; connect them to the versioned configuration,
   cloning, redaction and measurement contract as needed.
3. Wire execution in `probe/adapters.go` and `probe.Run`, configuration validation
   in `config`, and reported-evidence admission in `api`.
4. Add the check kind to the editor and synthetic preview when supported.
5. Document supported versions, evidence limits and a local test command in
   the package README. Map semantic regressions in `docs/SCENARIOS.md`.

Do not broaden the common contract for a capability used by only one adapter.
Do not add implicit registration, dynamic loading or a direct transport fallback.
HTTP GET is not sufficient proof that an API endpoint has no side effects.

`probe.TestAdapterCompatibility` compares serialized observations and measurement
contracts against fixtures captured before the package split. It ignores only
the ephemeral listener address, sampling timestamp and elapsed time.

Run `go test ./internal/adapters/... ./internal/probe ./internal/config` for
focused checks. Follow `CONTRIBUTING.md` for the full verification requirements.
