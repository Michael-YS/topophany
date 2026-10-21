# Multi-probe ingestion: implementation contract and record

Status: in progress. Branch: `codex/multi-probe-infrastructure`.
This document is the design freeze and the delivery record for the external
observation work. It is updated by every task commit. A later change to a
frozen rule needs a recorded reason here, not a silent change in code.

The goal is not to add a vendor adapter. It is to make the next adapter a
typed configuration, a bounded reader, versioned parsing, field mapping,
fixtures and a small explicit wiring change. Persisted deduplication,
confirmation, generic resource selection and the monitor card's numeric shape
must not need to change again.

## 1. Baseline

Recorded on 2026-10-21 from `main` at `74db358`, clean worktree, Go 1.26.10.
The branch was created before the first edit. No existing uncommitted work was
present or overwritten.

Baseline commands and results:

```text
go test ./internal/config ./internal/probe ./internal/runner ./internal/api \
  ./internal/store ./internal/engine ./internal/rollup ./internal/outbox \
  ./internal/client ./internal/view ./internal/monitor ./internal/adapters/...
```

All listed packages passed. There were no pre-existing failures to exempt. The
full repository checks are repeated in task 10; the focused matrix is repeated
per task in section 8.

The adjacent `../topophany-bar` checkout is read-only for this work. At
baseline it is on `main` at `ac40417` with a clean status. Its decoder reads
`resources[0]` for CPU, memory and disk, gives each nested resource its own
`valid_until`, and withdraws a resource's numbers at that deadline while the
card can remain current. No bar file is modified by this delivery.

## 2. What is actually wired today

- `model.Config` is schema 1 and is decoded strictly (unknown fields fail).
  Checks carry observer, kind, scheduling, transport and assertion fields, but
  there is no top-level source list and no binding between an external object
  and a check.
- `model.Observation` has one collection identity (`observer.boot.sequence`),
  one sampled time, one outcome and one evidence word. It has no second report
  payload or upstream identity. `model.Snapshot` is schema 2.
- `config.Contract` hashes a check's measured fields (kind, target, transport,
  assertions, path proxies). Schedule and attribution are deliberately not in
  it. `config.Public` redacts secret references and content. This boundary is
  reused for external bindings rather than inventing a second history key.
- `probe.Run` owns execution, cancellation, observation identity and error
  classification. Adapter packages own endpoint restrictions, protocols,
  parsing and reported facts. Nothing may turn an adapter/API/network/parse
  error into a target failure.
- `runner.Scheduler` runs native check jobs with bounded workers, one in-flight
  target/observer pair, jitter and a stop-on-sink-error rule. It is the place
  for source jobs, not a replacement for the native scheduler.
- `api.ingest` admits one batch under a topology generation, validates scope,
  contract, identity, timestamp and evidence, and calls `store.Put`. It trusts
  no client-owned receive time. `store.Put` fsyncs the journal before applying
  duplicate receipts, tails, rollups and carries.
- `store` persists observation-ID receipts, tails, carries, heartbeats and
  rollups in a checksummed checkpoint (on-disk state schema 1) plus journal
  replay. Raw history can be trimmed while receipts remain. There is no
  external report cursor or source-contact state.
- `engine` folds native observations through `Carry` into `CheckState`, then
  builds entities and availability from those states. There is no reported-
  only evaluation path and resource observations are not separately masked.
- `view.HostResources` selects only checks whose kind is `host`, attributes
  them through `observer.Node`, and derives `CardResource.Facts`. TUI and CLI
  read those facts; monitor's `server.card` converts the same facts to its
  numeric `ResourceValue`. There is no common resource model, no ordered
  fallback and no explicit primary disk.
- `rollup` counts every admitted observation's outcome and (for passing ones)
  duration. External resource observations must not enter this path, or a
  resource read would become service availability and collection time would
  become probe latency.
- `outbox` batches observations by count (at most 100) and bounds record bytes.
  External fan-out needs a byte split too, but no new dependency or storage.
- `monitor` publishes schema-1 frames; `server.card.resources[0]` is the
  compatibility surface used by `topophany-bar`. Existing numeric field names
  must stay stable.

## 3. Frozen ownership and types

Three layers own three different things.

### Source (executor ownership)

A source is one external instance read from one observer.

