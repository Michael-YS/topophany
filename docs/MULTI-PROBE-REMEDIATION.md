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

## Resolutions

| Issue | Implementation | Commit | Named regressions |
| --- | --- | --- | --- |
| R1 | Payload-specific field allowlist; empty nested sections refused; bounded normalization fuzz | `f518ec3` | `TestNormalizeFieldStatusIsPayloadSpecific`, `TestNormalizeMalformedPayloadsDoNotPanic`, `FuzzNormalize`, `TestRuntimeRejectsForeignPayloadFieldWithoutPanic`, `TestClientRejectsForeignPayloadFieldInExternalSnapshotWithoutPanic` |
| R2 | New `internal/admission` live window shared by API and `Store.Put`; replay remains reconstruction | `3e0a766` | `TestExternalAdmissionTimeRelations`, `TestLocalCollectorRejectsFutureReportWithoutMovingCursor`, `TestExternalJournalReplayIsNotRejectedByLaterLiveClock`, `TestAPIAndLocalAdmissionAgreeOnExternalTimeBounds` |
| R3 | `Options.WithTopology` installs recovery context before `Open`; stale contracts/bindings are skipped and pruned | `a669541` | `TestServeRecoversExternalJournalBeforeAPIInstallsContext`, `TestRecoverySkipsRemovedBindingAndChangedContract` |
| R4 | Schema 2 barrier checkpoint before first external journal ACK; monotonic format; legacy journal upgrade | `d07192a` | `TestOldBinaryRefusesNewExternalData`, `TestExternalFormatBarrierFailureLeavesNoAck`, `TestLegacyExternalJournalUpgradesBeforeNextAck` |
| R5 | Classify first, mutate accepted state only on `new`; rejected diagnostics only; owned Seen slice | `18879eb` | `TestMergeRejectedVerdictsPreserveAcceptedState`, `TestMergeStatusConfirmationOnlyFromNewValidReports`, `TestMergeDuplicateReplayIsIdempotent` |
| R6 | Epoch transitions, retired epochs, exclusive data-deadline gaps and continuity reset | `a12a885` | `TestMergeNewEpochResetsFailureAndRecoveryConfirmation`, `TestMergeEvidenceGapRestartsConfirmationWithoutFakeRecovery`, `TestMergePolicyGapAndEpochLifecycle`, `TestExternalEpochAndRetiredEpochSurviveRestart` |
| R7 | Resolved ordered proxy chain in private external contract; public contracts recomputed redacted | `7811a42` | `TestExternalContractIncludesResolvedRoute`, `TestExternalPublicContractUsesRedactedRouteOnly`, `TestRouteReloadInvalidatesOldExternalSnapshotsAndOutbox`, `TestRouteReloadRejectsAnOldExternalOutboxBatch` |
| R8 | Typed `source.Request`; `(object, report_type)` result keys; object/report/byte budgets | `c4d007f` | `TestRuntimeCollectsResourceAndStatusForOneObject`, `TestRuntimeRejectsAmbiguousReportRequestsAndResults`, `TestSourceSameObjectResourceAndStatusShareOneRead`, `TestSameObjectResourceAndStatusReportsCoexist` |
| R9 | External reports cloned at the public snapshot boundary; heartbeat path preserves ownership | `00a7855` | `TestExternalSnapshotOwnsEveryMutableReportMember`, `TestExternalSnapshotBuildConcurrentMutation` |
| R10 | Native transactions skip external merge; affected-only clone and delta byte accounting; observer/source quotas | `15928d9`, `5f8b5dd` | `TestNativePutDoesNotCloneExternalState`, `TestExternalMergeClonesOnlyAffectedState`, `TestExternalSourceShareCannotConsumeTheWholeBudget`, `TestExternalReportStateIsChargedToObserverQuota` |
| R11 | One `SelectResources` result per frame shared by scalar and card paths | `e0c4f6c` | `TestResourcePanel128RowsAllocationBudget`, `TestResourceScalarAndServerCardUseTheSameFrameSelection`, `BenchmarkResourcePanel512` |
| R12 | Translated README links, scenarios, fuzz registration and maintenance cleanup | final test/docs commits | `scripts/test_readme.py`, full scenario table |
| R13 | Implementation/remediation records and generated site refreshed | final docs commit | `scripts/test_site.py`, `build_demo.py --check` |

