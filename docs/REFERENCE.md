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

The edge answers first, so evidence is attributed along `via`. Any response (an error page included) proves that the first hop answered, and nothing behind it: the nodes behind it, the origin among them, count only checks that reach them without going through it. What the edge says of where a response came from, its `edge_cache` status among them, is recorded as reported labels and proves nothing, since the edge, or anything behind it, can send it whatever happened; a node reached only through the edge reads unknown and says so, and an answer through the edge is no evidence against a failure of the origin's own check. Give the origin a check of its own, not through the edge, to measure it. A Cloudflare `521` therefore leaves the edge up and the origin unproven; an incident on that address says the request did not satisfy its assertions and the failing layer is not located, because the status is a word the edge or the origin can send whatever happened. Where the status class supports a candidate the incident names it: a response carrying `cf-mitigated: challenge` is the edge's own statement that it served a challenge page and the origin was not asked for that request; a `302`, `303` or `307` redirect to a `Location` host ending in `.cloudflareaccess.com` is recorded as Cloudflare Access asking for a login, and the origin was not asked for that request (Cloudflare documents the `302` form; the adapter also accepts `303` and `307`); `403` and `429` without either may be edge policy or a challenge page rather than the origin's answer; `520` reports an empty, unknown or malformed response from the origin; `521`/`522`/`523` are the platform's own account of an origin it could not reach; `524` reports a read or write timeout after a successful origin connection; `525`/`526` report TLS to the origin failing (a handshake, or a certificate the edge does not accept); `530` reports an origin name it could not resolve or reach, a Tunnel with no live connector included; and any other `5xx` may come from either side. The origin is never declared at fault from the response alone. A confirmed failure without any response leaves every hop on the chain affected.

