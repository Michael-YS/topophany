# CloudFront

Pure response-header interpretation for `X-Amz-Cf-Pop` and `X-Cache`.
A nonempty POP header selects the provider. The first word of the last comma-separated cache entry is its reported cache status.

`HeaderFacts` returns a newly owned fact map and `true` when recognized, or
`nil, false` otherwise. It does not mutate the input. `probe.EdgeFacts` owns
the cross-provider first-match order; adapters do not call one another.
Shared short-label/cache-vocabulary rules live in `edgeheaders`.

These labels are peer-reported metadata. They do not authenticate the provider,
verify routing, or establish the health of an origin or application. The HTTP
probe owns transport, response evidence and withholding labels after a private
request. This package makes no API calls and requires no new configuration.

Run `go test ./internal/adapters/cloudfront`. Tests use synthetic headers;
`probe.TestEdgeFactsCompatibilityBeforePackageSplit` and
`probe.FuzzEdgeFactsCompatibility` compare with the frozen parser from commit
`599c890`. This baseline identifies the Topophany behavior being preserved,
not execution against a particular vendor release. Add new response shapes
and their evidence limits here when extending this platform.
