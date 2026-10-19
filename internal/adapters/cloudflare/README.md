# Cloudflare

Pure interpretation of Cloudflare response headers and redirects: `Cf-Ray`,
`Cf-Cache-Status`, `Cf-Mitigated`, `Server-Timing` and Access login redirects.
A nonempty ray header selects the provider, even when its labels cannot be
retained. The POP is the suffix after the last hyphen. `Cf-Mitigated` is kept
only as the closed value `challenge`, which Cloudflare sets on challenge
pages; other Cloudflare products set other values, such as `guardrails`, and
this adapter drops them.
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

`ServerTimingFacts` reads Cloudflare's `cfL4` entry from `Server-Timing`,
which some responses carry: a query string in its `desc` parameter with the
colo's own view of the connection (TCP or QUIC) that carried the response.
The first entry named exactly `cfL4` across all header values is read, quoted
or unquoted, with `url.ParseQuery`; the combined values are bounded at 2 KiB
of parsed bytes and a larger set yields nothing. Cloudflare's Rules changelog
([2026-04-01](https://developers.cloudflare.com/rules/changelog/)) references
`Server-Timing: cfL4`, and the rules field reference for
[`cf.timings.client_tcp_rtt_msec`](https://developers.cloudflare.com/ruleset-engine/rules-language/fields/reference/cf.timings.client_tcp_rtt_msec/)
documents that field, not the header's fields, in milliseconds. No official
field layout or unit specification for the header itself was found. The
header's `rtt` and `min_rtt` are read as microseconds from the live capture in
[`testdata/2026-10-19/`](testdata/2026-10-19/README.md): a fresh check read
`time_connect=0.0039 s` while the header read `rtt=1008`; as milliseconds that
would be a thousand times the completed handshake, as microseconds it is
1.008 ms. They are recorded as `edge_rtt_ms` and `edge_min_rtt_ms` in whole
milliseconds rounded to the nearest. `retrans` and `lost` become counts, and
`proto` only `TCP` or `QUIC`. Integers are one to twelve ASCII digits parsed
strictly; a malformed field never discards the others, and repeated keys keep
the first value. `cid`, `ts`, `sent_bytes`, `recv_bytes`, `cwnd`,
`delivery_rate`, `x` and everything else are dropped. These numbers and the
protocol word cross the secret boundary; POPs and other free labels do not.
The probe reads the entry only after `edge_provider` is `cloudflare`, and the
views append `· edge rtt N ms` last, showing a reading that rounded to 0 as
`<1 ms`. The entry is optional: `speed.cloudflare.com` is a Workers endpoint,
not a customer proxied zone, and spot checks of `www.cloudflare.com`,
`one.one.one.one` and `developers.cloudflare.com` found no `cfL4`. Presence on
an ordinary proxied zone is unverified; the spot checks suggest the entry is
not on every zone, but that is inferred rather than documented.

`HeaderFacts` returns a newly owned fact map and `true` when recognized, or
`nil, false` otherwise; `AccessFacts` returns a one-word map or `nil`, and
`ServerTimingFacts` returns nil when there is no exact `cfL4` entry or no
usable field, never any header text. None mutates its input.
`probe.EdgeFacts` owns the cross-provider first-match order; adapters do not
call one another. Shared short-label/cache-vocabulary rules live in
`edgeheaders`.

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
