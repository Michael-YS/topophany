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