## Verification record

The follow-up review of `4fd2136` invalidated its all-passed claim: automatic
checkpoints omitted external state, migration could race a background
checkpoint, unchanged thresholds could alter confirmation, API/store clocks
diverged on rollback, and the new startup test had a diagnostic-buffer race.
The previously referenced `.verify-build/verification/summary.json` described
`279b74a`, not that hardening commit, and is not evidence for this delivery.

The complete verification below ran against clean code commit `dc8cbd9349a5c32445fb0fd3492374299c44eb07`,
using `go version go1.26.10 darwin/arm64`. Its before/after source fingerprints match.
The subsequent documentation-only update is checked separately by the Python
documentation/source-boundary tests; it does not change the tested Go code.

| Check | Result |
| --- | --- |
| `python3 scripts/verify.py --output .verify-build/multi-probe-followup-final` | pass; `53` commands, no quality-gate failures |
| Format, module integrity and `go vet ./...` | pass |
| Full Go tests with coverage | 3796 passing test events, 0 failures, 6 skips; 86.2% statement coverage |
| `go test -race -count=1 -timeout=15m ./...` | pass |
| `go test -count=3 -timeout=10m ./...` | pass |
| Python source/documentation tests | 58 tests pass |
| Bounded fuzz suites | 17 targets pass at 25000 executions per target, including `FuzzNormalize` |
| Native and linux/darwin/windows amd64/arm64 builds | pass; cross-builds are compilation checks, not deployment tests |
| Actual `74db358` downgrade reader | pass, including after automatic checkpoint installation |
| `make site`, `make site-check` | pass; browser assets rebuilt, no tracked site changes |

The authoritative detailed results are the ignored
`.verify-build/multi-probe-followup-final/summary.json`, `results.json` and
per-command logs. Linux-only process/privilege checks remain skipped on this
macOS host; no production endpoint or credential was exercised.

### Performance comparison

The original hardening measurements are retained for comparison. Same local
machine (Apple M6, arm64, Go 1.26.10), `-benchmem -count=1`.
Timings include local filesystem fsync for `Store.Put`; they are comparative,
not a portable gate.

| Measurement | Before review | After hardening |
| --- | --- | --- |
| Native store write with 512 bindings x 64 Seen | 2,898,115 ns/op, 5,348,925 B/op, 5,130 allocs/op (external merge step) | 2,824,135 ns/op, 6,935 B/op, 32 allocs/op (complete `Store.Put`) |
| 512-node monitor frame, 128 rows | 95,251,479 ns/op, 261,012,670 B/op, 1,263,674 allocs/op | 890,875 ns/op, 3,220,961 B/op, 13,281 allocs/op |
| 512 external resource selection alone | 867,612 ns/op, 1,698,976 B/op, 5,197 allocs/op | unchanged by the monitor cache; kept as the view microbenchmark |
| External merge, 512 bindings, one changed binding | not separately measured | 76,213 ns/op, 374,572 B/op, 566 allocs/op |

B/op is allocated bytes per operation, not resident memory. The remaining
external-merge growth with binding count is the top-level map copy; it no
longer copies each binding's bounded Seen history.

### Follow-up performance

The validated review fixture, with CPU/memory/disk fields and 512 nodes, was
rerun with `-benchmem -benchtime=500ms -count=3 -cpu=1`. The median 128-row
frame was 1.28 ms and 3,789,124 B/op, below the 16 MiB engineering target;
these are allocated bytes, not RSS. Final-code store benchmarks at 0/512
bindings retained 31 allocations per native write and approximately 8–11 KB/op,
including journal fsync (median 2.66/2.88 ms). The earlier follow-up scaling run
at 128 bindings showed the same bounded allocation behavior. The bounded
external top-level map copy remains. Timing is machine/filesystem dependent.

### Security, upgrade and rollback

- No new runtime dependency, no production adapter registration and no
  adjacent repository change. `ProductionSourceAdapters()` remains empty.
- External reports remain `reported` evidence and never claim measured
  connectivity; source errors remain attempts and do not become target
  outages.
- Public contracts are recomputed from the redacted topology. Private
  credential values are never hashed into public output.