| Field | Meaning |
|---|---|
| `id` | Stable local source identity. Not an upstream object ID. |
| `name`, `notes` | Display only; never define sample identity or contract. |
| `adapter` | Closed adapter type. Parsing and version support belong to that adapter package. |
| `observer` | The one observer allowed to execute this source and submit its reports. |
| `target` | Endpoint address, adapter-specific. Public views strip secrets, queries and fragments. |
| `credential_env` | Environment variable *name*, never its value. Public views remove it. |
| `path` | Optional declared proxy path; no environment proxy or direct fallback. |
| `bind_ip`, `resolve_ip`, `server_name`, `ca_file` | Explicit transport and TLS policy. |
| `interval_seconds` | Source-level cadence. One source collection fans out to bindings. |
| `timeout_seconds` | Whole source attempt deadline, including pages and parsing. |
| `stale_seconds` | Contact freshness: last successful read + this window. |
| `max_response_bytes` | Per-response byte budget. |
| `max_pages` | Page/pagination budget. |
| `max_objects` | Objects accepted from one attempt. |
| `max_report_bytes` | Encoded budget for one normalized report. |

A source is executed only by its declared observer. Credentials are read from
the named environment variable by the runner/IO boundary; the config never
stores a secret value. A source's runtime session is keyed by its own
identity, credential generation, route and TLS policy. It is never shared by
URL across sources.

### Binding (check ownership)

An `external` check is a binding through a new `external` object. It is the
unit that history, confirmation and outbox identity already know.

| Field | Meaning |
|---|---|
| `source` | Source ID. The check observer must equal the source observer. |
| `object` | Stable upstream object ID. Upstream response data may not invent or rename this. |
| `report` | `resource` or `status`; exactly one payload shape follows. |
| `node` | Declared target node. Required; it is the resource/status subject, never the API observer. |
| `max_age_seconds` | `data_deadline = upstream sample time + max_age`. |
| `primary_disk` | Disk ID selected for a resource card. Required when a disk assertion is declared. |
| `assertions` | Optional CPU percent, memory percent and primary-disk percent maxima. |

Existing `service`, `application`, `environment`, `failure_threshold`,
`recovery_threshold` and `policy_threshold` fields keep their normal roles
where they apply. `interval_seconds`, `timeout_seconds` and `stale_seconds` on
an external check must resolve to the source's values; an explicit conflict is
a configuration error. Transport fields (`endpoint`, `target`, `path`,
`bind_ip`, `resolve_ip`, `server_name`, `ca_file`, HTTP/DNS/adapter/host
options) are forbidden on an external check. A declared service must belong to
the binding's node.

`node.resource_checks` is an optional ordered list of resource check IDs for
that node. The first is primary; the rest are fallbacks. If a node has more
than one usable native/external resource source and no explicit list, the
configuration is ambiguous and refused. Existing topologies without external
resources keep their current behavior.

### Report (data ownership)

`Observation` keeps its original collection identity and optional collection
diagnostics. It gains one optional `external` member:

```text
external.source, external.source_generation,
external.attempted_at, external.contact,
external.report
```

`external.contact` says the source read itself succeeded, independent of what
the report says. `external.report` is absent on an attempt that failed. The
source generation is an opaque local value used to reject results from a
cancelled old generation; it is not a measurement contract.

`ReportMeta` (shared by resource and status reports):

| Field | Meaning |
|---|---|
| `schema` | Report schema, currently 1. |
| `object` | Must equal the binding's object. |
| `sample_id` | Optional stable upstream sample identity. |
| `epoch` | Optional upstream boot/epoch identity. |
| `sequence` | Optional upstream monotonic sequence (pointer; 0 is a real value). |
| `observed_at` | Upstream sample time, or the read time only when `time_basis` says so. |
| `time_basis` | `upstream_sample`, `live_read` or `unknown`. |
| `field_status` | Bounded map of canonical field name to `known`, `missing`, `unsupported` or `invalid`. |

`ResourceReport` supports only:

- CPU: optional percent (0..100), logical core count, sample window ms, basis.
- Memory: optional total/used/available bytes, percent, basis.
- A bounded list of disks, each with a stable disk ID, optional display path,
  total/used/free bytes, percent and basis.

`StatusReport` supports an upstream status (`up`, `degraded`, `down`,
`unknown`), an optional upstream response time and an optional upstream
observation point ID. It never proves connectivity by the executing observer.

Any numeric field is a pointer so absence is never encoded as zero. A field
status distinguishes:

- `known`: the value is present and passed normalization;
- `missing`: the upstream object did not carry it;
- `unsupported`: the upstream/version contract says the value cannot exist;
- `invalid`: the response carried it but normalization refused it.

A report is `stale` only when its whole data deadline has passed. A field that
is missing/unsupported/invalid is not a stale report; it is an unknown value
inside a possibly current report. Missing fields never become zero.

## 4. Invariants

