<h1 align="center">
  <img src="docs/assets/banner.png" alt="Topophany — the appearance of a place" width="100%">
</h1>

<p align="center"><b>From where, via which path, reaching what — and on what evidence.</b></p>

<p align="center">
  <a href="https://topophany.com"><b>topophany.com</b></a> ·
  <a href="https://github.com/WhitenWhiten/topophany/releases">Download</a> ·
  <a href="docs/GUIDE.md">User guide</a>
</p>

<p align="center">
  <a href="https://github.com/WhitenWhiten/topophany/actions/workflows/ci.yml"><img src="https://github.com/WhitenWhiten/topophany/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://pkg.go.dev/github.com/WhitenWhiten/topophany"><img src="https://pkg.go.dev/badge/github.com/WhitenWhiten/topophany.svg" alt="Go Reference"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License: MIT"></a>
</p>

**Topophany** is a self-hosted monitor for your network and the services on it, read in a terminal UI. It is one Go binary, `topo`, that you run in three roles: **workers** measure from where they are, **collectors** store the evidence, and the **TUI** shows what is wrong and where.

<p align="center">
  <img src="docs/assets/overview.png" alt="The Overview during a simulated outage: the virtual geo map, its places as cards from Tokyo to Frankfurt with the failing route drawn in amber, a status card reading 5 incidents, and the list of what needs attention" width="100%">
  <br><sub>The Overview during a simulated outage, from <code>topo preview --scenario 1</code>.</sub>
</p>

## Why Topophany

- **Evidence, not colors.** Health, evidence source and freshness are separate fields. Nothing turns green without a passing check, an unmonitored service stays `unknown`, and a cached view says so.
- **Vantage points are explicit.** Each observer measures from where it actually is. A failure seen from home is reported as a failure of that path, not as a server outage.
- **Readable at a glance.** The Overview answers *is anything wrong, and where?* with a verdict, a map of every place and path, and the short list of what needs attention.
- **One binary, no dependencies.** No CGO, no database daemon, no Node.js, and zero external Go modules.

## Try it in a minute

