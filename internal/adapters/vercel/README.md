# Vercel

Pure response-header interpretation for `X-Vercel-Id` and `X-Vercel-Cache`.
A nonempty ID selects the provider. Its first component is the reported edge POP; with more than two components, the penultimate component is the reported function region.

`HeaderFacts` returns a newly owned fact map and `true` when recognized, or
`nil, false` otherwise. It does not mutate the input. `probe.EdgeFacts` owns
the cross-provider first-match order; adapters do not call one another.
Shared short-label/cache-vocabulary rules live in `edgeheaders`.

These labels are peer-reported metadata. They do not authenticate the provider,
verify routing, or establish the health of an origin or application. The HTTP
probe owns transport, response evidence and withholding labels after a private
request. This package makes no API calls and requires no new configuration.

Run `go test ./internal/adapters/vercel`. Tests use synthetic headers;
`probe.TestEdgeFactsCompatibilityBeforePackageSplit` and
`probe.FuzzEdgeFactsCompatibility` compare with the frozen parser from commit
`599c890`. This baseline identifies the Topophany behavior being preserved,
not execution against a particular vendor release. Add new response shapes
and their evidence limits here when extending this platform.
