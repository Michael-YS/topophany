# Software and platform adapters

Each supported software integration owns a directory here, including its
configuration validation, runtime parsing, tests and compatibility notes.
Edge response-header interpreters also own one directory per platform, with
their own parsing, tests and contribution notes.
Adapters depend on `model`, `secure` and the small shared contract in this
package. They must not import `probe`, `config`, the collector or the UI.

`probe` supplies explicit HTTP transports and bounded command execution. It
owns scheduling, cancellation, elapsed time, the observation envelope and
error classification. Adapters fill software-specific facts and policy
outcomes. Keep runtime reports distinct from measured application responses.

Cloudflare, Vercel, CloudFront, Fastly and Netlify currently interpret headers
from existing HTTP checks. Their `HeaderFacts` functions make no network calls
and do not need separate check kinds or configuration validators. Shared
reported-label bounds live in `edgeheaders`; `probe.EdgeFacts` retains the
explicit first-match order: Cloudflare, Vercel, CloudFront, Fastly, Netlify.
The HTTP probe still owns secret-response filtering. Provider branding remains
in `view`, and generic `via` evidence attribution remains in `engine`.

Supabase currently uses the shared topology model and ordinary HTTP checks.
Add its adapter directory when introducing actual platform-specific behavior.
Future cloud API or local connector collection belongs in the relevant platform
directory with explicit typed configuration and separate evidence boundaries.

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
`probe.TestEdgeFactsCompatibilityBeforePackageSplit` and
`probe.FuzzEdgeFactsCompatibility` compare header facts with an independent,
frozen copy of `599c890:internal/probe/edge.go` in `edge_legacy_test.go`.
Its original source hash is recorded there. The post-split `edge_mitigation` fact (exempted only when `Cf-Mitigated` carries the closed challenge word) and Vercel 1.2 location corrections are explicitly exempted in the comparison harness and covered separately; provider/cache facts and other platforms still match. Keep that oracle independent of
shared production helpers; do not regenerate it to make a refactor pass.

Run `go test ./internal/adapters/... ./internal/probe ./internal/config` for
focused checks. Follow `CONTRIBUTING.md` for the full verification requirements.
