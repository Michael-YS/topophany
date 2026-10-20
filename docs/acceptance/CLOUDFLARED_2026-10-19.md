# cloudflared connector acceptance — 2026-10-19

Workstream G adds the `cloudflared` check kind. It was verified against
cloudflared **2026.10.0** (built 2026-10-05T15:39:13Z; Go 1.27.1) installed at
`/opt/homebrew/bin/cloudflared` on macOS arm64, with a fake locally-managed
tunnel and **no live Cloudflare Tunnel**. The tree is the uncommitted
workstream G change on baseline `f92e931`.

This is a bounded acceptance record for the read-only listener paths and the
automated suite. It is not a claim of compatibility with other cloudflared
releases, with a registered tunnel carrying traffic, or with a production
deployment.

## What was run

A temporary configuration named a fabricated tunnel UUID and a credentials
file with fake `AccountTag` and `TunnelSecret` values, no ingress service but
`http_status:404`, and an explicit metrics address:

```yaml
tunnel: 00000000-0000-4000-8000-000000000000
credentials-file: /tmp/cfcap/creds.json
no-autoupdate: true
metrics: 127.0.0.1:20241
ingress:
  - service: http_status:404
```

```sh
/opt/homebrew/bin/cloudflared tunnel --config /tmp/cfcap/config.yml run
curl -sS -D ready.headers   http://127.0.0.1:20241/ready
curl -sS -D metrics.headers http://127.0.0.1:20241/metrics
```

cloudflared logged `Starting metrics server on 127.0.0.1:20241/metrics` and
then failed registration (`Failed to get tunnel`), retrying every few seconds.
The listener answered throughout; the process was stopped after the captures.
No Cloudflare API call, token or account resource was used.

Sanitized fixtures are in
[`internal/adapters/cloudflared/testdata/2026.10.0/`](../../internal/adapters/cloudflared/testdata/2026.10.0/README.md).
The connector UUID and the fabricated tunnel UUID are shown as all-zero
placeholders; the account tag, `TunnelSecret` and temporary paths appear
nowhere. The 200-ready and connected-metrics shapes are documented from the
roadmap and its Appendix A, not observed, and those two fixtures are named
`.synthetic`.

## Observed outputs

`/ready` (HTTP/1.1 503, `Content-Type: text/plain; charset=utf-8`,
`Content-Length: 88`, no trailing newline):

```json
{"status":503,"readyConnections":0,"connectorId":"<uuid>"}
```

`/metrics` (HTTP 200, `Content-Type: text/plain; version=0.0.4;
charset=utf-8; escaping=underscores`, 247 lines), allowlisted and nearby
families:

```
build_info{goversion="go1.27.1",revision="2026-10-05T15:39:13Z",type="",version="2026.10.0"} 1
cloudflared_tunnel_concurrent_requests_per_tunnel 0
cloudflared_tunnel_ha_connections 1
cloudflared_tunnel_request_errors 0
cloudflared_tunnel_total_requests 0
cloudflared_tunnel_tunnel_register_fail{error="server_error",rpcName="registerConnection"} 2
```

`cloudflared_tunnel_server_locations`, `cloudflared_tunnel_active_streams` and
`cloudflared_tunnel_timer_retries` were absent from all 247 lines with no
registered connection. `cloudflared_tunnel_tunnel_register_fail` is outside
the allowlist, so the parser reads its name and skips the line.

Three findings shaped the implementation:

- `cloudflared_tunnel_ha_connections` read **1** while `readyConnections` read
  **0**. The ready statement is the only source for readiness; the gauge is
  recorded as `ha_connections` and never decides the outcome.
- The 503 body is JSON with `Content-Type: text/plain`, so the adapter parses
  by shape and does not require `application/json`.
- The connector's own metrics endpoint documents no path prefix, and
  `Content-Type` carries `escaping=underscores`; neither changes the two fixed
  reads or the allowlisted names.

