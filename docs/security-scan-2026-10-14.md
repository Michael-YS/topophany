# Security scan remediation — 2026-10-14

Scope: the four findings in Codex Security Cloud repository scan
`wfr_18827793fe859210b895beed4403eafc43e13f09876649049ad28ea5159680b0`,
which scanned revision `68ba87fdda9161399a4aba0a3ffc8acbc3654f6b`.
The remediation branch started from `52d0e16deedc935fa0d33aa4171da3619a8005df`;
CI tests the proposed merge against the current PR base.

## Collector connection admission

The existing global connection ceiling remains in place before TLS and HTTP
parsing. A socket source may occupy at most one quarter of that ceiling, capped
at 16 connections. The minimum per-source allowance is one. IPv4-mapped IPv6
addresses are normalized to IPv4; IPv6 sources share a /64 quota. Forwarded HTTP
headers are not trusted to establish connection identity. Rejected connections
do not create accounting entries, and inactive entries are removed.

Every accepted connection has an absolute two-minute lifetime. The lifetime is
implemented by closing the connection, not by a socket deadline that net/http
can refresh. It therefore applies to active keep-alive, TLS and HTTP/2 connections
as well as idle connections. Existing header, body and idle timeouts remain.

HTTP/1 health checks, unknown routes, bad methods, authentication failures and
pre-authentication rejections close their connection. Successful authentication
and role authorization permit connection reuse. HTTP/2 does not receive the
HTTP/1-only `Connection` header; source quotas and the absolute lifetime still
apply at the socket level.

**Deployment trade-offs:** clients behind one NAT or reverse proxy share a source
quota. A long-running request can be interrupted by the absolute lifetime, and
clients must reconnect. These application bounds are not protection against
volumetric network attacks or enough independently controlled source networks.
Public deployments still need appropriately configured upstream admission and
network-level protection; trusting arbitrary `X-Forwarded-For` would not solve
source attribution safely.

## Public versus private revision identity

`config.Revision` remains the execution identity of the complete configuration.
Workers, ingestion validation, acknowledgements, persistence and stale-observation
filtering continue to use it. Changing a private execution setting must invalidate
old work even when the visible topology does not change.

`config.PublicRevision` hashes only `config.Public(c)`. Viewer configuration,
snapshot and history responses use this public identity, including revisions in
nested observations. Snapshot serialization redacts before caching and clones
check states before replacing their last observations. Stored evidence is not
rewritten. A viewer-visible digest consequently cannot distinguish configurations
that differ only in fields removed by the public projection.

This boundary covers URL user information and query contents, private HTTP
headers, bearer and adapter environment-variable names, proxy credentials, SSH
identity/known-host paths and CA-file paths. A change in public topology still
changes the public revision. The public revision is not accepted as a worker
execution revision.

## Bounded diagram work

Configuration acceptance and diagram complexity are separate. The network and
virtual diagrams accept at most 256 graph nodes and 512 edges, a viewport no wider
than 512 or taller than 256 cells, and at most 32,768 cells total. Dimension checks
avoid multiplication overflow. Larger inputs show an explicit complexity message;
the underlying topology remains available to the list and detail views. Virtual
row measurement applies the graph bounds before clustering and does not allocate
a routing grid for its tall measurement area.

Network crossing-cost optimization uses one finite work counter across every
candidate order and every layer gap. It never adopts an incompletely evaluated
cost. Equal source rows have a deterministic tie-breaker. Ordinary small diagrams
retain exhaustive ordering where the budget permits it.

Virtual A* expansion and existing-wire comparisons use one finite work counter
across all edges, retry passes and the alternative column layout. Exhausted search
returns no wire, not a partial or fabricated connection. The return value retains
one entry per edge, so missing routes cannot accidentally look like a successful
empty layout. Existing cache reuse avoids repeating routing for unchanged layouts.
Column-gap ordering is computed once rather than sorted for each candidate width.

The shared ceiling is 2,000,000 counted operations per optimization/routing
operation. Structural limits bound the other layout work. This is a deterministic
work limit, not a wall-clock latency guarantee. It deliberately favors responsive
interaction over drawing every wire in pathological cases. Existing dense-diagram
connectivity and wire-intersection assertions remain in place to guard normal
rendering quality.

## Regression coverage and reproducible validation

The added tests check both denial behavior and preserved functionality:

- Source fairness against a 128-connection single-source flood; global and source
  boundaries; IPv4/IPv6 normalization; no accounting for rejected sources;
  concurrent repeated close; lifetime expiry despite active reads and renewed
  socket deadlines; HTTP and TLS authentication/role failures; authenticated
  HTTP/1 reuse; HTTP/2 reuse; pre-authentication rejection headers.
- Every redacted field's public/private revision behavior; unchanged caller data;
  recursive checks of all viewer response revisions, including cached snapshots
  and nonempty history; private worker acknowledgements and stale-batch rejection;
  private-header fuzz seeds and a bounded fuzzing campaign.
- Zero/exact/exceeded work budgets; dimension and integer-overflow boundaries;
  deterministic network ordering without input mutation or missing buses;
  hand-checked crossing costs and exhaustive small-graph quality; blocked routes;
  path endpoints, adjacency and obstacles; shared exhausted work across attempts;
  explicit fallback for 257- and 3,000-node graphs and recovery to a small graph.

The standard CI retains formatting, vet, vulnerability checking, Linux/macOS race
tests, Windows tests/builds and the full acceptance suite. The Coverage workflow
adds identical statement-coverage commands for the proposed merge and the PR base,
retains package/function profiles, repeats security tests ten times with shuffled
order and the race detector, fuzzes private headers for ten seconds with two
workers, and records layout benchmarks. Test failures are not ignored. Benchmark
measurements are diagnostic and are not used as fragile machine-speed assertions.

Representative commands:

```sh
go test -race -count=1 ./...
go test -count=1 -covermode=atomic -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
go test -race -count=10 -shuffle=on -run 'Test(Security|ConnectionSource|SourceQuota|GlobalAndSource|ConnectionLifetime|InvalidConnection|HTTPConnection|ViewerRevisions|SummaryDoesNotMutate|PublicRevision)' ./internal/api ./internal/config ./internal/tui
go test -run '^$' -fuzz '^FuzzPublicRevisionPrivateHeaders$' -fuzztime=10s -parallel=2 ./internal/config
go test -run '^$' -bench '^BenchmarkSecurity' -benchtime=1x -count=3 -benchmem ./internal/tui
```

Consult the PR's actual check results and the retained `coverage` artifact for
measured coverage and execution results. This document describes the regression
contract; it does not claim that a new Codex Security Cloud scan has completed or
that repository-scan findings have been administratively closed.
