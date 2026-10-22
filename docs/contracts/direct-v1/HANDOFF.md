# Direct v1 handoff

Delivered locally on 2026-10-22. G0–G4 implementation and the Go-side D1
acceptance are complete. G5 verification passed with the explicit platform and
opt-in exclusions below. Nothing was published, pushed or deployed. No real Hub
or user credential file was accessed. All configuration/network acceptance used
isolated temporary roots and literal-loopback synthetic Hubs.

## Exact source and binary

Repository: `/Users/whiten/Desktop/topophany`; branch: `main`.
HEAD at verification: `7e7fdb11358fd8c49b07ac7e840a2cbe72826f0b`.
At verification the worktree was **dirty and uncommitted**: the direct implementation, tests,
documentation, fixtures and generated site asset references are local changes.
The initial worktree was clean; no preexisting user edits were overwritten.
The implementation was subsequently submitted as a local Git commit at the user's
request. Resolve that commit with `git log -1 --format=%H`; the exact verified
runtime source and binary remain identified by the hashes below.

| Artifact | SHA-256 |
|---|---|
| Runtime source, 744 files | `7db239d5e1f053733f22ec749232fd65b8ff40ec4c7b287ebf7215a86657560c` |
| `/Users/whiten/Desktop/topophany/bin/topo` | `6deb1e1be747cc48ec9ef69d77ade842abbf3fa6a654fed4e2b4e4885d9366c9` |
| `docs/contracts/direct-v1/manifest.json` | `63e7ad110dff62927f4eca7f4516ae678d65596412097412eb8790f51d3cdbd7` |

Runtime-source hashing sorts every regular file under `cmd/` and `internal/`,
plus `go.mod` and `go.sum` if present, by repository-relative POSIX path. Feed
each UTF-8 path, NUL, exact file bytes, NUL into one SHA-256. This includes tests
and embedded catalogs; it excludes this self-referential handoff and build output.
The binary was built with `make build` / `go build -trimpath` using
`go version go1.26.10 darwin/arm64`. It is a native macOS arm64 executable.

The manifest owns **28 exact fixture files**, including **14 stream frames**.
Consumers must copy the canonical package and validate every manifest entry;
they must not repair fixture bytes independently. Serialization tests use the
fixed evaluation clock `2026-10-21T12:00:00Z`. See contract.md for versions,
scenarios, clocks, error semantics and all budgets.

## Verification evidence

All commands below exited zero:

```sh
cd /Users/whiten/Desktop/topophany
make fmt-check
make build
make site
python3 scripts/verify.py --output /tmp/topophany-d1-final-verify-g_bwqrdl
python3 scripts/direct-contract.py --topo /Users/whiten/Desktop/topophany/bin/topo --out /tmp/topophany-d1-final-harness-20261022
git diff --check
```

The full verify run took 533.388 seconds: **58 command checks, zero failures**,
85.6% full-module statement coverage. Its JSON test pass has 1,222 passing
top-level tests, zero failures and six skips; 4,114 terminal pass events include
subtests/seeds and are not independent feature counts. It includes:

- `go vet ./...`, full `go test -count=1 ./...` with coverage,
  `go test -race -count=1 ./...`, and full tests repeated three times.
- 21 bounded fuzz targets, 25,000 executions per target.
- Native build and Linux/Windows/macOS amd64/arm64 cross-builds; example,
  monitor-file, help, source/release-boundary and source-identity checks.
- The source identity was unchanged throughout verification. Only this handoff
  documentation was finalized afterward; runtime source and binary are unchanged.

Detailed evidence remains at
`/private/tmp/topophany-d1-final-verify-g_bwqrdl/summary.json`,
`results.json`, `FINAL-TEST-REPORT.md` and their per-command logs.

The real-binary D1 harness passed all **seven** scenario groups in 11.033s:
configuration inspect/save/list/CAS; stream/loading/ready/projection/retry;
multi-source isolation and reconfiguration; all current CLI view shapes, exit
codes, history refusal and monitor-file restart; unchanged-record expiry and
network recovery; real PTY TUI; slow stdout/EOF/EPIPE/SIGINT/SIGTERM shutdown.
Twenty selected objects made exactly 20 resource GETs on first collection and
20 after controlled explicit retry (40 total). Template changes did not add
resource GETs. First loading frame: 0.006s; first ready frame: 1.009s on loopback.
These timings are measurements, not promises for an arbitrary Hub/network.
Results and sanitized artifacts are at
`/private/tmp/topophany-d1-final-harness-20261022/results.json` and its sibling
`capabilities.json`, `profiles.json`, `stream.json`.

Unit/race coverage also verifies four auth modes; empty/truncated discovery;
403 versus renewable 401, MFA, TLS and offline diagnostics; hidden/invalid
selection; profile/source/template and byte budgets; primary disk selection;
save/renewal write failures; two-process refresh coalescing; removal versus late
refresh; real receipt times, configured thresholds, Store merge parity,
generation fencing, atomic failed batches, bounded state rejection, retry nonce
replay/debounce/Retry-After, and concurrent snapshot/record/reconfigure/close.
Canary-secret assertions cover command stdout/stderr, generated public config
and serialized artifacts.

