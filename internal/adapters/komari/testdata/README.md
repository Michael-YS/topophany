# Komari protocol fixture

`recent_v1.6.0.json` is synthetic. It contains no monitoring data, credentials,
host addresses or identifiers taken from a deployment.

Its shape follows Komari **1.6.0**, commit
[`1b76b7ad5bc3de6529bb7f0577069b4ac241fec3`](https://github.com/komari-monitor/komari/tree/1b76b7ad5bc3de6529bb7f0577069b4ac241fec3):

- [`protocol/v2/jsonrpc.go`](https://github.com/komari-monitor/komari/blob/1b76b7ad5bc3de6529bb7f0577069b4ac241fec3/protocol/v2/jsonrpc.go)
  defines the nested CPU/RAM/disk report. Byte counters are signed 64-bit
  integers. CPU `usage` is omitted for zero; RAM/disk counters are not omitted.
- [`web/router/router.go`](https://github.com/komari-monitor/komari/blob/1b76b7ad5bc3de6529bb7f0577069b4ac241fec3/web/router/router.go)
  binds `GET /api/recent/:uuid` to the public recent-record method.
- [`web/api/client/ingest.go`](https://github.com/komari-monitor/komari/blob/1b76b7ad5bc3de6529bb7f0577069b4ac241fec3/web/api/client/ingest.go)
  supplies the authenticated UUID and server receive time.
- [`web/agent/connections.go`](https://github.com/komari-monitor/komari/blob/1b76b7ad5bc3de6529bb7f0577069b4ac241fec3/web/agent/connections.go)
  retains a one-minute recent window and returns an empty array when absent.

The tests independently assert 12.5% CPU, 37.5% memory and 20% aggregate disk
usage. Additional cases supply exact integers above 2^53, malformed and absent
values, reordered samples, ambiguous timestamps, resource limits, and
credential-reflection attempts. These fixtures are protocol tests, not a claim
of acceptance against a live Komari deployment; the opt-in official-binary
harness supplies the separate executable protocol check.