HTTP probes record which edge answered from the headers edges add to their responses: `edge_provider` (`cloudflare`, `vercel`, `cloudfront`, `fastly`, `netlify`), `edge_pop` (the point of presence, e.g. `NRT` from `CF-Ray`), `vercel_edge_region` (the receiving Vercel edge region), `vercel_route_regions` (up to eight reported Vercel routing regions, colon separated; no inferred function role), `edge_mitigation` (`challenge` when Cloudflare marks a challenge page; other Cloudflare products' values, such as `guardrails`, are not kept), `edge_access` (`login_redirect` when a `302`, `303` or `307` redirect's `Location` host ends in `.cloudflareaccess.com`; the URL's key ID and redirect target are not kept, and an evidence line names it only when the response names the edge's provider), `edge_cache` (one of the statuses edges report, such as `HIT`, `MISS` or `DYNAMIC`; any other is not kept), and, when Cloudflare adds its `cfL4` `Server-Timing` entry, `edge_rtt_ms` and `edge_min_rtt_ms` (the colo's own round-trip and minimum round-trip readings for the connection (TCP or QUIC) that carried the response, read as microseconds from the live capture and recorded as whole milliseconds rounded to the nearest), `edge_retrans` and `edge_lost` (decimal counts), and `edge_proto` (`TCP` or `QUIC`; any other word is dropped). The entry's connection identifier, byte counters, window, delivery rate and timestamp are not kept; no official field-layout or unit specification for the entry was found, and spot checks of several Cloudflare hosts sent no `cfL4`. These are what the response says, not verified routing: an origin can send the same headers. Values are reduced to short label characters.

A peer that is sent a secret could hand it back in what it answers, and observations reach every viewer of a collector, who is never given the secret. So when a request carries one (`bearer_env`, `request_headers` other than `Host` or a query in its URL, which the public topology hides, or a proxy `password_env` for a plain `http://` target, as such a hop can write the answer), its observation keeps what the peer said only as closed words, numbers and comparisons with the topology: an HTTP check keeps `edge_provider`, `edge_cache`, the closed `edge_mitigation` and `edge_access` words, Cloudflare's numeric `edge_rtt_ms`, `edge_min_rtt_ms`, `edge_retrans` and `edge_lost`, and the closed `edge_proto` word, but not `edge_pop`, legacy `edge_region`, `vercel_edge_region` or `vercel_route_regions`, and checks `expected_exit` without keeping `exit_ip`; a `mihomo` check sent a `token_env` keeps its `mode` as `rule`, `global`, `direct`, `script` or `other`, names in `reported_chain`, `reported_selection` and `provider.*` only the proxies its `root_proxy` and `expected_providers` declare (any other reads `undeclared`), and leaves out `test_url`. Such observations carry `peer_text: withheld_secret_request`. Use an `https://` exit endpoint to keep `exit_ip` through an authenticated proxy.

## Probe Parameters

Defaults: `interval_seconds=60`, `timeout_seconds=8`, `stale_seconds=3*interval`, `failure_threshold=3`, `recovery_threshold=2`. The first detected failure transitions state to `degraded`; reaching `failure_threshold` consecutive failures transitions state to `down`. An established outage requires `recovery_threshold` consecutive successes to return to healthy. Stale samples, monitor errors, and unknown states reset consecutive confirmation counters; observation gaps do not accumulate across lapses.

A `policy` outcome is a state assertion, not a failed request, so it never reads as `down`. An ordinary policy violation opens an incident on its first current sample, preserving the 1.1.1 default. Set `policy_threshold` to 1..20 to explicitly require consecutive violating samples; omitted or 0 means immediate confirmation. Any other outcome or a stale gap breaks the run. This setting is independent of `failure_threshold` and does not change measurement contracts or discard history. Reloading it re-evaluates retained evidence. A pending Mihomo provider load separately adds its configured settle window.

Older collectors/viewers reject the new `policy_threshold` field. Upgrade collectors and all viewers to 1.2.0 or newer before enabling it in a topology; default configurations omit the field and remain readable by older clients.

`disabled` pauses execution and `maintenance` flags planned work; neither state executes active probes or emits actionable incident alerts. Supported ranges: `interval_seconds` in 1..86400, `timeout_seconds` in 1..300. (The 1-second interval used in test suites is not recommended for production). Probes run with random jitter, non-overlapping executions per target, and a default global worker concurrency limit of 4. When more checks are due than workers are free, the one overdue longest goes first, so an observer with too much to do runs every check late rather than some never.

`bind_ip` specifies local source IP binding. An IPv6 address may carry a zone (`fe80::1%eth0`), which must name an interface the observer has; a check bound to an address or interface it cannot use reports a monitor error and never measures from another. `resolve_ip` pins dial destination IP while preserving the original HTTP `Host` header and TLS `ServerName`. These options do not guarantee bypassing host-level virtual network adapters. DNS probe target resolvers may also be pinned, while remote domain resolution over proxies depends on SOCKS/CONNECT target hostnames. ICMP requires explicit IPv4 literals, binds only an IPv4 `bind_ip` and rejects `resolve_ip`. Tailscale and WireGuard adapters read local status and reject dial/bind/target overrides.

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
| `host` | Optional `host` object; Linux, macOS and Windows | Local CPU, memory, filesystem and process counters collected; configured resource assertions pass |

### Host Resources

`kind: "host"` reads the worker's OS view. It needs no endpoint or network target. It also works with `topo once` and `serve --observer`. Linux reads bounded `/proc` files; macOS reads libSystem counters in process, without running commands. Filesystem capacity comes from `statfs`. No third-party dependencies or root privileges are required on a normally configured host. An unsupported OS, denied access, malformed counters or exhausted read budget yields a monitor `error`, never a target failure.

| `host` option | Meaning |
|---|---|
| `disk_path` | Clean absolute path on a **local** filesystem; defaults to `/`. One filesystem per check; add checks for other mounts. It is resolved in the worker's mount namespace, including systemd sandbox mounts. |
| `max_cpu_percent` | Maximum aggregate CPU use, 0..100; 0 disables the assertion. Linux samples a 200 ms interval, excluding idle and I/O wait and avoiding double-counted guest time; macOS samples `HOST_CPU_LOAD_INFO` tick deltas over 200 ms. Both report `cpu_sample_ms: 200`. |
| `max_memory_percent` | Maximum physical memory use, 0..100; 0 disables the assertion. Linux uses `MemTotal - MemAvailable`. macOS estimates available memory as free + inactive + speculative pages; this is not Activity Monitor memory pressure. |
| `max_disk_percent` | Maximum filesystem capacity use, 0..100; 0 disables the assertion. Used bytes = total − free; percent = used / (used + available to unprivileged callers), as in `df`, accounting for reserved blocks. This measures capacity, not disk I/O throughput. |
| `process_name` | Optional exact process name, at most 15 bytes. Linux matches `/proc/PID/stat`'s `comm`; macOS matches the kernel process name (`p_comm`) from `kern.proc.all`. No substring, argument or shell matching. Processes can change their names. |
| `min_processes`, `max_processes` | Bounds on matching non-zombie processes; each defaults to 0 (disabled). Require `process_name`; set `min_processes: 1` to detect a missing process. |

Host checks require `timeout_seconds >= 3` (default 8), and reject `endpoint`, `target`, `path`, `link` and dial/TLS options. A `service`, if supplied, must belong to the observer's node. A failed resource assertion emits `policy`, which the engine displays as degraded and records as a policy incident immediately unless `policy_threshold` is explicitly configured; equality with a maximum passes. Collection without enabled assertions succeeds when all counters are readable. Changing resource options changes the measurement contract.

Checks on the same worker share CPU, memory and process aggregates for up to 500 ms after collection finishes, preserving the original `sampled_at`. Only configured process names are retained as aggregates. Each check still queries its own filesystem and evaluates its own assertions. Reloading the topology starts a new runtime cache. Linux reads only the aggregate CPU line and tolerates processes exiting during both open and read. macOS reads memory capacity with `hw.memsize`, available pages with `HOST_VM_INFO64`, and visible process counts and names with `kern.proc.all`; it requests RSS with `PROC_PIDTASKINFO` only for configured names. The process table stays within an 8 MiB read budget. If macOS returns unchanged cached CPU ticks, collection retries a fresh 200 ms window after a one-second cooldown, at most three attempts within the check deadline; it never turns a missing delta into zero utilization.

Filesystem queries respect the check's deadline without delaying shutdown or reload. A blocked `statfs` syscall itself cannot be interrupted: at most four filesystem calls may remain outstanding across the entire process, including reloads. Requests for the same blocked path share its call; new paths receive monitor error `host_disk_busy` if all slots are occupied. Slots recover when the underlying calls return. A clean absolute path alone does not guarantee a responsive local filesystem.

The observation's `facts` contain `cpu_used_percent`, `cpu_logical_count` (CPUs available to the worker process), `cpu_sample_ms`; `memory_total_bytes`, `memory_available_bytes`, `memory_used_bytes`, `memory_used_percent`, `memory_basis`; `disk_path`, `disk_total_bytes`, `disk_used_bytes`, `disk_available_bytes`, `disk_used_percent`, `disk_inodes_total`, `disk_inodes_free`; `process_count`, `process_zombie_count`, and, when selected, `process_name`, `process_matching_count`, `process_matching_rss_bytes`. `violations` lists any failed resource assertions. Matching RSS sums resident memory and may count shared pages more than once.

Evidence is `reported`, with `path_evidence: local_host_runtime_reported`; collecting resources never proves a remote response or path connectivity. Process counts describe a non-atomic snapshot of **visible** processes: namespaces and OS permissions may restrict visibility. CPU and memory describe the OS view, not cgroup quotas; deploy the worker on the host for host measurements. No command arguments, environments, user names or complete process list are retained or uploaded.

See [`examples/host-resources.json`](../examples/host-resources.json). Inspect current metrics with `topo show checks host-resources`, or raw samples with `topo history host-resources --json`. Existing long-term rollups summarize check outcomes and probe duration; they do not aggregate CPU/RAM/disk values into metric time series. The TUI's charts read up to 120 raw samples of each host check's history once, then add each newer sample a snapshot carries; it keeps at most 240 per check in memory, only under the check's current contract, and stores nothing.

`topo preview` includes resource checks for all ten host machines and the router in the full built-in topology, with a local observer for each. The Servers tab (`3`) gives each card a panel for CPU, memory and disk: the reading now and the capacity, over a two-row chart of the last samples that fills as far as the resource is used, colored by headroom against the check's limit; then the named process and the visible process count. Selecting a resource row opens its check. A host check's evidence (`6`, then `Enter`) and its node's details chart each resource on the samples' time axis, four rows on a fixed 0–100% scale with the configured limit dotted across, in place of the probe's elapsed time; the raw facts follow. Edges, managed platforms and external parties have no local host counters. The two containers remain on their parent host cards; container quota metrics are not simulated. The preview remains synthetic on every OS; native host collection supports Linux, macOS and Windows.

Press `x` for CPU saturation on the application host, memory pressure on the relay, disk capacity exhaustion on exit A, or a missing process on the transit host. Other hosts retain independent healthy resource samples. A node's downtime stops its worker samples and heartbeat; maintenance excludes its resource checks. Returning to normal restores samples. Stale, cached or unreadable resource values are withheld on server cards and in evidence: the heading says when the last reading was, and charts keep earlier samples faint. Resource telemetry does not change the node's remote-response health.

Custom topologies such as `topo preview --config examples/host-resources.json` also simulate host checks. Outcomes use the same counter accounting and configured assertions as real host probes, and appear in Overview, Incidents and preview history. No machine resources or configured filesystem paths are read. Observations carry `simulation: host_resources`, stable per-host simulated OS/CPU/capacity metadata and `memory_basis: synthetic_available`. `--lang zh` or `--lang ja` localizes scenario names and descriptions.

### HTTP Probe Specifications

`http.method` supports `GET` and `HEAD` (defaults to `GET`). `status` accepts an array of expected HTTP status codes; defaults to 2xx when omitted. `contains`, `body_regex`, and `headers` define response body and header assertions. `request_headers` defines static non-sensitive headers, each named once whatever its case; `Host` (a host or `host:port`) sets the Host the request carries, for a virtual host the URL does not name, while TLS still names the URL's host unless `server_name` says otherwise; `Authorization` and `Cookie` headers are prohibited; bearer tokens must be referenced via `bearer_env`. A query in an `http` check's URL is taken for a credential, as a service's token often travels in one: it is sent only over HTTPS or to a direct literal loopback address, like `bearer_env`, and what the peer answers is kept as for a secret (above). Use `bearer_env` where the service takes one.

`expected_exit` accepts an array of expected IP addresses or CIDR blocks. `exit_format` supports `plain`, `trace`, and `json` (with `exit_json_key`, default `ip`). When the observed egress IP violates constraints, `outcome` is set to `policy` while connectivity facts are preserved. Timestamps and configuration revisions are bound to measurements, preventing metric mixing across target changes.

Default HTTP maximum response body size is 65,536 bytes (configurable up to 1 MiB). Exceeding this budget reports a monitor configuration error rather than target downtime. Probes, like every Topophany client, ask for no compression and decode none, so the budget, and a Mihomo controller's 2 MiB, count the bytes the peer sends: a body the server compresses anyway is checked as sent, and `contains` and `body_regex` see it compressed. Throughput stress-testing modes are not provided; heavy payload validations should be performed via authorized external diagnostics.

### Proxy Chains and Adapters

`proxies` defines outer connection hops in execution order: `http-connect`, `https-connect`, `socks5`, or an initial `ssh` jump hop. HTTP and SOCKS credentials reference `username` and `password_env`. `https-connect` verifies outer TLS using `server_name` and `ca_file`. SSH hops specify `ssh_user`, `ssh_identity_file`, and `ssh_known_hosts`. Give the two files as absolute paths: a relative one is resolved against the worker's working directory, which under systemd is not the directory you wrote the topology in. A leading `~` and `${...}` are refused rather than expanded.

A TCP check over an SSH jump hop alone returns `unknown / ssh_forward_unverified`: spawning an SSH subprocess cannot verify remote forward completion without application payload. Use TLS, HTTP, or SSH banner checks to obtain definitive protocol evidence. SSH file paths are normalized to absolute paths, rejecting quotes, control characters, a leading `~`, and `${...}` expansions. Ambient SSH agent forwarding is disabled.

Three-hop sequence: `Local Observer -> CONNECT Ingress -> SOCKS5 Transit -> CONNECT Egress -> Target`. Complex layered proxy protocols do not need to be decomposed into native primitives: direct probes toward a dedicated local SOCKS listener, and document internal hops in `path.nodes` and `adapter.root_proxy`.

`mihomo` adapter supports `token_env`, `root_proxy`, `expected_mode`, and `expected_providers` (mapping provider names to ordered node name arrays). Mismatched runtime members or ordering emit `policy` outcomes; identical names with differing internal parameters cannot be distinguished. Unresolved routing groups, load-balancing groups without single selections, and dependency loops report unknown status. TUN status and modes reflect controller reports rather than kernel routing table inspections.

The full controller proxy inventory is bounded at 4096 entries (including
built-ins and groups), with up to 4096 members per group/provider and 8 MiB per
inventory response. `/configs` retains its 2 MiB limit; unrelated objects,
history arrays, depth and duplicate keys remain strictly bounded. Inventory and
member overflows report `proxy_inventory_capacity_exceeded` and
`proxy_members_capacity_exceeded` as monitor errors. See the
[adapter capacity notes](../internal/adapters/mihomo/README.md#inventory-capacity).

For rotating providers, `adapter.provider_reference` can select fresh ordered
member lists from an independent HTTPS JSON publisher. Keep fixed groups in
`expected_providers`; unavailable or stale reference data is a monitor error,
and a publication changing during sampling is unknown. `settle_seconds`
(0..3600, default 120, 0 disables, and shorter than `max_age_seconds` when
set) is how long after `published_at` a difference is treated as not yet
confirmed: the controller loads a new provider over a minute or two. A
difference inside that window remains a policy sample, with
`provider_reference_settling: true`, code `provider_membership_mismatch`, and
`provider_comparison` saying `=settling`; the engine opens the incident only
after the window's equivalent consecutive policy samples (and at least
the effective `policy_threshold`), so repeated publications cannot defer it forever.
Matching membership is never deferred. Facts record
`provider_reference_published_at` (publisher), `provider_updated_at.<name>`
(the controller's own load time for that provider, reference providers first,
while the observation stays within the collector's 32 facts and each complete
key fits 128 bytes) and the sample's own time. Optional load-time keys that do
not fit are omitted; provider membership is preserved. Credential-bearing
controller responses omit raw load timestamps and numeric comparison counts;
closed match/mismatch/settling labels and independent load-pending evidence
remain available.
`provider_reference_load_pending` records load evidence independently of the
worker's wait. The collector applies its current window at the sample's time,
including after a reload and for queued samples. Legacy samples lacking this
evidence conservatively wait within the current publication window, except
for explicitly known mode/TUN violations.
An explicit `settle_seconds` is a new topology field: upgrade every collector
and worker before adding it, because older binaries reject unknown topology
fields. Omitting it uses the default and needs no topology change. See the
[Mihomo adapter guide](../internal/adapters/mihomo/README.md) for the document
format, transport and freshness limits.

A single check supports up to 16 static and dynamic providers combined, with provider names capped at 119 bytes without control characters to respect API record budgets. Long strings are truncated at UTF-8 byte boundaries; DNS response evidence exceeding 2,048 bytes returns an explicit check error.

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

The sample above illustrates schema structure and is not an active credential. `topo token` outputs a `{token, token_sha256}` pair generated using high-entropy random bytes. Never copy test tokens into production. Assign a separate worker token to each observer; viewer tokens have no write permissions. Changes to `auth.json` apply when the collector reloads (`SIGHUP`) or restarts; a reload that fails validation keeps the tokens in force. A request is served under the tokens and topology it was admitted with: a batch still being sent when a reload lands is refused with `503 topology_reloaded` and sent again under the new ones, so a revoked token stores nothing once the reload is done. A snapshot whose state has not yet been captured also returns `503 topology_reloaded` with `Retry-After: 1` after a reload, rather than mixing old authorization with new storage or incident metadata. Send a new request to authenticate against the current tokens.

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

If no collector answers and a collector explicitly returned `429 snapshot_busy`,
snapshot reads wait one second and retry the configured collectors once. The
wait is cancellable, does not extend with the server's retry header, and does
not delay a successful failover. A viewer keeps drawing its existing evidence
during the read; its usual expiry rule still applies. Other rate limits and
authentication or validation failures receive no extra retry.

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

Each webhook has its own queue of 64 alerts, delivered in order, at least once. When it is full, an alert from an observer with fewer waiting takes the place of the newest from the observer with the most, so a worker cannot crowd other observers' alerts out of a webhook they share however many incidents it makes for its own; any other new alert is dropped. Either drop is logged with the observer it was from. An alert is tried again after a failure to connect or a 408, 429 or 5xx status, with pauses doubling from 2 seconds to 5 minutes, until it has waited a day, when it is dropped and logged; any other status, including a redirect, which is never followed, drops it at once. Environment proxies are not used. Response headers are limited to 32 KiB over HTTP/1 and HTTP/2; response bodies are read only up to 4 KiB, with a ten-second request timeout. What waits is kept in `alert-queue.json` in the collector's data directory (mode `0600`; it holds alerts, never a webhook's URL or bearer), rewritten whenever an alert is queued, sent or dropped, so a collector that stops sends what waited when it starts, and logs how many wait for each webhook and how many it has sent and dropped. A reload keeps what waits for a webhook by its `id`, sent as the webhook is now configured; what it no longer takes, by event or observer, is dropped, as is what waits for a webhook removed. A queue file the collector cannot read stops it from starting rather than being replaced; move it aside to start a new one. Each collector with an alerts file sends its own, so give the file to one collector of a pair unless duplicates are wanted. The alerts file applies when the collector reloads (`SIGHUP`) or restarts; one that fails validation is refused with the rest of the reload.

Alert titles and text are clipped before queue admission to the same bounds
used for delivery (250 and 1500 bytes, plus a truncation marker). Each encoded
event has a share of the 16 MiB restart read budget, reserving space for all
16 webhooks, 64 entries each and queue metadata. An oversized event is dropped
and logged before it can evict another observer's alert. A final byte check
preserves the last readable queue file if persistence would exceed that budget.

## HTTP API Specifications

`topo serve --web-origin https://topophany.com` enables browser reads for one
exact origin. The default is empty (no CORS opt-in); wildcard origins, paths,
credentials and non-loopback HTTP origins are rejected. CORS preflight only
allows `GET` with `Authorization` on `/v1/snapshot`, `/v1/config`,
`/v1/history` and `/v1/rollups`. Actual reads still require a viewer token;
preflights pass through the existing anonymous admission/rate budgets and
never promote a socket to authenticated. Cookies and worker ingestion are
not enabled by this option. The origin is fixed at startup, independently of
configuration/auth reloads.

| Method / Path | Role | Description |
|---|---|---|
| `GET /healthz` | Unauthenticated | Indicates collector HTTP daemon is running; does not indicate observation freshness |
| `GET /v1/snapshot?observer=observer-local-01` | `viewer` | Returns current topology, evidence states (checks with their public contracts, nodes, endpoints, services, paths, links), observer liveness with whether each measures the current topology, and incidents; `observer` query param is optional |
| `GET /v1/config` | `viewer` | Returns deep-copied sanitized topology and revision hash: endpoint addresses and check targets lose their user information and fragment and have any query replaced by `redacted`, and secret references, request headers, CA files and proxy credentials are removed, an SSH hop's `ssh_user`, identity and known-hosts files among them |
| `GET /v1/history?check=ID&limit=100` | `viewer` | Returns the check's raw observation samples under its current contract, newest first (limit: 1..500), with the public revision and contract; `topo history` reads it |
| `GET /v1/rollups?check=ID&step=1h&span=7d` | `viewer` | Returns the check's long-term rollups under its current contract in `slots`, oldest first, with their `total`, `since` (the earliest moment the collector holds) and the public revision and contract; see [rollups](#rollups); `topo history --step` reads it |
| `POST /v1/observations` | Scoped `worker` | Ingests observation batches and optional heartbeats (up to 100 observations / 1 MiB per batch) |

Authentication requires the `Authorization: Bearer <token>` header. Batch writes require `Content-Type: application/json` (optional parameters such as `charset=utf-8` are permitted). Observation IDs follow `<observer>.<boot>.<sequence>`, where `sequence` is non-zero and `boot` is a valid identifier without dots to prevent cross-scope ID collision. `SampledAt` specifies local sample timestamp; `ReceivedAt` must be omitted or zero, and is populated by the collector.

Heartbeat-only durable updates are limited to one per second per observer,
including across token changes and collector restarts. An advancing heartbeat
inside that interval receives `429 observer_heartbeat_rate` with
`Retry-After: 1`; it is not acknowledged or persisted. Unchanged heartbeats
remain idempotent. Heartbeats attached to new observations share their durable
write; duplicate observations do not bypass the heartbeat limit. The latest
heartbeat's collector-owned `ReceivedAt` sets the interval; sender timestamps,
boots and topology revisions cannot reset it. A collector clock stepped back
restarts the interval at the new time instead of refusing heartbeats until the
clock catches up. Observation quotas still reserve
one liveness slot per observer.

Batches are schema 2 (`"schema": 2`). The batch's `revision` is the SHA-256 of the sender's normalized topology and only describes the sender; the acknowledgement echoes it. Each observation carries the `contract` of its check, which must equal the collector's contract for that check, or the batch is refused with `409 contract_mismatch`. A check's contract is the SHA-256 of what it measures: project, check ID, observer, kind, target (the endpoint's address when `target` is empty), timeout, `bind_ip`, `resolve_ip`, `server_name`, `ca_file`, the `http`, `dns` and `adapter` options (except `provider_reference.settle_seconds`, which only defers confirmation), and the proxies of its path in wire order. Nothing else enters it: names, notes, locations, intervals, staleness and confirmation thresholds, `disabled` and `maintenance`, what the check is attributed to (endpoint, service, path, link) and every other check can change without disturbing its samples, its history or the ID of an incident it is part of. `topo validate` prints every contract. Viewers see public contracts, computed over the redacted topology like the public revision, so they permit no guess at hidden fields.

A snapshot from a collector that saw TLS handshake failures also carries
`ingress`: `window_seconds`, `failures`, `source_count`, `last`, and up to
eight busiest `sources` (`address`, `count`, `last`) inside a rolling
30-minute window. The window is counted in sixty equal buckets, so a source
that keeps failing does not accumulate a lifetime total; a bucket at the old
edge may be omitted, so `failures` has one bucket's (30-second) resolution. `source_count` is
the number of distinct addresses the counter tracked; `truncated` says more
addresses were seen than it can track, making that count a lower bound.
`failures` counts every failure in the window, including addresses omitted
from `sources`. It counts failures reported by the HTTP server's own error
log, before any request carried an observer identity. Read it as evidence
about delivery into the collector, not about a target: a failed handshake
means that observer's evidence did not arrive, so the checks behind it read
stale rather than failed, and a source stays an address because an observer
names itself only after the handshake succeeds. Viewer snapshots replace each
source address with a short keyed digest by default (stable for the
collector's lifetime, so it cannot be reversed by enumerating IPv4 addresses
offline); start the collector with `--expose-ingress-sources` to include the
raw addresses, an explicitly privacy-sensitive choice. `topo status` prints the same summary as
`Collector ingress` when the window holds any failure.

A collector also accepts a schema 1 batch (no `schema`, observations with `revision` and no `contract`) from a worker not yet upgraded, as long as its `revision` is the collector's own; such samples count until the topology changes in any way. Upgrade collectors before workers: a 0.3 collector refuses schema 2 batches, and the worker quarantines them. Snapshots are schema 2 as well. Besides the contracts, a schema 2 snapshot carries `endpoints`, the state of each endpoint from the checks that target it, and for each observer `contracts`: `current` when its executor last reported measuring what the collector counts, `outdated` when it measures an older topology (its samples of changed checks are refused until it reloads), or absent when it has not said. Workers report this in their heartbeats as a digest of their observer's contracts. A snapshot also carries `ledger`, what the collector's incident ledger remembers of the incidents in its view: the records open (`state` `open` or `recovering`), then at most 50 closed in the last day (`state` `closed`, with `closed` and `ended`: `recovered`, `unconfirmed`, `paused` or `changed`), newest first. Each record has the incident's `id`, `scope`, `summary` and failed `checks`, `since` (the earliest failure the ledger saw), `opened` (when the collector first counted it) and `previous`, the earlier records of the same incident it holds. Clients read a record as what the collector concluded, not as evidence. A schema 2 client ignores fields it does not know, so later releases can add some without breaking it; it passes over a collector serving another schema, and says so if no collector serves its own. 0.8 clients therefore read only 0.8 collectors.

HTTP `202 Accepted` indicates successful storage and returns confirmed batch count. HTTP `409 Conflict` indicates project, contract (or, for schema 1, revision), or content mismatch. HTTP `403 Forbidden` indicates unauthorized observer scope or role. HTTP `422 Unprocessable Entity` indicates sample timestamps falling outside the ingestion window. HTTP `429` and `503` are retryable. Validation errors reject the entire batch without partial commits. External custom workers must adhere to `model.Batch` and `model.Observation` field schemas and budgets; `topo once` terminal output does not imply ingestion into a collector.

Remote configuration mutation, shell execution, and administrative actions are not supported. The API serves as a clean boundary for future web or TUI frontends rather than exposing database and log files directly.

### Rollups

A collector rolls every sample it accepts up into its check's rollups (see [operations](OPERATIONS.md#storage-and-disaster-recovery)). A rollup covers `seconds` from `start`. Each sample's outcome stands from when it was taken until the next sample or until it goes stale under the topology the collector counted it with, and the rollup says how long each stood: `up_seconds` (ok), `degraded_seconds` (policy), `down_seconds` (fail) and `unknown_seconds` (error or unknown: the monitor could not tell). `unmeasured_seconds` is the rest, when no current sample stood for it; it is never up or down. Availability is `up_seconds` over the measured time, `up_seconds + degraded_seconds + down_seconds`, read together with the share of `seconds` that was measured. `unreconciled_samples`, when nonzero, counts samples in intersecting buckets whose interval boundaries were unavailable after retention, budget eviction, the per-sample correction work limit or upgrade from an older checkpoint, or conflicted at an identical timestamp; this count can exceed `samples` when a partial bucket contributes time but its sample counts are omitted; time totals are incomplete, so clients do not present them as an uptime percentage. `samples` counts the samples taken within the rollup and `outcomes` the samples by outcome; `latency` estimates what the passing ones took (`samples`, `p50_ms`, `p95_ms` and `max_ms`) from histogram bins a quarter of a doubling wide.

`/v1/rollups` lays them out in slots of `step`: `5m`, `1h` (the default) or `1d`, aligned to UTC, over `span` before now, written as minutes, hours or days such as `90m`, `24h` or `30d` (by default a day of `5m`, a week of `1h` or 90 days of `1d`; at most 800 days and 999 steps). A collector keeps rollups five minutes wide for 48 hours, an hour wide for 32 days and a day wide after that; where they are wider than `step`, a slot is as wide as what is kept, so slots may grow wider into the past. The first slot holds the start of the span and the last ends now. `step_seconds` and `generated_at` say how it was laid out. A step or span outside these bounds is refused with `400 invalid_step` or `400 invalid_span`.

Each check in a snapshot may carry `availability`: its rollups over each `window` ending when the snapshot was generated, `24h`, `7d` and `30d`, in the fields above, with `since` when the rollups begin inside the window. A check the collector holds no rollups of has none, and a snapshot over its budget leaves them out before it leaves out last observations.

## Environment Variables

The terminal interface (`topo`, `topo edit` and `topo preview`) reads these.

| Variable | Effect |
|---|---|
| `NO_COLOR` | Any non-empty value turns color off, as `--no-color` does: no escape for color is written and selection is shown with plain glyphs such as `›` and `[a]` |
| `COLORTERM` | `truecolor` or `24bit` turns on 24-bit color. Windows Terminal, iTerm2, WezTerm, VS Code, Ghostty, Hyper, Tabby and kitty are recognized without it; other terminals get xterm-256 |
| `TOPO_BACKGROUND` | `terminal` (or `none`, `transparent`) keeps the terminal's own background instead of the UI's dark ink |
| `TOPO_HYPERLINKS` | `1` (`yes`, `on`, `true`) or `0` (`no`, `off`, `false`) decides whether links are written as OSC 8 hyperlinks; by default they are, except on the Linux console, a dumb terminal and GNU screen outside tmux |
| `TOPO_CELL_ASPECT` | A character cell's height over its width, such as `2.6`, for drawing the About globe and the geographic map in their true shape. When unset, the shape comes from the pixel size the terminal reports, else from its answer to `CSI 16 t`, else is taken as `2`. Windows Terminal answers with a fixed 10×20 cell whatever its font and line height, so it needs this where its cells are not 2:1. Values outside 1 to 4 count as `2` |
| `LC_ALL`, `LC_MESSAGES`, `LANG` | The first that is set chooses the language of `topo preview`'s built-in topology and scenarios; `--lang` overrides it |

### Storage allocation

Observers accept `storage_weight` (omitted/zero means 1; otherwise 1–1000). This trusted administrative setting changes quota allocation, not check identity or authorization scope. `/v1/snapshot` adds optional `storage`: global `receipts`, `max_receipts`, `state_bytes`, `max_state_bytes`, `receipt_hours`, and `observers` containing `observer`, `receipts`, `receipt_limit`, `bytes`, `byte_limit`, `last_hour_receipts`, `settled_receipts`, `fills` and `full_in_ns`. An observer-filtered view includes only that observer's allocation rows. Legacy snapshots may omit it. `topo capacity --help` describes offline preflight flags.

### Central address definitions

An optional top-level `addresses` object maps up to 256 stable IDs to literal IPv4, IPv6 or DNS hostnames. For example, `"addresses":{"dmit-public":"154.17.5.197"}` and an endpoint `"address":"${dmit-public}:22"` share one declared address. IPv6 references in host:port targets need brackets: `[${entry-v6}]:443`.

References resolve in endpoint addresses; check `target`, `bind_ip`, `resolve_ip`, `server_name`, DNS names/expected IPs, HTTP expected exits and adapter peers; and proxy addresses/server names. They never read environment variables or interpolate credentials, filesystem paths, commands or notes. Definitions cannot be recursive, URLs or secrets. Missing/malformed references fail validation; expanded fields are bounded to 8192 bytes. See `examples/address-registry.json` (paused, documentation addresses only).

`topo address --config topology.json` lists definitions. `--set NAME=IP_OR_HOSTNAME` validates and atomically updates the local file; `--delete NAME` refuses definitions still referenced. `--dry-run` previews the change. Output lists `changed_checks`: precisely the checks whose resolved measurement contract changes. Distribute/reload the reviewed topology to apply it; the command performs no remote writes.

Local config, the editor and deployment bundles preserve references. Public topology and server cards show resolved addresses; aliases themselves are omitted from public topology. Replacing a literal with an equivalent reference, renaming a definition or changing an unused definition preserves measurement history. Changing an address starts new series only for dependent checks. Keep stable names and purposes in labels/notes and let the address rows show current endpoints, avoiding a second copy of IPs in prose.


### Native Windows host resources

Windows host checks call Win32 APIs directly, without PowerShell, WMI or an external agent: `GetSystemTimes` for CPU deltas, `GlobalMemoryStatusEx` for physical memory, `GetDiskFreeSpaceExW` for capacity, and Toolhelp/`K32GetProcessMemoryInfo` for visible process counts and selected working sets. The sampler queries every processor group on private threads when needed; a thread whose affinity changed is retired instead of returned to the scheduler. CPU sampling and process enumeration respect cancellation; counters and arithmetic are bounded. The API definitions are documented by [Microsoft](https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-getsystemtimes).

Use a clean local drive path such as `C:/` or `D:\Data`; drive-relative paths, UNC paths and traversal are refused. The default `/` means the worker's current drive root. A Linux/macOS collector can validate a Windows drive path without interpreting it as its own filesystem.

`memory_basis` is `windows_AvailPhys`. Windows capacity is caller-relative under quotas: `disk_basis=windows_caller_quota`, total and available are the caller's counters, and used is their difference; the separately reported `disk_volume_free_bytes` is volume-wide. Inode counters are unavailable (`disk_inodes_basis=unavailable_on_windows`), and POSIX zombie semantics do not apply. See [GetDiskFreeSpaceExW](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-getdiskfreespaceexw).

Process matching is case-insensitive against the executable basename or its stem without `.exe`; the existing 15-byte configured-name bound still applies. Only selected names' counts/RSS are retained, never the inventory, arguments, environment or user names. RSS uses query-limited process handles; protected/unreadable selected processes produce a monitor error, and processes that exit during enumeration are skipped. Shared pages can be counted in more than one working set. Native acceptance must run on Windows; successful cross-compilation alone does not test these APIs.

## Persistent monitor panels

For RunCat Neo, use the [five-row custom metrics example](../examples/runcat/README.md).
It publishes RunCat's JSON format with host-online and application-health bars.

`topo monitor` manages named, local display panels. Templates use Topophany metric
names and contain no executable expressions. File imports and stdin imports save
the template content; the original file is not needed afterwards.

```sh
topo monitor configure desktop --template examples/monitor-desktop.json --source client --profile default
topo monitor run desktop
topo --monitor desktop

topo monitor configure desktop --output ./topophany-metrics.json --interval 10s
topo monitor show desktop
topo monitor path desktop
topo monitor preview desktop
topo monitor export desktop
topo monitor metrics
topo monitor configure desktop --enabled=false
```

`--template -` reads one bounded JSON value from stdin, including a quoted heredoc.
`export` prints the saved template. `metrics` lists metric names, types, units,
selectors and supported sources. `path` prints only the output path. `preview`
reads the last output and applies current freshness deadlines without writing or
contacting a source. `show` reports saved configuration, observed publisher state,
and the running configuration revision when its publisher lease is current.

A first configuration requires a template. Later calls patch only explicitly
provided settings. Invalid changes leave the previous configuration intact.
**Changes take effect after restarting the panel's process.** In particular,
`--enabled=false` prevents subsequent attachment/start; it does not stop a running
Collector, Worker, TUI or standalone panel. A configuration command never installs
or starts a background service.

Configuration schema 1 embeds the template in one `config.json`, so configuration
and template commit in a single durable replacement. Its default location is
`<user-config-dir>/topophany/monitor/<name>/config.json`. The rebuildable output
is `<user-cache-dir>/topophany/monitor/<name>/metrics.json`. `--monitor-dir DIR`
selects an explicit configuration root on every monitor command and on `topo`,
`serve` and `worker`. Relative output/credential paths become absolute during
configuration. Output paths must end in `.json`. Existing unrelated files,
symlink output targets, protected configuration names and attached-process data
directories are refused. Parent directory aliases are resolved before locking.

The configuration stores the named credential profile and absolute credential
file path, never the bearer token. The active profile is resolved once at first
configuration; changing the credential file's active profile later does not
retarget the panel. An existing profile pins its project. A changed project
requires explicit reconfiguration. Offline entities are allowed; unknown metric
names, unsupported source capabilities, invalid row references and incompatible
formats fail validation.

### Streaming panels

`topo monitor stream` reads one JSON request per line on stdin and writes one
JSON frame per line on stdout. It creates no panel configuration, output file or
lock. `topo monitor stream --capabilities` prints one line with protocol 1, the
binary version, sources, visuals and the metric catalog, then exits. The catalog
includes `servers.list`; `profiles: true` advertises saved-profile discovery.
These additions retain protocol 1. Consumers of older binaries treat an absent
`profiles` field as false and check the catalog before requesting `servers.list`.

`topo monitor stream --profiles` lists saved client profiles in one JSON line,
then exits 0:

```json
{"profiles":[{"name":"home","project":"homelab","active":true},{"name":"work","project":"","active":false}]}
```

The active profile comes first, followed by other names in lexical order, at most
64 entries. `active` identifies the profile an empty request `profile` selects.
Only names, projects and the active flag are exposed; tokens, collector URLs and
file paths are omitted. An unset project is `""`. Listing does not validate a
profile's connection or contact a collector; normal client connections still
require valid credentials and a project.

`--profiles --credentials PATH` reads that file instead of the normal credential
path, retaining the private-file permission and 1 MiB size checks. No credential
file, or no saved profiles, produces `{"profiles":[]}`. An unreadable, unsafe or
invalid file also produces an empty list and exit 0, with one fixed diagnostic on
stderr. The command reads no stdin and writes no files. `--capabilities` and
`--profiles` are mutually exclusive; the command-line `--credentials` option is
for `--profiles`, while a running stream takes credentials in each request.

A request has `schema: 1`, an optional `request_id` (at most 64 bytes), `source`
(`client` by default), `interval` (5s by default, 1s–1h), and a schema-1
`template`. Client requests accept an absolute `credentials` path, a saved
`profile`, a `project` guard and an `observer` view. Empty credentials/profile
use normal credential discovery and the active profile. Credentials are read
only for the client source. Preview requests accept `scenario` (0 by default)
and `lang` (`en`, `ja` or `zh`, default `en`); they read synthetic evidence
from the same simulator as `topo preview`, without credentials or host probes.

For example, [monitor-stream-preview.json](../examples/monitor-stream-preview.json)
requests all six structured evidence metrics, including the server chooser and
one selected server card:

```sh
topo monitor stream < examples/monitor-stream-preview.json
```

The example is one NDJSON request line. After its first frame, file EOF produces
a final stopped frame; an interactive consumer keeps stdin open for updates.

No frame precedes the first request. A valid request replaces the current one
and publishes immediately, with a new `config_revision` and the echoed
`request_id`. A client starts with unavailable rows while its first collector
read is pending. A replacement client source checks its first read within a
five-second budget while the previous request continues publishing; only then
is the replacement committed. A foreign-project response rejects the candidate.
An invalid request publishes an `error` frame with no rows and
keeps the previous request running. Error codes are `invalid_request`,
`unsupported_protocol`, `credentials_unavailable`, `profile_not_found` and
`project_mismatch`; messages contain no raw upstream errors and are bounded to
1024 UTF-8 bytes. An invalid request ID longer than 64 bytes is omitted from
the error's identity (the field is empty). A collector that
cannot be read produces running frames with null values and
`reason: "source_unavailable"`.

Requests are bounded to 256 KiB per line; an oversized line is drained and
rejected without ending the stream. Frames are bounded to 1 MiB, each written
once and flushed. Values too large for a frame are withheld with
`reason: "frame_budget_exceeded"`. EOF, SIGINT or SIGTERM publishes one final
`stopped` frame with an empty `rows` array. A closed stdout ends quietly.
Runtime failures exit 0; only bad command-line flags exit 2.

The stream uses the same source refresh, one-second evidence aging, display
cadence, immediate safety transitions and publisher lease as file panels below.
Structured rows keep the original deadlines of the evidence behind their
collector judgments. When a deadline passes, the row's value is withheld until
a new snapshot supplies a current judgment; aging a check never extends an old
healthy service or node. Server card row deadlines cover node and displayed service judgments, including
transitive service and path dependencies. Resource readings and nested observer
verdicts carry independent deadlines: expired readings lose their numbers, and
expired observer verdicts become cached, without discarding the card. The source
envelope and publisher lease still bound the entire card.
Its function API in `internal/monitor/stream` accepts request lines and a frame
callback, honors context cancellation, and allows another source adapter for
embedding. Every callback receives an independently writable frame, including
the first frame and nested metric values.

### Sources and process ownership

| Source | Process | Scope |
|---|---|---|
| `client` / `preview` | `topo monitor stream` | Requests on stdin; saved connection or synthetic evidence |
| `client` | `topo monitor run NAME` | Saved profile; optional `--observer` configured view |
| `client` | `topo --monitor NAME` | The TUI's actual fetch results, including view switches and failures |
| `collector` | `topo serve … --monitor NAME` | Current collector's complete project view, read without persistence side effects |
| `worker` | `topo worker … --monitor NAME` | This worker's local observations, threshold state and separate delivery reports |

The source must match the process role. Optional configured project/observer
bindings must match at startup. An attached TUI also checks the saved profile's
project and collector URLs against its actual connection; a mismatch fails
explicitly. Its panel shares the TUI fetches, without opening another polling
connection. Worker panels cannot use `summary.status`; `local.status` describes
local checks and does not assert successful upload or global project health.

For example:

```sh
topo monitor configure collector-panel --source collector --template examples/monitor-desktop.json
topo serve --config topology.json --auth auth.json --data collector-data --monitor collector-panel

topo monitor configure worker-panel --source worker --template examples/monitor-worker.json
topo worker --config topology.json --credentials worker-credentials.json --observer local --data worker-data --monitor worker-panel
```

### Template and metric contract

Templates have `schema: 1`, `title`, optional symbolic `icon`, `bar.row` and an
ordered `rows` array (1–128 rows, at most 256 KiB for the saved configuration).
Each row has a unique `id`, optional `label`, `metric`, optional `select`, optional
`format`, `visual` and optional `limit`. `bar.row` references a row for a
system-bar consumer. Icons are symbolic names; consumer-specific
icon rendering is not implemented here.

The metric catalog includes:

- `summary.verdict` (`status`): the Overview verdict, glyph and text, check
  counts, configured total, incident count and live/total observers. Paused and
  maintenance counts are subsets of unknown and never add to the total.
- `observers.verdicts` (`list`): each observer's own verdict and counts, liveness
  and last evidence, ordered down, degraded, unknown, up, then by name; at most
  64 entries. A scoped stream also reads the complete snapshot for these
  verdicts. Scoped file and attached TUI panels use the evidence they already
  receive: the selected observer retains its verdict; other observers retain
  heartbeat liveness but have cached verdicts and unknown check counts.
- `attention.list` (`list`): the Overview's Needs attention list in its order,
  with a total, titles, routes, observers and incident start times. Its row
  `limit` defaults to 5 and accepts 1–20. Incident states are lowercase
  `view.Pill`; other states are lowercase `view.State`, or `up` when empty.
- `applications.list` (`list`): the Services tab's application/environment
  groups, evidence counts and services in topology order, with attention and
  unmonitored counts; at most 64 groups and 64 services per group.
- `server.card` (`card`): a required `select: {"node":"ID"}` names the server.
  Its card carries the node state, place, local resources (primary disk first),
  up to 32 services in trouble-first order, and observers with their verdicts.
  Stale, cached and unreadable resource numbers are null; original sample
  timestamps remain. An unknown node yields `entity_missing`.
- `servers.list` (`list`): up to 256 nodes with cards in the Servers tab, in that
  tab's topology order. External nodes and containers belonging to a declared
  parent are omitted; edges, platforms and unowned containers remain eligible.
  Each entry has `id`, `name` (ID when unnamed), `kind`, `provider`, `group`,
  `where`, `health` and `note`, matching its `server.card`. Use its ID as that
  card's `select.node`. Cached entries have unknown health. The row health is
  the worst listed health, and its text is, for example, `14 servers`. Its
  deadline is the earliest source-envelope or listed-node judgment deadline;
  unrelated service, resource and observer evidence does not expire the list.
  Per-node health and note changes publish immediately, even if the row's worst
  health stays the same. A readable empty topology produces `[]`; an unavailable
  source produces null with `source_unavailable`, as for other metrics.
- `summary.status`: complete source-view verdict, independent of which other
  rows the template displays; available to client/collector sources.
- `local.status`: the worker's local view verdict.
- `check.status`: one check's current health.
- `host.cpu.used_percent`, `host.memory.used_percent`, `host.disk.used_percent`:
  numeric percentages in **0–100**, suitable for `format: "percent"` and
  `visual: "bar"`. CPU is whole-host normalized utilization over the sampler's
  window. Output includes `sample_window_ms` when available, and `basis` for
  CPU/memory/disk accounting; memory and disk retain the native sampler's
  platform-specific definitions rather than silently normalizing unlike metrics.
- `delivery.status`, `delivery.pending_records`, `delivery.pending_bytes`:
  worker transport reports, selected by optional collector identity digest.
  `ready` means no currently reported transport error, not proof all samples
  have reached the collector; read the pending counts alongside it.
- `storage.journal.write_bytes`, `storage.checkpoint.write_bytes` for collectors;
  `storage.queue.operations`, `storage.queue.write_bytes` for workers;
  `storage.sync.count`, `storage.sync.duration_ms` for both local service roles.
  These are counters since process start, not remote client metrics.

The six structured metrics above support client and collector sources; preview
uses the client capabilities. Each requires its listed visual. Existing metrics
keep `text` and percent metrics also accept `bar`. `limit` belongs only to
`attention.list`; `select.node` belongs only to `server.card`. Validation errors
identify the row.

Check metrics accept `select: {"check":"ID"}`. Delivery metrics accept
`select: {"collector":"16-character-lowercase-hex-digest"}`. A missing selector
is permitted only when runtime matching yields exactly one entity. Zero matches
produce `entity_missing`/`never_reported`; multiple matches produce
`selector_ambiguous`. There is no implicit aggregation. With multiple delivery
destinations, set the collector digest explicitly in each delivery row.

### Output schema and freshness

Panel output has its own `schema: 1`: `name`, `config_revision`, `instance`,
`sequence`, `source`, `project`, optional `observer`, `lifecycle`, `generated_at`,
`publisher_expires_at`, `title`, `icon`, `bar_row`, and ordered `rows`. Sequence
numbers advance within a publisher instance and can skip for slow consumers.
Each row includes its template fields, scalar or structured `value`, `unit`, formatted `text`,
`health`, `freshness`, optional `reason`, `sampled_at`, `valid_until`, and resolved
check/observer/collector identity where applicable. Unknown numbers are `null`,
never zero. Health and data freshness are separate dimensions.

Rows are projected at one evaluation time; their underlying samples can have
different timestamps. Observer verdicts in a scoped stream use an accompanying
complete snapshot from the same source. Actual sample timestamps
survive refresh and replay. Summary rows use the newest underlying check sample
when one exists and expire at the earliest relevant fresh-evidence boundary.
Source snapshots also expire after 15 seconds, independently of check TTLs.

`--interval` defaults to 5 seconds and accepts 1 second through 1 hour. It does
not reschedule checks. Source refresh is at most 5 seconds apart; evidence is
aged every second. Changed evidence is written on the display cadence; health,
freshness and lifecycle transitions can write sooner. Unchanged content skips
writes, except publisher renewal at most 10 seconds apart, with a 30-second
publisher lease. Thus a large interval does not suppress safety transitions or
publisher renewal. A first configuration creates a `not_started` placeholder;
reconfiguration never resets existing output. Startup publishes immediately;
orderly shutdown publishes `stopped` and removes current values.

**Readers must evaluate both `publisher_expires_at` and each row's `valid_until`
against their current clock.** A killed process cannot write `stopped`; its file
can still contain `running` after the lease has expired. Do not use mtime or
`generated_at` as a substitute for the original sample time, or extend evidence
validity because the publisher renewed its lease. Implausibly future timestamps
are treated as unavailable. `monitor preview` applies this reader policy.

File snapshots and stream frames share a 1 MiB serialized limit. Rich row
values have a 64 KiB individual limit and share a 512 KiB aggregate budget,
leaving room for template metadata and scalar rows. Repeated structured
projections are computed once per panel. Oversized values become unavailable
with `reason: "frame_budget_exceeded"` before retention or file serialization.
Files use compact JSON; excessive envelope/template metadata is rejected
without replacing the previous complete file.

Each output has one cooperating writer, protected by a process-lifetime lock in
a separate stable file. Locks release after process termination. Writes create
a complete temporary file beside the destination and replace the destination;
Windows sharing violations receive bounded retries without deleting the old
file. Readers should reopen by pathname and permit delete sharing on Windows.
Only local filesystems are supported for lock/replacement guarantees. Output
failures are reported and retried; the output path can become unavailable.
Always retain freshness checks even when a previous complete file remains.

The display cache does not force file/directory synchronization on each update.
Configuration, journals, checkpoints and outboxes retain durable writes. File
output runs separately from ingestion/sampling and coalesces only display
frames. `monitor.Hub` exposes latest-snapshot retrieval and bounded subscriptions
with the current frame included atomically; slow consumers skip intermediate
frames. The core can run with no file sink. Native menu-bar/tray UI, IPC, charts,
expression evaluation and history buffers remain future work.

### Additional software adapter policies

Mihomo `adapter.expected_tun` accepts an explicit boolean; missing controller
state stays unknown. `provider_match_modes` maps expected provider names to
`ordered` (the default) or `set`. Set mode ignores ordering, while duplicate,
missing and extra members still violate policy. Bounded `provider_comparison`
counts explain mismatches without exposing undeclared names for public
controllers. Credential-bearing responses retain closed comparison labels
without raw counts or controller timestamps.

WireGuard `adapter.expected_allowed_ips` optionally asserts a nonempty set of
1..128 CIDRs. `expected_keepalive_seconds` asserts 0..65535 seconds (0 is off).
These inspect public runtime fields, not OS route correctness or business
reachability. Use an application check inside the tunnel for that evidence.

Tailscale `adapter.tailscale_diagnostic` optionally accepts `disco` or `tsmp`
and requires `timeout_seconds >= 4`. Omission keeps collection passive. Enabled
checks issue one bounded ping to the uniquely selected peer's reported IP.
DISCO records direct/DERP/peer-relay response hints and may negotiate transport;
TSMP records a response without identifying the actual transport. Both retain
reported evidence and leave application reachability unverified. No application
or host health is inferred from a diagnostic timeout.

The CLI can report an internal timeout on stdout and still exit non-zero
(`ping "<ip>" timed out`). The adapter recognizes exactly that text for the
fixed target and reports `unknown` with `diagnostic_response: timed_out`; every
other non-zero exit stays a monitor-side error, so a stopped daemon, a missing
peer or a permission problem is never read as the peer not answering. Facts
carry `last_seen` only when the control plane reports it; a missing time reads
`not reported`, not the year 1 and not the sample's own time.