1. The executor/observer, API target, resource subject and upstream object ID
   are four identities and are stored separately.
2. An upstream response can never create a topology entity or change a
   binding. Only the reviewed config says which objects are acceptable.
3. A successful read says the source answered. It does not say the target is
   online; a resource read never says an application is reachable.
4. Source authentication, transport, page, parse and normalization errors are
   collection errors. They can make `contact` false but cannot become a target
   failure or a measured outage.
5. External evidence is `reported`. It never enters measured connectivity
   voting, link/path response proof or measured availability.
6. Collection duration and upstream response time are separate fields and
   separate statistics. Upstream latency is never filled with API duration.
7. A duplicate report cannot refresh any data deadline, confirmation count,
   report-sample count or latency sample.
8. A new report with the same values is still a new sample when its identity
   is new. A content hash is never the sample identity.
9. An out-of-order old report is diagnosed and persisted only as a late
   diagnostic. The first phase does not backfill historical rollups.
10. Deduplication cursor, last accepted report, raw validity window and
    confirmation state are persisted. They are not inferred from the raw tail.
11. Unknown values are nullable. No layer writes a missing value as zero.
12. CPU, memory and disk values from different sources are not mixed, summed,
    averaged or spliced in one card.
13. Existing host config, facts and measurement contracts are unchanged.
14. Existing frozen adapter/observation fixtures are not regenerated to hide a
    behavior change.
15. Every public path has explicit size, count, concurrency, timeout and
    persistence budgets; exceeding one is an explicit error.

## 5. Version and upgrade policy

- Topology schema stays 1. New top-level `sources` and new check/node fields
  are additive. An older binary rejects them with strict decoding rather than
  ignoring them, which is the intended explicit failure.
- Wire batch schema becomes 3. A schema 2 batch remains readable and is
  treated as native-only; a schema 2 observation carrying an external report
  is refused. Schema 1 native batches keep their revision-based admission.
  A schema 3 collector rejects unsupported schema 0/1/4 explicitly.
- Snapshot schema becomes 3. The new client reads schema 2 (native-only) and
  schema 3, validating each shape; an old client refuses schema 3. External
  fields are absent in schema 2 snapshots and validated when present.
- The store checkpoint writes on-disk state schema 1 while no external state
  exists. As soon as external report/source state exists it writes schema 2,
  including that state. The new reader accepts both. An old binary refuses
  schema 2 instead of ignoring state and appending behind it. Rollback to an
  old binary therefore requires restoring a matching pre-upgrade backup, as
  documented in task 10.
- Measurement contracts for native checks are unchanged. External contract
  identity includes the binding and the source's measured configuration
  (adapter, target after redaction, route/TLS, budgets, object, report type,
  max age, primary disk and assertions). Display names, notes and credential
  variable names are excluded. Credential values never enter any contract.
- Report schema 1 is additive within the delivery. Unknown report schema or
  report type is refused, not silently parsed.

## 6. Report identity, time, duplicate, conflict and reordering rules

The collector is the only authority for acceptance. A client never supplies
`received_at`, duplicate classification, data deadlines or confirmation.

Identity preference:

1. `sample_id`, scoped by source and object. The persisted recent-ID set
   detects exact replays after raw samples are trimmed. Same ID + same
   canonical content = `duplicate`; same ID + different canonical content =
   `conflict`.
2. `epoch` + `sequence` when no `sample_id` is present. Same pair follows the
   same duplicate/conflict rule. Ordering is by `(observed_at, sequence)`.
3. `observed_at` alone (with a known time basis). Same timestamp + same
   content = duplicate; same timestamp + different content = conflict. A
   newer timestamp = new; an older timestamp = late.
4. A report with no stable ID and an unknown/absent time is `unusable` for
   freshness and confirmation. It may be retained as a bounded diagnostic,
   but cannot move a cursor.

Time basis is not inferred from the collector's wall clock:

- `upstream_sample`: `observed_at` is when the upstream sampled the value.
- `live_read`: the adapter's versioned contract states this read itself is a
  live sample; `observed_at` is the read time.
- `unknown`: no reliable time; the report is not fresh evidence, cannot extend
  a data deadline, and cannot confirm a failure or recovery.

A normal dashboard's fetch timestamp is never silently relabeled as an
upstream sample time. An adapter that only has a cached value must either
report the cache's own sample time or `unknown`.

Ordering:

- A cursor is the accepted report's ordering tuple plus identity.
- Same identity, same content: `duplicate`.
- Same identity, different content: `conflict`; no state advances.
- Identity older than the cursor: `late`; no state advances, no historical
  rollup backfill in this phase.