## Final acceptance checklist

| Requirement | Result / evidence |
|---|---|
| Current TUI/JSON/status/show/monitor-file/stream without collector | Passed real binary/PTY harness; process-owned sessions, no metrics daemon/database |
| Stable identities, origin, values and separate evidence/contact/publication clocks | Passed fixed-clock projection/Store-parity/serialization tests; processes intentionally do not share samples or confirmation counts |
| Template changes preserve acquisition; controlled retry; source isolation; old samples expire | Passed request-count, multi-source, expiry/recovery and nonce tests/harness |
| Save/switch/remove/auth updates fail explicitly without secret overwrite races | Passed CAS, injected storage failure, two-process renewal and late-delete tests |
| 20-object page/request budget, root disk, bounded first wait/memory/JSON | Passed generated-budget, actual 20-object runtime, admission/state-budget and deadline tests |
| Existing modes and D1 consumer interface | Full Go client/preview/worker/collector tests passed; history refuses direct; canonical manifest and real consumer probe passed |

## Exclusions and known limits

The six preexisting test skips are explicit and were not counted as passes:
`TestPrivilegedInstallRejectsIntermediateLinksBeforeMutation` (root required),
`TestSystemdUnitPathsAreUnquoted` (systemd-analyze unavailable),
`TestPrivilegedRollbackRejectsForeignOwner` (root required),
`TestICMPRealLoopback` (raw socket unavailable),
`TestCollectorCheckpointMemory` (opt-in operator profile), and
`TestPreviewDump` (opt-in frame export).
The old `scripts/smoke.py` is Linux-only and reported skipped on this macOS host;
its collector/worker Linux process/PTY scenario was **not executed**. Existing
Go regression tests ran, and the separate direct harness's macOS process/PTY
scenarios passed. Cross-builds prove compilation, not native Windows/Linux
runtime behavior. No test was removed to obtain a pass.

Authentication is tested against synthetic PocketBase/Beszel endpoints; no live
Hub/version-wide production acceptance is claimed. Supported resource mapping
is Beszel v0.21.0 and the documented four-integer `mem` schema. Complete MFA/SSO,
historical metrics and mixed collector/direct reads are outside D1. Private
unreferenced credential files are retained after replacement/uncertain commits;
there is no automatic garbage collector that could delete a referenced secret.

## Public interfaces

- `topo sources list --json`; inspect/save/remove --json-stdin, configuration API 1.
- `topo monitor stream --capabilities`, protocol/schema 1 with direct capability.
- Direct requests require profile; profile_revision is an installation fence.
- Running frames identify profile/profile_revision/dataset_id; rejected requests
  do not advertise an installed dataset. See contract.md and manifest.json.
- `topo --source direct --profile ID` supports TUI and --json; status/show keep
  their existing view shapes. History explicitly refuses direct.
- `monitor configure/run` support direct; other file/template commands retain
  their previous meanings. Every direct entry supports --direct-root ABS_PATH.

## Synthetic Hub

```sh
d1_hub_dir="$(mktemp -d /tmp/topophany-d1-hub.XXXXXX)"
python3 /Users/whiten/Desktop/topophany/scripts/direct-contract.py --serve-fixture-hub --ready-file "$d1_hub_dir/ready.json"
```

It binds literal 127.0.0.1 and writes synthetic login data to a new 0600 ready
file. SIGINT/SIGTERM stops it. Options and controlled scenarios are in
../../DIRECT.md. No real Hub/credential access is part of acceptance.

## Bar boundaries observed

The original Bar repository has not been edited. Temporary source copies with
canonical Go fixtures were used. The full package could not build because this
machine lacks xcstringstool; Foundation test compilation also lacks TestingMacros.
These are **unpassed Bar test runs**, not successful UI acceptance.

A separate temporary executable compiled all 25 unchanged TopophanyKit and
TopophanyEngine Swift production files from the latest inspected Bar source.
It passed manifest/hash verification, all 14 stream fixture variants, preservation
of integer 9007199254740993, and real SourcesClient inspect/save/list/remove plus
ProcessEngine loading/ready/resource cards against the final binary. Evidence:
`/var/folders/xf/zl7r7pg97xzdj6c3w3pfqkq40000gn/T/topophany-d1-latest-bar-prsx3xa7/probe.log`.
The tested Swift core-source SHA-256 is
`7fca08830629fb05a7daa2da609defeb00d2c5b75af53cd314c66b2b0d52f921`, using
the path/NUL/bytes/NUL algorithm above relative to `Sources/`, limited to
TopophanyKit and TopophanyEngine `.swift` files. This is core integration evidence;
Bar's full tests, UI and WidgetKit visual/timeline acceptance remain unverified.

The canonical stream-request-error fixture intentionally omits dataset_id and
source installation metadata. Bar's current DirectContractTests assertion that
all stream-* fixtures must have a nonempty dataset_id should be restricted to
running frames. D1 requires rejected requests to retain the previous running
request; an error must not pretend a new dataset was installed. The production
Bar decoder accepts this fixture. No D1 field has been renamed for compatibility.
