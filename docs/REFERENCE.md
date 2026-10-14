# Configuration and API Reference (Schema 1)

The complete schema is defined in Go in `internal/model/model.go`. Input configuration is strictly parsed JSON; unknown fields, multiple top-level values, and files exceeding 4 MiB are rejected. Identifiers must use stable ASCII strings; human-readable names and notes may contain UTF-8 text. Never use raw IP addresses as entity IDs.

## Entities

| Collection | Key Fields | Constraints and Semantics |
|---|---|---|
| `nodes` | `id`, `name`, `kind`, `parent`, `group`, `provider`, `notes`, `location` | Host machines and virtual environments can share IP addresses while maintaining distinct identities; `parent` hierarchy must be acyclic; `kind` is free-form, and `edge` (an anycast network in front of origins, such as Cloudflare or Vercel) and `platform` (managed hosting, such as Supabase) are drawn as places you deploy to rather than machines you run; `provider` (≤64 bytes) names who runs it; `location` is an optional object: `{"label": "Tokyo", "lat": 35.68, "lon": 139.69}` used solely for map layout (lat −90..90, lon −180..180, label ≤64 bytes); inherits parent location when omitted; location is never guessed from IP addresses |
| `endpoints` | `id`, `node`, `address`, `network`, `observer`, `via` | `network` values: `public`, `lan`, `tailnet`, `loopback`, `external`; loopback endpoints must specify a bound `observer`; `via` lists up to 8 distinct edge or proxy nodes a client reaches before `node`, client side first (see [Edges and Platforms](#edges-and-platforms)) |
| `services` | `id`, `name`, `node`, `runtime`, `endpoints`, `depends_on`, `runbook`, `notes` | Deployment host may differ from access endpoint (for example, a forwarded port on a cloud relay accessing a service inside a local network); `runtime` (≤32 bytes) says how it runs there, such as `workers`, `pages`, `functions` or `container`; `runbook` is informational only |
| `observers` | `id`, `name`, `node`, `context`, `route_expectation`, `stale_seconds` | Represents an explicit OS and network execution namespace; `route_expectation` documents intended routing rather than cryptographically verified routing proof |
| `links` | `id`, `from`, `to`, `transport`, `notes` | `from`/`to` are node IDs indicating connection initiation direction, not packet drop direction |
| `paths` | `id`, `name`, `observer`, `nodes`, `links`, `proxies`, `candidates`, `depends_on`, `opaque` | Sequential links must connect; `proxies` defines outer connection chain order; `candidates` specifies alternative failover paths rather than intermediate hops |
| `checks` | `id`, `observer`, `endpoint`, `service`, `path`, `link`, `kind`, `target` | Executed from the specified observer; defaults to `endpoint.address` when `target` is empty; each check has a measurement contract (see below) |

Hard service dependencies, parent-child hierarchies, and path dependencies must be strictly acyclic. Distinct vantage points must not share a single path definition; maintain separate path objects for `local->transit-gw-01` and `cloud-relay->transit-gw-01`. Do not directly probe an abstract `candidates` routing group; probe each candidate path individually or configure a dedicated path pointing to the active production listener.

The destination endpoint in a check template must correspond to the actual service being evaluated. Do not associate an external public egress check target with a transit gateway host endpoint and treat responses from that target as the gateway's own availability. Overriding `target` is an explicit administrative configuration; the software does not automatically discover or attest to physical asset identity.

## Edges and Platforms

A site behind Cloudflare, or an app on Vercel or Supabase, is modeled with the same entities. The edge is a node of kind `edge` (no `location`: it answers from many places); a managed platform is a node of kind `platform` that may carry the location of its region. A service deployed on one names it as `node`, with `runtime` saying how it runs; an address served through one lists it in `via`:

```json
{"id": "cf", "name": "Cloudflare", "kind": "edge", "provider": "cloudflare"}
{"id": "blog-web", "node": "origin", "address": "https://www.example.invalid/", "network": "public", "via": ["cf"]}
{"id": "worker", "name": "Short links", "node": "cf", "runtime": "workers", "endpoints": ["worker-url"]}
```

The edge answers first, so evidence is attributed along `via`. Any response (an error page included) proves that the first hop answered; the nodes behind it, the origin among them, count only an `http` response that passed its assertions and that the edge did not serve from its cache. A Cloudflare `521` therefore leaves the edge up and the origin unproven; an incident on that address says the fault lies behind the edge. A confirmed failure without any response leaves every hop on the chain affected.

HTTP probes record which edge answered from the headers edges add to their responses: `edge_provider` (`cloudflare`, `vercel`, `cloudfront`, `fastly`, `netlify`), `edge_pop` (the point of presence, e.g. `NRT` from `CF-Ray`), `edge_region` (a function region named in `X-Vercel-Id`) and `edge_cache` (e.g. `HIT`, `MISS`, `DYNAMIC`). These are what the response says, not verified routing: an origin can send the same headers. Values are reduced to short label characters.

## Probe Parameters

Defaults: `interval_seconds=60`, `timeout_seconds=8`, `stale_seconds=3*interval`, `failure_threshold=3`, `recovery_threshold=2`. The first detected failure transitions state to `degraded`; reaching `failure_threshold` consecutive failures transitions state to `down`. An established outage requires `recovery_threshold` consecutive successes to return to healthy. Stale samples, monitor errors, and unknown states reset consecutive confirmation counters; observation gaps do not accumulate across lapses.

`disabled` pauses execution and `maintenance` flags planned work; neither state executes active probes or emits actionable incident alerts. Supported ranges: `interval_seconds` in 1..86400, `timeout_seconds` in 1..300. (The 1-second interval used in test suites is not recommended for production). Probes run with random jitter, non-overlapping executions per target, and a default global worker concurrency limit of 4.

`bind_ip` specifies local source IP binding; `resolve_ip` pins dial destination IP while preserving the original HTTP `Host` header and TLS `ServerName`. These options do not guarantee bypassing host-level virtual network adapters. DNS probe target resolvers may also be pinned, while remote domain resolution over proxies depends on SOCKS/CONNECT target hostnames. ICMP requires explicit IPv4 literals and rejects `resolve_ip`. Tailscale and WireGuard adapters read local status and reject dial/bind/target overrides.

| Kind | Target / Options | Operational Meaning of Success |
|---|---|---|
| `http` | HTTP(S) URL; `http` object | Target request satisfies configured assertions; does not prove application session state, streaming protocols, or unrelated API endpoints |
| `tcp` | `host:port` | Observer established a TCP connection; does not prove application protocol health or end-to-end proxy path function |
| `tls` | `host:port`; `server_name`, `ca_file` | Complete TLS handshake with verified identity and trust chain; does not attest to custom proxy transport authentication |
| `ssh-banner` | `host:port` | Received a valid `SSH-2.0` protocol banner; does not prove host key authentication |
| `dns` | Resolver `host:port`; `dns.name`, `type=A/AAAA`, `expected_ips` | Resolver returned matching resource records within constraints; no DNSSEC validation guarantee |
| `udp-echo` | `host:port` | Target reflected the randomly generated per-probe nonce; requires an active UDP echo service |
| `icmp` | IPv4 literal | Matching ICMP echo reply received with expected identifier and sequence; requires raw socket privileges |
| `mihomo` | Controller API base URL; `adapter` object | Successfully queried runtime metadata; indicates reported state rather than end-to-end traffic success |
| `tailscale` | `adapter.peer` matching peer DNS, IP, or ID | Local Tailscale client reported peer status; idle or relay indicators do not directly prove application service status |
| `wireguard` | `adapter.interface`, `adapter.peer` (public key) | Local WireGuard status queried; older handshakes do not imply broken connectivity; private keys and PSKs are stripped |

### HTTP Probe Specifications

`http.method` supports `GET` and `HEAD` (defaults to `GET`). `status` accepts an array of expected HTTP status codes; defaults to 2xx when omitted. `contains`, `body_regex`, and `headers` define response body and header assertions. `request_headers` defines static non-sensitive headers; `Authorization` and `Cookie` headers are prohibited; bearer tokens must be referenced via `bearer_env`.

`expected_exit` accepts an array of expected IP addresses or CIDR blocks. `exit_format` supports `plain`, `trace`, and `json` (with `exit_json_key`, default `ip`). When the observed egress IP violates constraints, `outcome` is set to `policy` while connectivity facts are preserved. Timestamps and configuration revisions are bound to measurements, preventing metric mixing across target changes.

Default HTTP maximum response body size is 65,536 bytes (configurable up to 1 MiB). Exceeding this budget reports a monitor configuration error rather than target downtime. Throughput stress-testing modes are not provided; heavy payload validations should be performed via authorized external diagnostics.

### Proxy Chains and Adapters

`proxies` defines outer connection hops in execution order: `http-connect`, `https-connect`, `socks5`, or an initial `ssh` jump hop. HTTP and SOCKS credentials reference `username` and `password_env`. `https-connect` verifies outer TLS using `server_name` and `ca_file`. SSH hops specify `ssh_user`, `ssh_identity_file`, and `ssh_known_hosts` as absolute file paths, rejecting unexpanded tildes and environment variables.

A TCP check over an SSH jump hop alone returns `unknown / ssh_forward_unverified`: spawning an SSH subprocess cannot verify remote forward completion without application payload. Use TLS, HTTP, or SSH banner checks to obtain definitive protocol evidence. SSH file paths are normalized to absolute paths, rejecting quotes, control characters, and `${...}` expansions. Ambient SSH agent forwarding is disabled.

Three-hop sequence: `Local Observer -> CONNECT Ingress -> SOCKS5 Transit -> CONNECT Egress -> Target`. Complex layered proxy protocols do not need to be decomposed into native primitives: direct probes toward a dedicated local SOCKS listener, and document internal hops in `path.nodes` and `adapter.root_proxy`.

`mihomo` adapter supports `token_env`, `root_proxy`, `expected_mode`, and `expected_providers` (mapping provider names to ordered node name arrays). Mismatched runtime members or ordering emit `policy` outcomes; identical names with differing internal parameters cannot be distinguished. Unresolved routing groups, load-balancing groups without single selections, and dependency loops report unknown status. TUN status and modes reflect controller reports rather than kernel routing table inspections.

A single check supports up to 16 `expected_providers`, with provider names capped at 119 bytes without control characters to respect API record budgets. Long strings are truncated at UTF-8 byte boundaries; DNS response evidence exceeding 2,048 bytes returns an explicit check error.

## Collector Authentication

```json
{
  "schema": 1,
  "tokens": [
    {"token_sha256": "REPLACE_WITH_SHA256_FROM_TOPO_TOKEN", "role": "viewer"},
    {"token_sha256": "REPLACE_WITH_SHA256_FOR_WORKER", "role": "worker", "observer": "observer-local-01"}
  ]
}
```

The sample above illustrates schema structure and is not an active credential. `topo token` outputs a `{token, token_sha256}` pair generated using high-entropy random bytes. Never copy test tokens into production. Assign a separate worker token to each observer; viewer tokens have no write permissions. Changes to `auth.json` apply when the collector reloads (`SIGHUP`) or restarts; a reload that fails validation keeps the tokens in force.

Client and worker credential files use the following format, with separate files referencing distinct token environment variables:

```json
{
  "schema": 1,
  "active": "primary",
  "profiles": {
    "primary": {
      "project": "example-network",
      "servers": [
        {"url": "https://monitor-a.example.invalid:7447", "token_env": "TOPH_VIEW_TOKEN"},
        {"url": "https://monitor-b.example.invalid:7447", "token_env": "TOPH_VIEW_TOKEN_B"}
      ]
    }
  }
}
```

Specify either `token` or `token_env`, with up to 8 server addresses. Worker credential files must reference tokens authorized for the worker role on each server. Client read-only failover engages on connection errors, 5xx server errors, rate limits (429), or stale snapshots. Failover is refused on 401/403 authorization failures, project/observer mismatches, and malformed responses.

## Alerts

A collector started with `--alerts alerts.json` tells webhooks what it concludes. Webhook URLs are secrets, so the file is private to the collector like `auth.json` (a regular file, mode `0600` or stricter, unknown fields refused), never part of the topology that workers and viewers receive:

```json
{
  "schema": 1,
  "webhooks": [
    {"id": "phone", "format": "ntfy", "url": "https://ntfy.sh/REPLACE_WITH_A_PRIVATE_TOPIC"},
    {"id": "team", "format": "slack", "url": "https://hooks.slack.com/services/REPLACE", "events": ["incidents"]},
    {"id": "ops", "format": "json", "url": "https://ops.example.invalid/topophany", "bearer": "REPLACE", "observers": ["observer-local-01"]}
  ]
}
```

| Field | Meaning |
|---|---|
| `id` | Names the webhook in logs, which never show its URL |
| `format` | `json` (the event as data), `ntfy` (text, with a title, tags and high priority for an opened incident or a silent observer), `slack` (incoming webhook, markup escaped) or `discord` (mentioning no one) |
| `url` | HTTPS; plain HTTP only to a literal loopback address or with `"allow_insecure": true`. A query may carry the service's own token; credentials in the authority are refused |
| `bearer` | Optional; sent as `Authorization: Bearer …` |
| `events` | `incidents`, `observers` or both (the default) |
| `observers` | Optional; only what these observers saw |

There are four events: `incident.opened` and `incident.closed` when the incident ledger opens or closes a record (a recovery, an unconfirmed ending, a pause or a change, with how long it lasted), and `observer.silent` and `observer.back`. An observer is called silent once the collector has settled after starting (as long as its stalest check takes to lapse), once per silence, and called back only after it was called silent. A `json` webhook receives `{"schema": 1, "project", "collector", "event", "at", "title", "text"}` and either `incident` (the ledger record) or `observer` (its state).

Each webhook has its own queue of 64 alerts, delivered in order; when it is full, new alerts for it are dropped and logged. An alert is tried again after a failure to connect or a 408, 429 or 5xx status, six times in all with pauses of 2 to 32 seconds; any other status, including a redirect, which is never followed, drops it. Environment proxies are not used. Alerts wait only in memory: a collector that stops loses those it has not sent. Each collector with an alerts file sends its own, so give the file to one collector of a pair unless duplicates are wanted. The alerts file applies when the collector reloads (`SIGHUP`) or restarts; one that fails validation is refused with the rest of the reload.

## HTTP API Specifications

| Method / Path | Role | Description |
|---|---|---|
| `GET /healthz` | Unauthenticated | Indicates collector HTTP daemon is running; does not indicate observation freshness |
| `GET /v1/snapshot?observer=observer-local-01` | `viewer` | Returns current topology, evidence states (checks with their public contracts, nodes, endpoints, services, paths, links), observer liveness with whether each measures the current topology, and incidents; `observer` query param is optional |
| `GET /v1/config` | `viewer` | Returns deep-copied sanitized topology and revision hash |
| `GET /v1/history?check=ID&limit=100` | `viewer` | Returns the check's raw observation samples under its current contract, newest first (limit: 1..500), with the public revision and contract; `topo history` reads it |
| `GET /v1/rollups?check=ID&step=1h&span=7d` | `viewer` | Returns the check's long-term rollups under its current contract in `slots`, oldest first, with their `total`, `since` (the earliest moment the collector holds) and the public revision and contract; see [rollups](#rollups); `topo history --step` reads it |
| `POST /v1/observations` | Scoped `worker` | Ingests observation batches and optional heartbeats (up to 100 observations / 1 MiB per batch) |

Authentication requires the `Authorization: Bearer <token>` header. Batch writes require `Content-Type: application/json` (optional parameters such as `charset=utf-8` are permitted). Observation IDs follow `<observer>.<boot>.<sequence>`, where `sequence` is non-zero and `boot` is a valid identifier without dots to prevent cross-scope ID collision. `SampledAt` specifies local sample timestamp; `ReceivedAt` must be omitted or zero, and is populated by the collector.

Batches are schema 2 (`"schema": 2`). The batch's `revision` is the SHA-256 of the sender's normalized topology and only describes the sender; the acknowledgement echoes it. Each observation carries the `contract` of its check, which must equal the collector's contract for that check, or the batch is refused with `409 contract_mismatch`. A check's contract is the SHA-256 of what it measures: project, check ID, observer, kind, target (the endpoint's address when `target` is empty), timeout, `bind_ip`, `resolve_ip`, `server_name`, `ca_file`, the `http`, `dns` and `adapter` options, and the proxies of its path in wire order. Nothing else enters it: names, notes, locations, intervals, staleness and confirmation thresholds, `disabled` and `maintenance`, what the check is attributed to (endpoint, service, path, link) and every other check can change without disturbing its samples, its history or the ID of an incident it is part of. `topo validate` prints every contract. Viewers see public contracts, computed over the redacted topology like the public revision, so they permit no guess at hidden fields.

A collector also accepts a schema 1 batch (no `schema`, observations with `revision` and no `contract`) from a worker not yet upgraded, as long as its `revision` is the collector's own; such samples count until the topology changes in any way. Upgrade collectors before workers: a 0.4 collector refuses schema 2 batches, and the worker quarantines them. Snapshots are schema 2 as well. Besides the contracts, a schema 2 snapshot carries `endpoints`, the state of each endpoint from the checks that target it, and for each observer `contracts`: `current` when its executor last reported measuring what the collector counts, `outdated` when it measures an older topology (its samples of changed checks are refused until it reloads), or absent when it has not said. Workers report this in their heartbeats as a digest of their observer's contracts. A snapshot also carries `ledger`, what the collector's incident ledger remembers of the incidents in its view: the records open (`state` `open` or `recovering`), then at most 50 closed in the last day (`state` `closed`, with `closed` and `ended`: `recovered`, `unconfirmed`, `paused` or `changed`), newest first. Each record has the incident's `id`, `scope`, `summary` and failed `checks`, `since` (the earliest failure the ledger saw), `opened` (when the collector first counted it) and `previous`, the earlier records of the same incident it holds. Clients read a record as what the collector concluded, not as evidence. A schema 2 client ignores fields it does not know, so later releases can add some without breaking it; it passes over a collector serving another schema, and says so if no collector serves its own. 0.5 clients therefore read only 0.5 collectors.

HTTP `202 Accepted` indicates successful storage and returns confirmed batch count. HTTP `409 Conflict` indicates project, contract (or, for schema 1, revision), or content mismatch. HTTP `403 Forbidden` indicates unauthorized observer scope or role. HTTP `422 Unprocessable Entity` indicates sample timestamps falling outside the ingestion window. HTTP `429` and `503` are retryable. Validation errors reject the entire batch without partial commits. External custom workers must adhere to `model.Batch` and `model.Observation` field schemas and budgets; `topo once` terminal output does not imply ingestion into a collector.

Remote configuration mutation, shell execution, and administrative actions are not supported. The API serves as a clean boundary for future web or TUI frontends rather than exposing database and log files directly.

### Rollups

A collector rolls every sample it accepts up into its check's rollups (see [operations](OPERATIONS.md#storage-and-disaster-recovery)). A rollup covers `seconds` from `start`. Each sample's outcome stands from when it was taken until the next sample or until it goes stale under the topology the collector counted it with, and the rollup says how long each stood: `up_seconds` (ok), `degraded_seconds` (policy), `down_seconds` (fail) and `unknown_seconds` (error or unknown: the monitor could not tell). `unmeasured_seconds` is the rest, when no current sample stood for it; it is never up or down. Availability is `up_seconds` over the measured time, `up_seconds + degraded_seconds + down_seconds`, read together with the share of `seconds` that was measured. `samples` counts the samples taken within the rollup and `outcomes` the samples by outcome; `latency` estimates what the passing ones took (`samples`, `p50_ms`, `p95_ms` and `max_ms`) from histogram bins a quarter of a doubling wide.

`/v1/rollups` lays them out in slots of `step`: `5m`, `1h` (the default) or `1d`, aligned to UTC, over `span` before now, written as minutes, hours or days such as `90m`, `24h` or `30d` (by default a day of `5m`, a week of `1h` or 90 days of `1d`; at most 800 days and 999 steps). A collector keeps rollups five minutes wide for 48 hours, an hour wide for 32 days and a day wide after that; where they are wider than `step`, a slot is as wide as what is kept, so slots may grow wider into the past. The first slot holds the start of the span and the last ends now. `step_seconds` and `generated_at` say how it was laid out. A step or span outside these bounds is refused with `400 invalid_step` or `400 invalid_span`.

Each check in a snapshot may carry `availability`: its rollups over each `window` ending when the snapshot was generated, `24h`, `7d` and `30d`, in the fields above, with `since` when the rollups begin inside the window. A check the collector holds no rollups of has none, and a snapshot over its budget leaves them out before it leaves out last observations.
