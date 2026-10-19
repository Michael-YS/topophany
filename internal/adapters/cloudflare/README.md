# Cloudflare

Pure response-header interpretation for `Cf-Ray`, `Cf-Cache-Status` and `Cf-Mitigated`.
A nonempty ray header selects the provider, even when its labels cannot be retained. The POP is the suffix after the last hyphen. `Cf-Mitigated` is kept only as the closed value `challenge`, which Cloudflare sets on challenge pages; other Cloudflare products set other values, such as `guardrails`, and this adapter drops them. It crosses the secret boundary while the POP does not.

`HeaderFacts` returns a newly owned fact map and `true` when recognized, or
`nil, false` otherwise. It does not mutate the input. `probe.EdgeFacts` owns
the cross-provider first-match order; adapters do not call one another.
Shared short-label/cache-vocabulary rules live in `edgeheaders`.

These labels are peer-reported metadata. They do not authenticate the provider,
verify routing, or establish the health of an origin or application. The HTTP
probe owns transport, response evidence and withholding labels after a private
request. This package makes no API calls and requires no new configuration.

Run `go test ./internal/adapters/cloudflare`. Tests use synthetic headers;
`probe.TestEdgeFactsCompatibilityBeforePackageSplit` and
`probe.FuzzEdgeFactsCompatibility` compare with the frozen parser from commit
`599c890`. This baseline identifies the Topophany behavior being preserved,
not execution against a particular vendor release. Add new response shapes
and their evidence limits here when extending this platform.
