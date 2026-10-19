# Cloudflare

Pure interpretation of Cloudflare response headers and redirects: `Cf-Ray`,
`Cf-Cache-Status`, `Cf-Mitigated` and Access login redirects. A nonempty ray
header selects the provider, even when its labels cannot be retained. The POP
is the suffix after the last hyphen. `Cf-Mitigated` is kept only as the closed
value `challenge`, which Cloudflare sets on challenge pages; other Cloudflare
products set other values, such as `guardrails`, and this adapter drops them.
`AccessFacts` reads the status and `Location` of a response. Cloudflare
documents a browser request without an Access session as a `302` to
`<team>.cloudflareaccess.com/cdn-cgi/access/login/<host>?kid=…&redirect_url=…`;
a failed service-token authentication returns `401` or `403` instead, with no
redirect to recognize. The adapter accepts `302`, `303` or `307` and keeps
only `edge_access: login_redirect` when the location is an absolute URL
without user information whose host, lowercased, ends in
`.cloudflareaccess.com` with at least one character before the suffix. The
scheme is not restricted and `Hostname` strips the port, even an out-of-range
one, so `.team.cloudflareaccess.com` and `..cloudflareaccess.com` pass: this
follows the documented host suffix, not the team-name syntax. Malformed,
opaque, relative, lookalike and control-bearing locations yield nothing.
Nothing else of the location is kept, because it carries a key ID and the
redirect target.
The views append `· Access login` when the response also names the edge. Both
the mitigation and Access words cross the secret boundary while the POP does
not.

`HeaderFacts` returns a newly owned fact map and `true` when recognized, or
`nil, false` otherwise; `AccessFacts` returns a one-word map or `nil`. Neither
mutates its input. `probe.EdgeFacts` owns the cross-provider first-match order;
adapters do not call one another. Shared short-label/cache-vocabulary rules
live in `edgeheaders`.

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
