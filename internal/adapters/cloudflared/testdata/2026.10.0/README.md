# cloudflared 2026.10.0 listener fixtures

Captured 2026-10-19 on macOS arm64 from cloudflared **2026.10.0** (built
2026-10-05T15:39:13Z), a Homebrew build. These are the connector's own local
metrics responses, not an application request.

## How it was started

No live tunnel exists in this repository, so the connector was started with a
fake locally-managed tunnel: a temporary configuration naming a tunnel UUID
and a credentials file whose `TunnelSecret` and `AccountTag` were fabricated
placeholders. cloudflared still starts its metrics listener before (and while)
registration fails, which is what let the unready shape be captured:

```sh
cloudflared tunnel --config config.yml run
# tunnel: 00000000-0000-4000-8000-000000000000
# credentials-file: a temporary file holding fake values (TunnelSecret and account tag)
# no-autoupdate: true
# metrics: 127.0.0.1:20241
```

Registration failed against the fabricated tunnel (`Failed to get tunnel`),
so `readyConnections` stayed zero. The listener was queried with:

```sh
curl -sS -D ready.headers    http://127.0.0.1:20241/ready
curl -sS -D metrics.headers  http://127.0.0.1:20241/metrics
```

The process was then stopped. Nothing in these files came from a Cloudflare
account, a real tunnel, or a live edge connection.

## Files

- `ready-503.json` — the exact `/ready` body, 88 bytes, with the generated
  connector UUID replaced by the all-zero placeholder. The live response was
  HTTP/1.1 **503 Service Unavailable**, `Content-Type: text/plain;
  charset=utf-8`, `Content-Length: 88`, no trailing newline. The body's
  `status` duplicated the HTTP status.
- `metrics.txt` — the exact `/metrics` body, 247 lines, HTTP **200**, with
  `Content-Type: text/plain; version=0.0.4; charset=utf-8;
  escaping=underscores`. It contains no connector UUID; the process and Go
  runtime families are public build and runtime counters. Counters only grew
  between the two captures (`cloudflared_tunnel_tunnel_register_fail` 2 then
  6); the file keeps the first capture.
- `ready-200.synthetic.json` — **synthetic, not observed.** It is the
  documented three-member shape with four ready connections.
- `metrics-connected.synthetic.txt` — **synthetic, not observed.** The
  allowlisted families with the documented label names, including
  `cloudflared_tunnel_server_locations{connection_id,edge_location}`,
  `cloudflared_tunnel_active_streams` and
  `cloudflared_tunnel_timer_retries`. Family names and label names come from
  Cloudflare's documentation of the connector's metrics and the observed
  `edge_location`/`connection_id` strings in the binary; the values and the
  four colos are invented.

## What was observed live

From the 503 capture (the exact body above and these exact metric lines):

```
build_info{goversion="go1.27.1",revision="2026-10-05T15:39:13Z",type="",version="2026.10.0"} 1
cloudflared_tunnel_concurrent_requests_per_tunnel 0
cloudflared_tunnel_ha_connections 1
cloudflared_tunnel_request_errors 0
cloudflared_tunnel_total_requests 0
cloudflared_tunnel_tunnel_register_fail{error="server_error",rpcName="registerConnection"} 2
```

Two details matter for the parser:

- `/ready` says `readyConnections: 0` while `cloudflared_tunnel_ha_connections`
  reads **1**. The metrics gauge is not a ready-connection count, so the
  adapter decides readiness from `/ready`, never from `ha_connections`.
- The response is JSON with `Content-Type: text/plain`, so the adapter parses
  the body and does not require `application/json`.
- With no registered connection, `cloudflared_tunnel_server_locations` and
  `cloudflared_tunnel_active_streams` were **absent** from all 247 lines, as
  were `cloudflared_tunnel_timer_retries` and the unrelated
  `cloudflared_tunnel_response_by_code`.

## What was not observed

The 200 `/ready` shape, `readyConnections` above zero, the
`cloudflared_tunnel_server_locations` family and its exact labels and values,
`cloudflared_tunnel_active_streams` and `cloudflared_tunnel_timer_retries` were
not observed live because they need a registered tunnel with at least one live
edge connection. Their shapes are taken from Cloudflare's documentation of
`/ready` and of the `cloudflared_tunnel_server_locations{connection_id,edge_location}`
family, not from this capture, and the two `.synthetic` files say so in their
names.
`connector_version` is read from `build_info{version=...}`: the observed
`2026.10.0` is nine characters, so the bound is 24 token characters, not the
eight a colo token gets.

The generated connector UUID is an identifier and is not kept here. The
fabricated `TunnelSecret`, account tag and temporary paths never appear; the
tunnel UUID above is an all-zero placeholder. Reproduce a new capture for
another version in its own `<version>/` directory with the same notes.
