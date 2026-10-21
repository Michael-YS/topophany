# Multi-probe hardening remediation

This document tracks the review of `codex/multi-probe-infrastructure` at
`28c3f701e32ab4664dd4a9f0210f124f6cf6962b`, its baseline `74db358`, and the
repairs on `codex/multi-probe-hardening`. It is an engineering record, not a
second implementation contract: `docs/MULTI-PROBE-IMPLEMENTATION.md` and
`docs/EXTERNAL-ADAPTERS.md` remain the design authorities.

The review evidence was a set of overlay tests and logs under
`/tmp/topophany-review-20261021/`. They were used only to reproduce and
understand the failures. No review fixture, generated data directory or binary
is committed or required by the repository's tests.

## Baseline

Recorded at the start of the hardening branch:

- Go: `go1.26.10 darwin/arm64`, local Apple silicon.
- Existing focused suite:
  `go test ./internal/report ./internal/source ./internal/engine ./internal/config ./internal/store ./internal/monitor ./internal/runner ./internal/api ./internal/integration`
  passed on `28c3f70`.
- The reviewer's overlay tests failed on the same commit for all ten code
  issues below (`regressions.log`, `downgrade.log`, `script-checks.log`), while
  the pre-existing tests passed. That gap is the reason each item now has a
  named repository regression, not only a unit test of a helper.
- The reviewer's benchmark logs were taken with the same Go version and
  `-benchmem`; later comparisons in this document use the same machine and
  parameters, but timings are indicative rather than a portable gate.

There is no repository-level `AGENTS.md`; `CONTRIBUTING.md` and
`internal/adapters/README.md` were read directly. The worktree was clean before
the branch was created.

## Issues and acceptance conditions

Numbers are stable references used by the regression cases in
`docs/SCENARIOS.md`.

### R1. Report field validation panics on a foreign payload field

- Trigger: a status report carries `field_status["disk.root.percent"]` while
  `Resource` is nil.
- Boundary: `report.Normalize`, the source runtime, API admission and snapshot
  decoding all call the same field walk.
- Acceptance: fields are checked against a payload-specific whitelist; empty,
  double, unknown and illegal payloads are refused without panic; fuzzed
  bounded input never panics and accepted output re-normalizes to itself.

### R2. Local ingestion admits what the API refuses

- Trigger: a local collector writes a report sampled 24 hours in the future
  directly through `Store.Put`; its cursor then rejects valid older samples.
- Boundary: observation time, attempt time, report sample time, live evidence
  relations, evidence kind, identity and encoded bounds.
- Acceptance: API and local store call one live-admission function with
  explicit clock/skew/window inputs; rejecting a future report leaves the
  cursor and counters unchanged; journal replay is not re-judged as live.

### R3. Production startup cannot replay an external journal

- Trigger: `app.serve` opens the store before `api.New` installs the bindings;
  reopening after an external write fails while replaying the journal.
- Boundary: `app.serve` initialization order, journal recovery, contract and
  binding changes.
- Acceptance: production order installs the recovery context before
  `Store.Open`; the real `serve` path reopens successfully after an uncheckpointed
  external write; old-contract or removed-binding records neither block
  startup nor enter current state.

### R4. An older binary silently reads and rewrites the external format

- Trigger: the old `74db358` store opens a data directory whose first external
  record is still only in the journal; it ignores the unknown JSON member and
  rewrites a schema 1 checkpoint.
- Boundary: checkpoint format, journal acknowledgement, migration and
  rollback.
- Acceptance: before the first external journal record is acknowledged, a
  schema 2 checkpoint is installed through the existing atomic write and fsync
  path; the barrier is monotonic even after sources/state are removed; a
  binary built from the fixed old commit refuses the directory. Native-only
  directories remain schema 1.

### R5. Rejected reports mutate confirmation state

- Trigger: a conflict with a different `Basis` clears confirmed failure state
  before the duplicate/conflict check runs.
- Boundary: `report.MergeBinding`.
- Acceptance: identity, content, order, time and usability decisions complete
  before any accepted-state mutation. Only `new` changes the cursor, last
  report, deadline, basis, confirmation/runs and sample statistics. Rejected
  verdicts advance diagnostics only and do not mutate the input state.

### R6. Epoch and evidence-gap continuity are wrong

- Trigger: a new epoch inherits the previous `down` run; two failures an hour
  apart under a 60-second `max_age` count as consecutive.
- Boundary: cursor ordering, sample identity namespaces, runs, confirmation
  threshold application.
- Acceptance: a newly accepted epoch resets runs and confirmations before its
  first report is counted; an accepted report separated from the previous
  evidence by more than `max_age` restarts runs without fabricating recovery;
  old epochs and replayed old samples cannot switch current state; historical
  accepted-sample counters are never rewound.

### R7. External measurement contracts omit the actual route

- Trigger: changing a source path's proxy address leaves the external contract
  hash unchanged.
- Boundary: `config.Contract`, `config.Contracts`, `PublicContracts`.
- Acceptance: the private contract includes the resolved ordered proxy chain
  (and its TLS/routing parameters) from the source path; name/notes/credential
  variable changes do not reset it; public contracts are recomputed from the
  redacted topology and disclose neither private hashes nor secrets; native
  frozen contract fixtures do not change.

### R8. One object cannot supply two report types

- Trigger: an adapter returns resource and status readings for the same object
  bound by two checks; the runtime de-duplicates by object only.
- Boundary: `source.Request`/`Reading`, runtime validation, runner fan-out and
  source budgets.
- Acceptance: requests and result indexes are keyed by object and report type;
  one collection fans out to both bindings; duplicate same-type results are
  refused; unsupported or mismatched types are explicit; objects, reports and
  encoded bytes have separate budgets.

### R9. Public snapshots alias the prepared external cache

- Trigger: mutating the `Report` of one `Build` result changes the cached
  `Prepared` state and every later snapshot.
- Boundary: `engine.Prepared`, `engine.Build`, external views and monitor views.
- Acceptance: every mutable member of a published external report (including
  pointers, maps, disks, field status and sequence) is detached at the public
  boundary; concurrent `Build` and mutation are race-free; persisted state is
  unchanged.

### R10. Native writes copy and serialize the entire external state

- Trigger: `Store.Put` of a native observation calls the full external merge
  because it keys on `len(kept) > 0`.
- Boundary: `Store.Put`, external-state candidate building, byte accounting,
  checkpoint and quotas.
- Acceptance: native-only transactions do not visit external state; external
  transactions clone and update only affected sources/bindings and account
  bytes incrementally; durability, cursor advancement and replay invariants
  are unchanged; one source cannot silently consume the whole external budget.

### R11. The monitor recomputes resource selection for every row

- Trigger: 128 `resource.*` rows over 512 nodes call `SelectResources` 128
  times in one frame.
- Boundary: `monitor.Build` projection, scalar resource rows and `server.card`.
- Acceptance: one selection per frame is shared by both paths with the same
  snapshot and clock; a 512-node/128-row frame allocates well below the
  reviewer's 261 MB; expiry, fallback, partial fields, disk choice and output
  semantics are unchanged.

### R12. Translation documentation check

- Trigger: `README.zh.md` and `README.ja.md` omit the two new documentation
  links present in `README.md`.
- Acceptance: both translated READMEs list the same documentation entries and
  `scripts/test_readme.py` passes.

## Commit log

Each row is a complete, tested change. The final verification refresh is in
the last row.

| Task | Commit | Summary |
| --- | --- | --- |
| 0 | _this commit_ | Baseline, issue freeze and remediation plan |