## Automated verification

Run on the same macOS arm64 host:

| Check | Result |
|---|---|
| `gofmt -l .` | empty |
| `go vet ./...` | clean |
| `go test -count=1 ./...` | all packages pass |
| `go test -race -count=1 ./internal/adapters/cloudflared ./internal/probe ./internal/config ./internal/engine ./internal/view ./internal/tui ./internal/api` | all seven pass (TUI suite 366 s) |
| `go test -run '^$' -fuzz '^FuzzReady$' -fuzztime=15s ./internal/adapters/cloudflared` | PASS, 324,469 execs, 300 interesting inputs |
| `go test -run '^$' -fuzz '^FuzzMetrics$' -fuzztime=15s ./internal/adapters/cloudflared` | PASS, 2,660,140 execs, 307 interesting inputs |
| `make site` | homepage in English, Japanese and Chinese lists cloudflared with its own locale text; browser demo rebuilt |
| `python3 scripts/test_site.py` | 12 tests OK |
| `python3 scripts/test_readme.py` | 3 tests OK |

The loopback tests use `httptest` listeners, not this connector: 200 and 503
readiness, zero-ready, garbage, oversized and foreign bodies, timeout, refused
connection, expected connections and colos, a server echoing no UUID, and the
ready-first precedence (an unready connector with a broken `/metrics` is still
`tunnel_not_ready`, while an established one with a broken `/metrics` is a
monitor error). Read order and method are asserted as `GET /ready` then
`GET /metrics`; both live fixtures are also read together. Escaped label values
are bounded, and integral Prometheus decimal/exponent counters are accepted.
The production client's no-proxy rule is tested at the builder
(`probe.TestCloudflaredClientRefusesProxies`), not through a client the adapter
was handed. A public topology of an https listener is validated with a
non-secret `ca_file` placeholder and never carries the real path.
`probe.TestAdapterCompatibility` adds a `cloudflared-ok` record beside the
frozen legacy records; none of the existing records changed.
`engine.TestCloudflaredLinkAndPublicEdgeEvidence` covers the link's
reported-up and `tunnel_not_ready` states beside a public check through the
edge, and `TestConnectorLinkExemptionIsNarrow` keeps stale, mixed, non-connector
and failed connector evidence from being read as a session report.

Review corrections in this round: `/ready` now decides before `/metrics` is
required; a 503 with broken metrics stays `tunnel_not_ready`; counters accept
the exporter's integral exponent form; a `metrics_unavailable` sample leaves
the link unknown with its own reason; and the engine shares the adapter's path
evidence constant.

## What this established and what it did not

Established: the metrics listener starts and answers `/ready` and `/metrics`
without a registered tunnel; the unready JSON and status shapes above; the
observed family names and integer values; that the connector version is nine
characters (`2026.10.0`), so the fact bound is 24, not eight; and that
`ha_connections` is not a ready count. The label *names* `connection_id` and
`edge_location` were seen as strings in the binary; the
`cloudflared_tunnel_server_locations` sample shape and its lowercase values
come from the roadmap and Appendix A, not from this capture.

Not established: a 200 `/ready` body with ready connections; any
`cloudflared_tunnel_server_locations` sample, so its label value case and
per-connection value were not observed; `active_streams`,
`timer_retries` and `response_by_code`; behavior with a registered tunnel,
ingress traffic, reconnects, or counters crossing their counter reset; any
other cloudflared release; and production rollout on a worker host. The
`expected_connections` and `expected_colos` policies were verified only against
synthetic loopback bodies.

## Remaining acceptance

Repeat this capture on a host with a registered tunnel, record one 200-ready
body and one `server_locations` sample in a new
`testdata/<version>/`, and observe a stop/restart of cloudflared through the
check to confirm `tunnel_not_ready` after one interval and counter behavior
across a restart. Other versions need their own directory and provenance note.