- New identity ordered after the cursor: `new`; state advances, including when
  every value equals the previous report's value.
- A late/duplicate/conflict/unusable report may update only diagnostic
  counters and source contact when the underlying read succeeded. It cannot
  advance report confirmation, report sample counts, status counts, latency
  samples or `data_deadline`.

First-phase limitation (explicit): late reports are not backfilled into
historical rollups or incident timelines. They are counted as late
diagnostics and retained only as long as ordinary raw history permits.

## 7. Collection state vs report state

These are separate and are persisted separately.

- **Attempt**: one source job. It has start/end, duration, contact success and
  a bounded error code. A failed attempt does not delete the last accepted
  report.
- **Contact**: updated to the newest successful source read. Repeated reads
  update contact even when their report is a duplicate or late.
- **Report**: upstream identity, canonical payload, `time_basis`, raw age and
  acceptance result. Only a `new` accepted report advances it.
- **Data deadline**: `observed_at + binding.max_age_seconds` (only when the
  report is usable and time is known).
- **Contact deadline**: `last successful contact + source.stale_seconds`.
- **View valid until**: the minimum of data deadline, contact deadline and the
  snapshot's own deadline. A cached view is never shown as current.
- **Confirmation**: only new accepted status reports advance status
  failure/recovery runs, using the check's confirmation thresholds. Resource
  reports use a separate policy state and never produce service availability.
- **Statistics**: report sample counts, status up/down counts, policy outcomes
  and upstream latency samples have their own counters. Duplicate/late/
  conflict/unusable counts are diagnostics. The existing native rollups and
  latency histogram do not receive external resource observations.

An expired report or contact deadline makes the current state `unknown`. A
historical failure cannot be turned into a recovery by a read that returns no
new report.

## 8. Dependency direction and module boundaries

```text
model
  ↑
report  --------------------------> no UI, collector, adapter or probe imports
  ↑
config ------> adapters (existing)
  ↑
probe -------> runner
  ↑              ↑
api ----------> store
  ↑              ↑
engine --------> rollup
  ↑
view --------> monitor, tui, client projections
```

- `model` owns public types and wire vocabulary.
- `report` owns normalization, time/deadline arithmetic, resource assertions,
  counter-to-rate helpers and the pure merge verdict. It imports only
  `model`.
- `config` validates topology shape and computes contracts. It must not import
  `runner`, `api`, `store`, `view` or adapters that would create a cycle.
- `probe`/`runner` own execution and bounded IO; adapters own endpoint,
  protocol, version, pagination and field parsing.
- `api` admits and translates a request; `store` persists; `engine` judges.
- `view` derives presentation from a snapshot; monitor/TUI never re-select
  resources independently.

## 9. Initial resource budgets

These are implementation limits, not claims about a vendor.

| Budget | Limit |
|---|---|
| Sources | 64 |
| Bindings / external checks | 512 |
| Bindings per source | 512 |
| Source objects per attempt | 10,000 |
| Source pages per attempt | 20 |
| Source response bytes per page | 1 MiB default, 8 MiB max |
| Encoded report | 64 KiB max |
| Disks per resource report | 64 |
| Field-status entries | 64 |
| Report sample-ID replay set per binding | 256 IDs |
| Source jobs in flight per process | 4 |
| Source wait queue | 128 |
| Outbox fan-out batch | 100 observations and 512 KiB encoded |
| External store state | 4 MiB, inside existing `MaxStateBytes` |
| Report cursor retention | checkpoint lifetime, pruned only with binding |
| Upstream latency statistics | bounded count/sum/buckets in persisted binding state |

Limits that are exceeded produce an explicit collection/admission error. They
are never silently truncated into a successful fresh report. Source-level
concurrency is one in-flight job per source, and the native scheduler retains
its existing worker budget.

## 10. Acceptance matrix