Install from the [releases page](https://github.com/WhitenWhiten/topophany/releases) (Linux, macOS, Windows · amd64, arm64), or with Go 1.26.9 or newer:

```sh
go install github.com/WhitenWhiten/topophany/cmd/topo@latest
```

See the whole UI with synthetic evidence, no setup needed. Press `x` to switch failure scenarios:

```sh
topo preview
```

Run a real collector on your machine, with a demo topology and fresh credentials:

```sh
topo init --dir ./demo
topo serve --config ./demo/topology.json --auth ./demo/auth.json \
  --data ./demo/collector-data --observer demo

# in another terminal
topo --credentials ./demo/credentials.json
```

## How it works

```text
  WORKERS                          COLLECTORS                  CLIENTS
  measure from where they are      keep the evidence           read it

  topo worker   (home laptop)  ─┬─▶  topo serve  (A)  ─┬─▶  topo          the TUI
  topo worker   (Tokyo server) ─┴─▶  topo serve  (B)  ─┴─▶  topo status   for scripts and agents
```

- **Workers** run the checks in a reviewed local topology file and deliver observations to one or two collectors, buffering on disk when the network is down.
- **Collectors** keep an append-only journal (fsync before acknowledging) and work out health, incidents and freshness. They remember each incident from opening to close, can post them to webhooks (ntfy, Slack, Discord or JSON), and can run checks of their own.
- **Clients** are read-only. Closing the TUI does not stop monitoring, and nothing in Topophany changes routes, proxies or services.

| Command | What it does |
|---|---|
| `topo` | Open the TUI on a remote collector, using saved credentials |
| `topo status` / `topo show TAB [ID]` | Print what the TUI shows, as text or `--json`; `status` answers in its exit status too |
| `topo login` | Save and verify a connection to a collector |
| `topo serve` | Run a collector, optionally with checks of its own |
| `topo worker` | Run checks from this machine and report them |
| `topo edit` / `topo validate` | Edit a topology file in a TUI / check it and print its revision and contracts |
| `topo preview` | Open the TUI on synthetic evidence |
| `topo init` | Create a local demo with fresh credentials |

Run `topo help` for the full list.

## Monitor your own network

1. **Describe it.** Write a topology in JSON: nodes, links, endpoints, services, paths, observers and checks. Start from [`examples/homelab.json`](examples/homelab.json), edit it with `topo edit --config topology.json`, and check it with `topo validate`.
2. **Run a collector** where your devices can reach it, with TLS: `topo serve --config topology.json --auth auth.json --data collector-data --listen 0.0.0.0:7447 --tls-cert server.pem --tls-key server-key.pem`
3. **Run a worker** on every machine you want to measure from: `topo worker --config topology.json --observer <id> --credentials worker-credentials.json --data worker-data`
4. **Look at it** from anywhere: `topo login --server https://your-collector:7447 --project <name>`, then just `topo`.

The [operations guide](docs/OPERATIONS.md) covers dual collectors, systemd units and limits, and the [security model](docs/SECURITY.md) covers tokens and transport.

## What it checks

- **Native probes:** HTTP (status, body, headers, egress IP), TCP, TLS with certificate validation, SSH banner, DNS A/AAAA, UDP echo, ICMP (IPv4).
- **Proxy chains:** HTTP CONNECT, HTTPS CONNECT, SOCKS5 and OpenSSH `-W`, chained over several hops.
- **Read-only adapters:** Mihomo, Tailscale, WireGuard.
- **Edges and platforms:** Cloudflare (including Workers and Pages), Vercel, Supabase. An answer from the edge proves the edge, not the origin behind it.

## The terminal UI

Nine tabs: **Overview**, **Topology** (with three maps: virtual geo, geo and network), **Servers**, **Services**, **Paths**, **Checks**, **Incidents**, **Observers** and **About**.

| Keys | |
|---|---|
| `1`–`9`, `Tab` | switch tabs |
| `j` / `k`, arrows | move through the list |
| `Enter` | show the evidence for the selected row |
| `/` | filter |
| `v` | switch the observer you view from |
| `m` / `z` | cycle the map / zoom it |
| `q` | quit |

The mouse works too. Colors adapt to truecolor, 256-color and `NO_COLOR` terminals. The [user guide](docs/GUIDE.md) describes every tab and map, and how to read all of it from scripts and agents with `topo status`, `topo show` and `topo history`.

## Documentation

| | |
|---|---|
| [User guide](docs/GUIDE.md) | The TUI, editing, preview mode, semantics, probes, multi-location deployment |
| [Configuration and API reference](docs/REFERENCE.md) | Every entity, probe parameter and API endpoint |
| [Operations](docs/OPERATIONS.md) | Dual collectors, storage, recovery and resource limits |
| [Security model](docs/SECURITY.md) | Credentials, transport and capability boundaries ([review](docs/SECURITY-REVIEW-2026-10-14.md)) |
| [Scenarios](docs/SCENARIOS.md) · [Acceptance](docs/ACCEPTANCE.md) | What the tests prove, and what needs checking on real hardware |
| [Changelog](CHANGELOG.md) | Release history |

## Repository layout

```text
cmd/topo        the single binary
internal/       model, config, probes, scheduler, journal, engine, API, client, outbox, view, TUI, preview
examples/       sample topologies and credential templates
deploy/         hardened systemd units for the collector and the worker
docs/           guides, reference and verification records
scripts/        acceptance and verification suites
site/           the topophany.com homepage (a separate module, not part of go install)
```

## Development

```sh
go test ./...                # unit and integration tests
go test -race ./...
python3 scripts/verify.py    # the full acceptance suite (Linux, Go, Python 3)
make site                    # re-record the homepage's TUI replay after changing the TUI
```

Building needs no module downloads. Read [CONTRIBUTING.md](CONTRIBUTING.md) and the [Code of Conduct](CODE_OF_CONDUCT.md) before sending changes, and report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE)
