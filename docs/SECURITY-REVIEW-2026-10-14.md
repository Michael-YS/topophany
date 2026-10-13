# Topophany Security and Reliability Review

On October 14, 2026, a source code review and local verification were conducted across API authentication, configuration decoding, network probing, external tool adapters, persistent storage, outbox queues, scheduling, state derivation, terminal handling, and release packaging scripts. The issues itemized in the table below were resolved in the workspace, and executable binaries were rebuilt using Go 1.26.9.

The foundational threat model remains applicable: audited local configurations, the host operating system, and local processes operating under the same user privileges are trusted; worker tokens represent their designated observer identity but cannot physically attest to executor integrity. The confirmed issues encompass both security boundary defects and reliability edge cases that could lose observation results or synthesize incorrect health states.

## Itemized Fixes

Priorities indicate remediation order rather than active exploitation in production environments. **P1** indicates issues affecting isolation boundaries, process availability, or crash recovery; **P2** indicates protocol interpretation, delivery reliability, or platform parity; **P3** indicates input contracts and defensive handling of diagnostic telemetry.

| Priority | Issue and Trigger Condition | Remediation | Verification |
| --- | --- | --- | --- |
| P1 | Go standard library scanner identified 14 reachable vulnerabilities in Go 1.26.5 spanning HTTP/2, TLS, URL parsing, ASN.1, etc. | Minimum build requirement updated to Go 1.26.9; binaries rebuilt; automated vulnerability scanning incorporated | Post-fix source scan confirms zero known reachable vulnerabilities |
| P1 | Observer `home` and boot identifier `branch.boot` could concatenate into observation ID `home.branch.boot`, colliding with observer `home.branch` during deduplication | Disallowed dots within observation `boot` strings while continuing to allow dots in observer IDs | `TestObserverIdentitySeparatorCannotCrossScopes` |
| P1 | Restricting only history line counts allowed large facts to accumulate state checkpoints exceeding the 256 MiB recovery ceiling | Added a conservative 128 MiB state byte capacity budget, rejecting oversized transactions before journal commit; verified checkpoint serialization bounds | `TestStateByteCapacityRejectsBeforeAcknowledgement` |
| P1 | Checkpoints retained stale tail state whose deduplication receipts had expired; crashing after checkpoint write but before journal truncation could duplicate tail recovery | Recovery reconstructs deduplication indices for retained entries; duplicate writes with identical IDs remain rejected while expired receipts release capacity | `TestExpiredTailCheckpointCrashReplayIsIdempotent`, `TestExpiredReceiptCannotReplaceRetainedEvidence` |
| P2 | DNS label parsing conflated dot characters inside labels with label boundaries, risking association of unrelated owner records; CNAME parsing permitted trailing garbage bytes | Strict validation rejecting ambiguous label representations; enforce exact byte consumption matching RDLENGTH | `TestDNSRejectsLabelBoundarySpoof`, `TestDNSRejectsTrailingCNAMEData` |
| P2 | Oversized DNS answers, excessive provider counts/keys, and 4-byte UTF-8 sequences could exceed collector API limits, permanently quarantining records | Unified field budgets across config and probe layers; surface explicit budget errors; truncate diagnostic strings at UTF-8 boundaries | `TestDNSLargeAnswerProducesDeliverableError`, `TestSafeFactRespectsByteBudget`, `TestProviderEvidenceBudgetValidatedAtLoad` |
| P2 | OpenSSH `known_hosts` parsing with Go string escaping caused mismatched parameter interpretation on Windows backslashes | Normalized paths to absolute representations, standardized Windows path separators, rejected dangerous expansion syntax, and used single-file quoting | Real `ssh -G` configuration resolution test; subprocess bridge regression suite |
| P2 | Single-hop SSH TCP probes reported immediate success upon subprocess spawn before authentication or remote connection completed | Returns explicit `unknown / ssh_forward_unverified`, requiring responsive TLS, HTTP, or SSH banner checks for reachability proof | `TestSSHJumpProcessBridgeAndStrictArguments` updated assertions |
| P2 | Windows atomic credential and state file replacements intermittently encountered `Access is denied` sharing violations | Added bounded exponential backoff retries for transient sharing violations; retries atomic replacement without deleting original files | `TestConcurrentAtomicWritesAreWhole` (passed 10x consecutive runs) |
| P2 | Time-of-check to time-of-use race condition in `init` directory creation allowed concurrent runs to overwrite credentials | Enforced atomic directory creation to ensure a single winner | `TestConcurrentInitHasExactlyOneWinner` |
| P2 | A single expired or invalid outbox record caused entire batched requests to be rejected, needlessly quarantining valid adjacent records | Upon aggregate permanent rejection, outbox items are retried individually, isolating only the defective entry | `TestRejectedAggregateDoesNotQuarantineValidNeighbour` |
| P2 | Journal recovery checked the 2 MiB limit only after reading full lines; outbox restart recovery used unbounded reads after file size checks | Implemented bounded buffered reading for journal; applied streaming limits and early aggregate checks during outbox recovery | `TestJournalRecordBudgetDuringRecovery`; outbox corruption and budget test suites |
| P2 | When path A depended on path B, and B depended on failing path C, path A dropped transitive impact indicators | Propagated transitive `Affected` flags while keeping unmeasured downstream health states unknown | `TestPathDependencyImpactPropagatesTransitively` |
| P2 | IPv4-mapped IPv6 egress addresses caused matching IPv4 CIDR assertions to incorrectly report policy failures | CIDR matching evaluates both original and unmapped IPv4 addresses | `TestMappedIPv4ExitMatchesIPv4CIDR` |
| P3 | Diagnostic keys (`facts`, `timings`, `PathEvidence`) did not strip terminal escape sequences; `host:port` missed certain control delimiters | Standardized safe text sanitization across all diagnostic fields; rejected control codes and whitespace in host fields | `TestAllDiagnosticFieldsRejectTerminalControls`, `TestHostPortRejectsControlDelimiters` |
| P3 | HTTP `Content-Type` headers checked only prefix matching, allowing `application/jsonp` to reach JSON handlers | Implemented strict MIME media type parsing requiring exact `application/json` match | `TestJSONMediaTypeMustMatchExactly` |
| P3 | Adapter public test URLs sanitized credentials and query parameters but retained URL fragments containing sensitive tokens | Stripped URL fragments from sanitized adapter URLs | `TestAdapterURLFragmentsAreRedacted` |