| ID | Requirement | Evidence task |
|---|---|---|
| A1 | Sources/bindings parse strictly, default, clone, redact and validate | task 1 |
| A2 | Old native host contract fixtures unchanged | task 1/10 |
| A3 | Resource/status payload exclusivity, nullable fields and field status | task 1/2 |
| B1 | Units normalized to bytes/ms/percent and invalid values refused | task 2 |
| B2 | Partial resources allowed; required assertion field missing => unknown | task 2 |
| B3 | Time basis respected; dashboard read time is not promoted | task 2 |
| B4 | Counter rate handles first sample/reset/window/series isolation | task 2 |
| C1 | External report only under declared source/check/object/type/contract | task 3 |
| C2 | Forged measured/connectivity, bad time, oversize payload refused | task 3 |
| C3 | Schema 1/2 native batches still work; external in schema 2 fails | task 3 |
| D1 | Pure merge yields new/duplicate/late/conflict/unusable | task 4 |
| D2 | Cursor/last report/raw window/confirmation survive checkpoint+trim | task 4 |
| D3 | Replay and crashes do not double-apply side effects | task 4 |
| E1 | Data/contact/view deadlines computed separately | task 5 |
| E2 | Duplicates do not advance confirmation/counts/latency | task 5 |
| E3 | Source errors never fabricate target failure or recovery | task 5 |
| E4 | External status/resource statistics do not enter native rollups | task 5 |
| F1 | One source read fans out 100 bindings | task 6 |
| F2 | Per-source in-flight/quota/backoff/isolation/budgets | task 6 |
| F3 | Reload cancels old generation and rejects stale results | task 6 |
| G1 | Native host converts to common resources; no host/observer.Node dependency | task 7 |
| G2 | Ordered primary/fallback, no cross-source splicing, explicit disk only | task 7 |
| G3 | TUI/CLI/monitor share `SelectResources` | task 7 |
| H1 | `server.card.resources` first is the explicit primary, bar decodes it | task 8 |
| H2 | Per-resource deadline withdraws numbers while card remains current | task 8 |
| H3 | `resource.*` metrics are catalogued; `host.*` keeps native semantics | task 8 |
| I1 | Synthetic list/counter/status end-to-end through real public chain | task 9 |
| I2 | Duplicate/conflict/late/restart/offline/schema cases named | task 9 |
| I3 | Budget benchmark records measured data sizes | task 9 |
| J1 | Adapter authoring and upgrade/rollback docs complete | task 10 |
| J2 | fmt, vet, race, verify and required generated checks pass | task 10 |

## 11. Task record

| Task | Commit | Status |
|---|---|---|
| 0 base/design freeze | `7f5ebaa` | complete |
| 1 typed model/config | `87d82a4` | complete |
| 2 report normalization | `4a1de5c` | complete |
| 3 collector admission | this commit | complete |
| 4 persistence/dedup | — | pending |
| 5 report state/statistics | — | pending |
| 6 source runtime | — | pending |
| 7 unified resources | — | pending |
| 8 stream/bar | — | pending |
| 9 end-to-end | — | pending |
| 10 docs/full verification | — | pending |

## 12. Adjustments and limitations

Record only real changes to the frozen rules here, with the reason.

- Task 1 keeps `primary_disk` optional so a partial resource report can be
  modelled, but selection never guesses: with no explicit primary disk the
  external disk is unknown. A disk assertion requires `primary_disk`.
- Task 1 leaves external execution deliberately unwired: reaching the native
  probe path reports `external_runtime_unavailable` rather than a successful
  empty sample. Task 6 replaces this with source-level execution.
- Task 2 implements the common layer in `internal/report`. Normalization
  requires at least one resource value, permits any subset of CPU, memory and
  disks, and validates capacity relations with subtraction so addition cannot
  overflow. Field-status entries are optional for present values (present is
  implicitly known) and required only to say missing/unsupported/invalid. A
  contradictory status is refused.
- Task 2's counter helper is runtime-only and bounded by an explicit series
  limit; it never evicts series, because eviction would silently merge
  histories. A reset or long window reseeds and yields no rate.
- Raw `model.ExternalReport` normalization does not itself decide sample
  acceptance; that is the task 4 merge step. Time-unknown reports normalize
  for diagnosis, but `SampleAt`/`DataDeadline` refuse to make them evidence.
- Task 3 bumps the wire batch and snapshot schemas to 3. The collector still
  accepts contracts-bearing schema 2 native batches; schema 2 refuses an
  external member. The new client reads schema 2 native snapshots and schema
  3. The engine's frozen judgment oracle now normalizes only the advertised
  wire schema to its historical value, so all prior digests still verify the
  judgments; a separate assertion checks that schema 3 is advertised. This is
  a deliberate version normalization, not a regenerated oracle.
- Task 3 has no valid external producer yet: the native scheduler rejects an
  external check until task 6 wires source jobs, and a client-supplied external
  observation is subject to the new admission checks. This deliberately keeps
  the intermediate commit from claiming a report that no source runtime read.
- First-phase limitation: late reports are diagnostic-only, not backfilled
  into historical rollups.
- No production vendor adapter is added in this delivery. Synthetic adapters
  are test-only and are injected explicitly by tests; production startup
  refuses a config whose sources have no explicitly wired adapter.