- A native-only data directory keeps checkpoint schema 1 and remains readable
  by `74db358`. Before the first external journal acknowledgment the store
  installs schema 2 through the existing atomic-write/fsync path. Schema 2 is
  monotonic, including after source/state removal. Rollback requires restoring
  the pre-upgrade backup; there is no in-place downgrade.
- External state is charged to the global state budget and to the owning
  observer's share. With several sources configured, one source is capped at
  half of the external budget. Capacity refusal happens before journal
  acknowledgment and never evicts accepted evidence.

### Remaining limits and unverified ranges

- No production vendor adapter exists yet, by instruction; the synthetic
  adapters are test-only and are not a production deployment test.
- The external state budget is fixed at 4 MiB. Configurations whose bounded
  Seen/retention needs exceed it are refused explicitly rather than silently
  evicted; this is a real capacity limit, not a dynamic quota.
- Late reports are still not backfilled into historical rollups or incidents.
- Live Windows TUN, real proxy cores, real SSH tunnels, production endpoints
  and live credentials were not exercised. Cross-builds verify compilation
  only.
- The downgrade test needs `git` and commit `74db358` in the checkout; it skips
  with that stated reason in a shallow archive. It is not replaced by a
  regenerated fixture.
- `scripts/verify.py` runs loopback-only acceptance; production monitoring and
  deployment were not accessed.

### Follow-up audit fixes

| Issue | Fix and repository regression | Commit |
| --- | --- | --- |
| Automatic checkpoint lost state/format | Capture an owned external state and format; `TestAutomaticCheckpointPreservesExternalStateAndFormat`, `TestExternalBackgroundCheckpointPreservesConcurrentTail`, old-reader automatic checkpoint case | `bfa8c92` |
| First external write raced an older checkpoint | Wait before preparing/admitting the transaction; `TestExternalMigrationWaitsForNativeBackgroundCheckpoint` | `0d5f12b` |
| Duplicate/restart cleared confirmed policy after a gap | Rejudge only changed effective thresholds; `TestRejectedReportsPreserveConfirmedPolicyAfterGap`, `TestPolicyConfirmationSurvivesRestartAndUnchangedRules` | `98313c2` |
| API/local rollback windows diverged | Share the collector clock under the store lock; `TestExternalAPIAndStoreShareRollbackClock`, `TestLocalExternalAdmissionAdvancesDuringRollback` | `cfcad5c` |
| Startup regression used a racy log buffer | Use synchronized diagnostic writers; existing production-startup test passed five race-instrumented repetitions | `763934d` |
| Wall-clock comparisons could use monotonic annotations | Store the wall bound as UTC and use ticks only for elapsed time; `TestClockRollbackAdvancesWithoutDoubleCounting` | `dc8cbd9` |

### Merge readiness

The full suite on `dc8cbd9` passed, including the previously failing race
test and the new lifecycle regressions. The five follow-up review reproductions
also pass. The deliberately unsupported production-vendor and platform ranges
above remain unchanged. Future changes require verification against their own
source revision; the older generic verification directory must not be reused
as evidence for this branch.

## Commit log

| Task | Commit | Summary |
| --- | --- | --- |
| 0 | `976dd18` | Baseline, issue freeze and remediation plan |
| 1 | `f518ec3` | Payload-compatible field rejection and normalization fuzz |
| 2 | `3e0a766` | Shared live admission for API and local collector |
| 3 | `a669541` | External recovery context before journal replay |
| 4 | `d07192a` | Durable external checkpoint format barrier |
| 5 | `18879eb` | Accepted-state isolation from rejected reports |
| 6 | `a12a885` | Epoch and evidence-gap confirmation lifecycle |
| 7 | `7811a42` | Resolved route content in external contracts |
| 8 | `c4d007f` | Object/report-type source requests and fan-out |
| 9 | `00a7855` | Detached external reports in snapshots |
| 10 | `15928d9` | Incremental external state on store writes |
| 10a | `5f8b5dd` | External state charged to observer quotas |
| 11 | `e0c4f6c` | Per-frame resource selection cache |
| 12 | `e86d22a` | Scenarios, replay idempotence and multi-report identity |
| 13 | `4fd2136` | README links and initial records; verification superseded above |
| 14 | `74afa41` | Collector clock step-back during a source read |
| 15 | `b1113b6` | Remove unreachable epoch revival helper |
