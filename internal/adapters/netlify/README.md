# Netlify

Pure response-header interpretation for `X-Nf-Request-Id`, `Server` and `Cache-Status`.
A nonempty request ID or a case-insensitive exact `Server: Netlify` selects the provider. In the last cache entry, lowercase `hit` and `fwd=` directives are processed in order; the last recognized directive wins.

`HeaderFacts` returns a newly owned fact map and `true` when recognized, or
`nil, false` otherwise. It does not mutate the input. `probe.EdgeFacts` owns
the cross-provider first-match order; adapters do not call one another.
Shared short-label/cache-vocabulary rules live in `edgeheaders`.

These labels are peer-reported metadata. They do not authenticate the provider,
verify routing, or establish the health of an origin or application. The HTTP
probe owns transport, response evidence and withholding labels after a private
request. This package makes no API calls and requires no new configuration.

Run `go test ./internal/adapters/netlify`. Tests use synthetic headers;
`probe.TestEdgeFactsCompatibilityBeforePackageSplit` and
`probe.FuzzEdgeFactsCompatibility` compare with the frozen parser from commit
`599c890`. This baseline identifies the Topophany behavior being preserved,
not execution against a particular vendor release. Add new response shapes
and their evidence limits here when extending this platform.
