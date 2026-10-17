# Tailscale

Collects the local `tailscale status --json` report through the probe's bounded
command executor. Configuration selects a peer by key, ID, hostname, DNS name
or Tailscale IP. This package owns parsing, peer matching and validation.

An online but idle peer remains online. A reported direct endpoint or preferred
DERP region is a runtime hint, not proof of an application's transport. An
offline peer, stopped backend or absent peer produces an unknown observation;
local command and permission failures are monitor errors.

Tests use synthetic command output and do not invoke a real Tailscale daemon.
Run `go test ./internal/adapters/tailscale`.

Stable node IDs and public keys take precedence over IP addresses and names.
DNS names are compared without case or a trailing dot; IPv6 addresses are
normalized. Duplicate best matches report `ambiguous_peer`. Missing `Online`
reports unknown rather than offline. Backend login, machine authorization,
startup and stopped states have distinct codes. `PeerRelay` is recognized
separately from direct endpoints and DERP preferences. These remain reports.

Status JSON has a 2 MiB limit, at most 4096 entries per container and 32768
aggregate containers. Unknown fields remain supported within those budgets.
Versioned synthetic fixtures and their provenance live in `testdata/`.

`adapter.tailscale_diagnostic` optionally accepts `disco` or `tsmp`; it is
disabled when omitted. It requires `timeout_seconds >= 4` and runs exactly one
`tailscale ping --c=1 --until-direct=false --timeout=2s` to an IP from the
uniquely selected peer, with `--tsmp` for TSMP. It never resolves a hostname.
The outer check timeout also bounds the status query and command lifetime.
DISCO can negotiate a different transport, so enable it only when that active
traffic is wanted. Its reply records direct, DERP or peer relay as a diagnostic
hint. TSMP bypasses the peer's host IP stack and its output does not identify
the transport, so its diagnostic route remains unknown. Neither reply proves
application reachability or promotes the observation into connectivity evidence.
Timeouts stay unknown; malformed output or local command failures are monitor
errors. Raw command output and hostnames are never exported by the diagnostic.
