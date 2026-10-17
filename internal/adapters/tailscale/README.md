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