Additionally, batch validation now indexes check definitions once to avoid redundant full-configuration scans per observation. CI workflows incorporate Windows test runs alongside the full Linux acceptance suite.

## Standard Library Vulnerabilities

Prior to remediation, `govulncheck v1.9.0` reported 14 reachable vulnerabilities under Go 1.26.5. Following the upgrade to Go 1.26.9, scanning reports `No vulnerabilities found`. Reachability is established via static call-graph analysis and does not imply every issue was exploitable under default project configurations. The official [Go 1.26.9 Release Notes](https://go.dev/doc/devel/release#go1.26.9) detail the security fixes included in that release.

| Advisory | Identified Vulnerability | Fixed Version |
| --- | --- | --- |
| [GO-2026-6603](https://pkg.go.dev/vuln/GO-2026-6603) | HTTP/2 trailer memory exhaustion | 1.26.9 |
| [GO-2026-6617](https://pkg.go.dev/vuln/GO-2026-6617) | HTTP/2 HPACK encoder race causing server crash | 1.26.9 |
| [GO-2026-6611](https://pkg.go.dev/vuln/GO-2026-6611) | Excessive CPU consumption via repeated HTTP/2 initial window adjustments | 1.26.9 |
| [GO-2026-6612](https://pkg.go.dev/vuln/GO-2026-6612) | Duplicate return of HTTP/2 stream flow control quota | 1.26.9 |
| [GO-2026-6613](https://pkg.go.dev/vuln/GO-2026-6613) | Connection desynchronization following HTTP/1 server CONNECT response | 1.26.9 |
| [GO-2026-6605](https://pkg.go.dev/vuln/GO-2026-6605) | Connection desynchronization following HTTP/1 client CONNECT rejection | 1.26.9 |
| [GO-2026-6610](https://pkg.go.dev/vuln/GO-2026-6610) | HTTP/2 client accepts malformed frame headers | 1.26.9 |
| [GO-2026-6607](https://pkg.go.dev/vuln/GO-2026-6607) | TLS ECH outer extension reference validation | 1.26.9 |
| [GO-2026-6608](https://pkg.go.dev/vuln/GO-2026-6608) | MIME header parsing memory limit bypass | 1.26.9 |
| [GO-2026-5026](https://pkg.go.dev/vuln/GO-2026-5026) | Punycode label validation bypass | 1.26.6 |
| [GO-2026-6218](https://pkg.go.dev/vuln/GO-2026-6218) | Quadratic complexity in URL path resolution | 1.26.6 |
| [GO-2026-6090](https://pkg.go.dev/vuln/GO-2026-6090) | Post-handshake message rate limiting in TLS | 1.26.6 |
| [GO-2026-5972](https://pkg.go.dev/vuln/GO-2026-5972) | Recursion depth limit in ASN.1 decoding | 1.26.6 |
| [GO-2026-6089](https://pkg.go.dev/vuln/GO-2026-6089) | Read timeout during unencrypted HTTP/2 detection | 1.26.6 |

## Verification Evidence

- Windows Full Test Suite: PASS.
- Windows Critical Package Repeat Suite: `secure`, `api`, `store`, and `outbox` executed 10x consecutive iterations, PASS.
- Linux Full Acceptance Summary: 35 acceptance commands executed and passed; statement coverage 84.8%; includes full test pass, race detector, 3x repeat tests, 8 time-bounded fuzz targets, and cross-compilation builds for Linux, Windows, and macOS.
- Process and PTY Smoke Acceptance: Dual collectors, worker ingestion, login, 7 PTY views, failover, state expiration, and crash recovery passed.
- Supplementary store boundary tests: Windows Store Re-verification and WSL Linux Store Race Re-verification passed.
- Vulnerability scans (pre-fix report, post-fix source scan, rebuilt binary scan, binary metadata): source and binary scans confirm zero known vulnerabilities.
- A source and binary SHA-256 manifest was produced for distribution verification.

The Windows host lacked the CGO toolchain required for the Go race detector; race detection was verified on WSL Ubuntu. The Linux live raw socket ICMP test was skipped due to unprivileged environment; ICMP packet encoding, checksums, and loopback simulation tests passed. OpenSSH tests verified argument normalization and subprocess bridging without running live cryptographic sessions. macOS and ARM64 builds verify cross-compilation without native OS runtime execution.

The raw logs and manifests behind this evidence were produced locally during the review and are not tracked in the repository; current results are reproducible with `scripts/verify.py` and are published by CI as the `verification` workflow artifact.

## Operational Impact

Redeployments require rebuilding from source with Go 1.26.9 or deploying the rebuilt binary. Existing running processes are not restarted automatically.

Custom worker `observation boot` strings must not contain dots; built-in workers require no migration. A single adapter check enforces explicit limits on provider counts and name lengths. Single-hop SSH TCP checks report unknown status and should be updated to protocol checks that elicit responsive application evidence. Persistent storage and outbox queues emit explicit errors upon reaching budget limits rather than dropping unacknowledged data.

This report summarizes confirmed and remediated issues and the empirical scope of verification. It does not constitute an exhaustive guarantee against unidentified vulnerabilities or arbitrary production environments.
