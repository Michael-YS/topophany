# Mihomo

`adapter.go` orchestrates collection, `client.go` owns response structures and
the read allowlist, `chain.go` expands selected groups and dialer proxies, and
`policy.go` implements software-specific interpretation. `config.go` validates
the typed view of the existing flat JSON adapter options.

Only `/configs`, `/proxies` and `/providers/proxies` may be read. Delay endpoints
are excluded because some can clear a group's fixed selection. The three reads
are not an atomic snapshot. A reported selection, TUN setting or delay does not
prove that an application request followed that route.

Provider expectations compare names **and order**. They cannot verify proxy
parameters. Unknown group shapes, cycles and ambiguous load balancing remain
unresolved. Credential-bearing controller responses may display only names
already declared by the topology; arbitrary returned names are withheld.

Compatibility is tested against synthetic API responses, not a claimed matrix
of live Mihomo releases. Add sanitized response fixtures and named regressions
when supporting another response shape. Never commit full proxy configurations,
tokens or real subscriber credentials.

Run `go test ./internal/adapters/mihomo ./internal/probe`.


Dynamic providers can opt into `adapter.provider_reference`, an independent
names-only publication. Keep fixed groups in `expected_providers` and list only
the rotating provider in the reference's `providers` array:

```json
{
  "token_env": "MIHOMO_TOKEN",
  "root_proxy": "daily",
  "expected_mode": "rule",
  "expected_providers": {"daily": ["dmit", "azure", "CDN"]},
  "provider_reference": {
    "url": "https://publisher.example/mihomo-members.json",
    "providers": ["edge-local"],
    "max_age_seconds": 10800,
    "token_env": "PUBLISHER_TOKEN"
  }
}
```

`token_env` and `ca_file` inside the reference are optional and independent of
the controller's credential and CA. The publisher must serve this JSON shape:

```json
{
  "schema": 1,
  "published_at": "2026-10-17T07:15:00Z",
  "providers": {"edge-local": ["edge-a", "edge-b", "edge-c"]}
}
```

Generate that document from the publisher's authoritative provider artifact,
not from the controller being checked. Publish it atomically alongside each
successful provider publication, preserving node order and updating the UTC
publication timestamp. It contains no proxy passwords or connection parameters.
The timestamp above is illustrative and must be replaced by the actual
publication time. Existing YAML provider files are not parsed as reference JSON;
the publisher needs to expose this names-only companion document.

References require HTTPS or direct literal loopback HTTP without proxy hops.
The check's explicit proxy path and bind address still apply; its controller
`resolve_ip` and `server_name` overrides do not apply to the publisher. Redirects
are refused, and controller credentials are never forwarded to the publisher.
`max_age_seconds` is required (1..604800). Documents are limited to 64 KiB,
16 providers, 256 members per provider and bounded JSON complexity. Names must
be nonempty, unique, free of control characters and at most 2048 bytes. Static
and dynamic providers must be distinct, with at most 16 combined per check.

`provider_reference.settle_seconds` (0..3600, default 120) is the window after
`published_at` in which a difference is not yet a violation: a controller loads
a new provider over a minute or two, and every publication would otherwise
report `policy / provider_membership_mismatch` while it catches up. Inside the
window a difference is `unknown / provider_reference_settling`, with
`provider_reference_settling: true` and `provider_comparison` saying
`=settling(`. Matching membership is never deferred; `0` disables the wait and
restores immediate comparison.

Facts put three times side by side: `provider_reference_published_at` from the
publisher, `provider_updated_at` from the controller's own `updatedAt` when it
reports one, and the sample's own time. A difference that outlives the window
is the drift this check exists to find; one that does not is the publication
loading.

The adapter reads the publication immediately before and after the runtime
provider read. A stable, fresh publication with different loaded members/order
still produces `policy / provider_membership_mismatch`. A publication that
changes during sampling produces `unknown / provider_reference_changed`, unless
an independent fixed policy violation is already known. Unavailable, malformed
or stale publications produce monitor errors, with no fallback to an old list
or to the controller's own membership. Publication timestamps more than 30
seconds in the future are rejected. These reads are still not an atomic
cross-system snapshot; freshness is bounded by the configured publication age.

Observations include `provider_reference_published_at` and the closed-word
`provider_reference_consistency` (`stable` or `changed`). A content fingerprint
is used only internally: an authenticated publisher could reflect its credential
in returned names, so no digest of its arbitrary text is exported to viewers.
Dynamic contents do not change the configured measurement contract, but changing
the source or freshness policy does. Dynamic names do not expand the controller
response's display allowlist. Names still cannot verify proxy parameters or an
end-to-end route. This option requires workers that support `provider_reference`;
existing checks without it keep their previous behavior.

`adapter.expected_tun` optionally asserts the reported TUN boolean. Missing
TUN state stays unknown, including when false was expected. It does not prove
that OS routes or application traffic use TUN.

`adapter.provider_match_modes` maps expected provider names to `ordered`
(default) or `set`. Use ordered mode for priority-sensitive fallback groups.
Set mode tolerates reordering but rejects duplicate members and still detects
missing/extra names. It applies to static or reference-backed expectations.
`provider_comparison` reports bounded counts and order changes without exporting
undeclared names. A publication changing during sampling is labeled unverified.
The summary is truncated at the usual fact budget for very large provider names.
