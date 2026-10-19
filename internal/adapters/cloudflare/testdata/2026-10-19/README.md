# `Server-Timing` capture, 2026-10-19

- URL: `https://speed.cloudflare.com/` (HTTP/1.1, no query, no credentials)
- Captured: 2026-10-19, about 21:57 UTC, from Los Angeles (`LAX`)
- Client: `curl 8.7.1 (x86_64-apple-darwin26.0) libcurl/8.7.1 (SecureTransport) LibreSSL/3.3.6 zlib/1.2.12 nghttp2/1.69.0`, run as `curl -s --http1.1 -D - -o /dev/null https://speed.cloudflare.com/`. The response also carried `CF-RAY: a4d30a510bb2b256-LAX`, `X-Worker-Version-Id: 7ce3efef-3b3a-4cd1-9898-4bf02b198eee` and no `CF-Cache-Status`.

`server-timing.txt` is the header value plus the file's trailing newline, exactly as received, except that `cid` — a connection identifier — was replaced with sixteen zeros. No other character was changed.

```
cfL4;desc="?proto=TCP&rtt=1014&min_rtt=1012&rtt_var=290&sent=5&recv=7&lost=0&retrans=0&sent_bytes=2839&recv_bytes=496&delivery_rate=3933852&cwnd=53&unsent_bytes=0&cid=0000000000000000&ts=204&x=0"
```

## Provenance and the zone

`https://speed.cloudflare.com/` is a Workers endpoint, not a customer proxied zone: the response carried `X-Worker-Version-Id`. This capture therefore establishes the header on Cloudflare's own Workers response, and presence on an ordinary proxied zone remains unverified. In spot checks `https://www.cloudflare.com/`, `https://one.one.one.one/` and `https://developers.cloudflare.com/` sent no `cfL4`, and `https://blog.cloudflare.com/` sent an application `Server-Timing` with `rt`, `render` and `db.*` entries; the parser treats the entry as optional and ignores what it does not name.

## Unit

No official field-layout or unit specification for the `cfL4` entry's own fields was found. Two official pages discuss related data: the Rules changelog entry for 2026-04-01 (`https://developers.cloudflare.com/rules/changelog/`) says QUIC RTT and delivery-rate data "was only available via the `Server-Timing: cfL4` response header", and the rules field reference for `cf.timings.client_tcp_rtt_msec` (`https://developers.cloudflare.com/ruleset-engine/rules-language/fields/reference/cf.timings.client_tcp_rtt_msec/`) documents that field, not the header's `rtt`, in milliseconds.

The header's `rtt` is read as microseconds from this capture, not from a vendor statement. A fresh `curl -w` check at the same host read `time_connect=0.003919 s`, `time_starttransfer=0.711495 s` and `time_total=1.048704 s` while the header read `rtt=1008`. As milliseconds that would be roughly a thousand times the completed TCP handshake and comparable to the whole request; as microseconds it is 1.008 ms, which fits a same-metro anycast answer. Nanoseconds would be 1.008 µs, shorter than any TCP round trip over a real path. `min_rtt=1012` sits just below `rtt`, as a minimum does on a short-lived connection, and `rtt_var=290` is 0.29 ms, a plausible sub-millisecond variance. A second capture moments later read `rtt=948&min_rtt=919&rtt_var=280`, the same shape.
