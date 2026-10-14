# Scenario Contracts and Test Mapping

This document lists the finite, verifiable scenario contracts for version 0.2.0, deliberately avoiding unsubstantiated claims that "all conceivable network failure scenarios are verified." The unit and integration test cases below reproduce software mechanics under controlled conditions; live operational deployments require separate hardware acceptance per [ACCEPTANCE.md](ACCEPTANCE.md). Automated execution evidence is produced by `scripts/verify.py` and published as a CI artifact.

| Scenario and Invariant | Automated Test Evidence |
|---|---|
| Native host and virtual containers can share IP addresses while maintaining distinct asset identities; loopback endpoints must bind to specific observers | `TestConfigIdentityAndStrictDecode`, `TestConfigRejectsBrokenTopology`, `TestDefaultTimingAndScope` |
| Malformed references, duplicate IDs, dependency / parent-child / candidate routing loops, and invalid port/timing/protocol combinations must be rejected at validation | `TestConfigRejectsBrokenTopology`, `FuzzDecodeTopology` |
| Changing test URLs, methods, assertions, or target contracts produces a new configuration revision; historical metrics cannot blend into the new revision contract | `TestRedactionIsDetachedAndRevisionChangesWithMeasurement`, `TestHeartbeatDoesNotRefreshCheckAndForeignRevisionIgnored` |
| Multiple failures from a local observer toward a transit gateway while cloud observers observe success: aggregate by observer; do not assert whole-server outage or blanket network drop | `TestSourceScopedFilteringNotServerDowntime` |
| TCP connection establishment (TCP accept) does not prove remote application health or proxy chain integrity | `TestTCPAcceptAndReportedTelemetryNotHostProof`, `TestStreamEvidenceIsProtocolSpecific`, `TestRuntimeSelectionAndTCPAcceptNeverProvePathRecovery` |
| Service outages, path failures, unmonitored registered entities, and transitive dependency impacts are reported distinctly | `TestServiceDependencyImpactAndRemarks`, `TestEveryTabFrameBoundsAndDetails` |
| Failure thresholds, recovery thresholds, sampling gap resets, stale timeouts, and monitor-side errors are handled independently | `TestConfirmationRecoveryFreshnessAndMonitorErrors`, `TestOutOfOrderAndGapDoNotFabricateConfirmation`, `FuzzStateNeverPromotesExpiredSample` |
| Observer heartbeats do not refresh stale check results; an observer going offline does not mark remote assets down | `TestHeartbeatNotANewMeasurement`, `TestStoppedObserverIsUnknownNotMassOutage`, `scripts/smoke.py` |
| Passing HTTP transport fallbacks while proxy authentication fails must not conflate the fallback response with proxy tunnel success | `TestProxyFailureDoesNotFallbackDirect`, `TestSOCKSRemoteDNSAndAuthenticationRejection`, `TestStreamEvidenceIsProtocolSpecific` |
| An answer from an edge in front of an origin, even an error page such as Cloudflare's 521, proves the edge only; the origin counts a passing response the edge did not serve from its cache; edge headers are recorded as reported, bounded labels | `TestEdgeAnswersDoNotProveTheOrigin`, `TestHTTPRecordsTheEdgeThatAnswered`, `TestEdgeFactsFromEachNetworkAndUntrustedValues`, `TestEdgeAnswersForTheOriginBehindIt` |
| Ingress, transit, and egress hops connect in sequential order; candidate routes represent alternative paths rather than additional hops | `TestRealThreeHopConnectSOCKSConnect`, `TestPathCandidatesAndNotesAreNotFabricatedHops` |
| External black-box proxy pools remain internally unobservable; unknown routing groups, nested proxies, or reference loops must not fabricate deterministic hops | `TestProxyGraphCyclesUnknownGroupsAndOpaqueProvider` |
| `dialer-proxy` chains expand recursively in proper order; unverified routing hops are marked as reported telemetry | `TestMihomoReadsOnlySafeEndpointsAndExpandsDialerProxy`, `TestFallbackSelectionNotEndToEndRecovery` |
| A change in routing group selection without end-to-end verification cannot declare end-to-end service restoration | `TestFallbackSelectionNotEndToEndRecovery`, `TestRuntimeSelectionAndTCPAcceptNeverProvePathRecovery` |
| Primary path fails while candidate path succeeds: report candidate availability and degraded redundancy, but do not claim production traffic has rerouted | `TestFallbackSelectionNotEndToEndRecovery`, `TestPathCandidatesAndNotesAreNotFabricatedHops` |
| Runtime routing mode drifts from expected rule mode; provider member drift; historical test URLs and sample times are never rewritten | `TestMihomoModeProviderDriftAndAPIAuth`, `TestMihomoReadsOnlySafeEndpointsAndExpandsDialerProxy` |
| Disallow calling side-effecting delay benchmarks, exhaustive connection dumps, or administrative APIs under the guise of safe read requests | `TestMihomoForbiddenReadAndUnknownSchema`, `TestRolesAndNoManagementSurface` |
| HTTP connectivity succeeds but egress IP violates policy constraints: report policy failure while preserving connectivity facts | `TestHTTPAssertionsAndExitPolicy`, `TestProxyGraphCyclesUnknownGroupsAndOpaqueProvider` |
| HTTP status codes, body regex, headers, body size limits, redirects, cancellation, and new connection streams are evaluated individually | `TestHTTPAssertionsAndExitPolicy`, `TestHTTPNewClientStreamAndCancellation`, `TestHTTPRedirectNeverForwardsCredentials` |
| TLS certificates and hostnames must be strictly verified; pinned dial IPs preserve original Host and SNI | `TestTLSVerificationAndPinnedDialAddress`, `TestHTTPSConnectVerifiesOuterCertificate` |
| Local unprivileged execution, unavailable local bind IPs, and missing credential configs report monitor errors rather than target outages | `TestLocalBindFailureIsMonitorError`, `TestHTTPMissingSecretAndNoAmbientProxy`, `TestICMPCorrelationPermissionAndBudget` |
| Immune to ambient `HTTP_PROXY` environment pollution; proxy failures never fall back to direct connections; SOCKS domain resolution occurs on the proxy | `TestTokenEnvAndNoEnvironmentProxy`, `TestHTTPMissingSecretAndNoAmbientProxy`, `TestProxyFailureDoesNotFallbackDirect`, `TestSOCKSRemoteDNSAndAuthenticationRejection` |
| Malformed CONNECT/SOCKS frames, authentication downgrades, read/write timeouts, and string length budgets are bounded | `TestMalformedProxyResponsesAndBudgets`, `TestProxyCancellationIsBounded`, `TestSOCKSAddressEncoding`, `FuzzSOCKSAddress` |
| Initial SSH jump hop enforces strict host-key validation, rejects interactive shells, disables ambient agent sharing, and closes stdio cleanly | `TestSSHJumpProcessBridgeAndStrictArguments`, `TestSSHBinaryLookupFailure` |
| Reverse port forwarding checks must evaluate the service behind the tunnel; local services and forwarded ports represent distinct check scopes | `TestDefaultTimingAndScope`, `TestServiceDependencyImpactAndRemarks`, `TestOnceRealLocalHTTP` |
| Bootstrap DNS circular dependencies with proxies are rejected; UDP DNS validates txid/questions, truncates to TCP, and bounds CNAME recursion | `TestConfigRejectsBrokenTopology`, `TestDNSUDPAndTCPFallback`, `TestDNSParserCNAMEAndCompressionBudgets` |
| DNS AAAA queries, disallowed address sets, NXDOMAIN responses, and malformed packets are handled deterministically | `TestDNSAAAAAllowlistMismatchAndNegativeResponses`, `FuzzDNSPacket` |
| UDP echo requires matching the current probe nonce; ICMP replies match identifier/sequence/checksum; permission errors are isolated | `TestUDPEchoRequiresResponseNonce`, `TestICMPCorrelationPermissionAndBudget`, `TestICMPRealLoopback` (unprivileged skips gracefully), `FuzzICMPChecksum` |
| Tailscale idle/online peer status does not substitute for application health; older WireGuard handshakes are not outages; keys are always stripped | `TestTailscaleIdleOfflineAndScope`, `TestWireGuardOldHandshakeIsNotFailureAndKeysAreDiscarded` |
| Missing adapter executables, oversized subprocess output, and probe timeouts affect only the specific check | `TestAdapterExecutableAndOutputBudget` |
| Scheduling is partitioned by observer, skipping paused/maintenance checks; enforces bounded concurrency, target non-overlap, and cancellation | `TestOnceScopeIdentityAndExclusions`, `TestSchedulerConcurrencyTargetExclusionAndCancellation`, `TestCancellationAndInvalidScheduler` |
| Storage write failures abort cleanly without emitting false acknowledgments; retries never drop uncommitted records | `TestSinkFailureStopsWithoutLosingError`, `TestStorageFailureIsNotAcknowledged` |
| Dual collectors receive independent dispatches; one outage allows the other to continue; backlogged replay dedupes without rewriting sample times | `TestTwoCollectorsOutageRestartBackfillAndDeduplication`, `TestDurableDetachedDedupeAndLateReplay`, `scripts/smoke.py` |
| Lost ACKs, forged ACKs, conflicting revisions, and permanent 403s: retain or quarantine data safely without dropping valid records | `TestPostAcknowledgementMustMatchExactBatch`, `TestInvalidACKRetainsDataAndBackoff`, `TestPermanentMismatchQuarantineDoesNotLoseHealthyCopy` |
| Outbox atomic persistence, partial acknowledgments, detached memory return structures, capacity limits, and corruption handling | `TestDurablePartialAcknowledgementAndOwnership`, `TestQueueBudgetsValidationAndCorruption`, `TestConcurrentQueueAppend`, `TestQuarantinePreservesEvidenceAndOtherRecipients` |
| Batch validation conflicts reject the entire batch atomically; deduplication receipts are retained; late samples do not overwrite fresher evidence | `TestBatchConflictIsAtomic`, `TestReceiptCapacityAndHeartbeatMonotonicity`, `TestInvalidSecondObservationLeavesBatchUncommitted` |
| Post-checkpoint journal replay and crash windows; recompute only incomplete tail records; complete corrupted lines halt replay immediately | `TestRecoveryOfOnlyIncompleteTail`, `TestCheckpointCrashWindowReplay`, `FuzzEnvelope` |
| History capacity, recent state tails, and deduplication receipts are stored with bounded limits; single-writer lock; I/O failures fail closed | `TestHistoryBudgetKeepsReceiptsAndState`, `TestAutomaticCheckpointAndLock`, `TestStorageErrorsFailClosed`, `TestConcurrentStoreReadWrite` |
| Project identity is immutable; secondary collectors from differing projects are rejected; auth errors cannot be bypassed via failover | `TestReadFailoverAndPinning`, `TestReadFailClosedSecurityBoundaries`, `TestStaleAndFutureCollectorFailover` |
| Remote token roles and observer scoping; payload size, rate limits, ingestion windows, timestamps, revisions, and field constraints enforced | `TestRolesAndNoManagementSurface`, `TestBatchValidationContract`, `TestInputBudgetsAndRateLimit`, `FuzzValidateBatch` |
| HTTPS CA certificates and server hostnames are verified; HTTP redirects never forward Authorization headers; no ambient proxy inheritance | `TestCollectorTLSValidation`, `TestNoRedirectCredentialLeak`, `TestTokenEnvAndNoEnvironmentProxy` |
| Only token SHA-256 digests stored server-side; client credentials written atomically with 0600 permissions; failed logins do not overwrite existing credentials | `TestAuthFilesAndValidation`, `TestPrivateAtomicCredentialFiles`, `TestConcurrentAtomicWritesAreWhole`, `TestCredentialPersistenceAndValidation`, `TestLoginRemoteJSONAndSavedDefault` |
| Oversized, symlink, or non-regular credential files are rejected; directory locks and atomic write errors are surfaced cleanly | `TestPrivateAtomicCredentialFiles`, `TestReadBoundedAndCredentialBudgets`, `TestDirectoryLock` |
| TUI views, narrow terminals, multibyte text, path expansion, observer source switching, and empty-state indicators | `TestEveryTabFrameBoundsAndDetails`, `TestKeyboardNavigationSearchAndEmptyViews`, `TestTerminalEscapesAndWideText` |
| Cached data or disconnected states do not sustain green status indicators; never silently switch observer vantage points | `TestCachedViewDoesNotShowGreen`, `TestRunWithFetchSourceSwitchFailureRecoveryAndQuit` |
| Terminal control code injection, bidirectional text overrides, fragmented UTF-8/ANSI sequences, and oversized bracketed pastes cannot execute commands | `TestTextBlocksTerminalControlAndBidi`, `TestDecoderFragmentedKeysAndPaste`, `TestOversizedFragmentedPasteNeverExecutesCommands`, `FuzzDecoderAndRendering` |
| Real Linux PTY raw mode, resize signals, suppressed password echo, mandatory terminal verification on stdin/stdout, and cursor restoration on exit | `TestActualLinuxTerminalReadSizeAndRestore`, `TestReadSecretActualNoEchoPasteBackspaceCancel`, `TestNonTerminalFailsWithoutControlOutput`, `scripts/smoke.py` |
| Default `topo` invocation loads saved credentials to connect to remote collector; multiple independent daemons run detached from TUI lifecycle | `TestLoginRemoteJSONAndSavedDefault`, `TestServeActualHTTPAndGracefulShutdown`, `TestWorkerActualRemoteIngestion`, `scripts/smoke.py` |
| Offline reproducible Go build; sanitized example topologies validate cleanly; `init` generates unique random credentials without overwriting | `TestCLIDispatchAndHelp`, `TestInitFreshCredentialsNoOverwriteAndSanitizedExport`, `TestShippedTopologyExamplesValidateAndHomelabIsPaused`, `scripts/verify.py` |

## Scan-bundle regression coverage

| Finding | Regression tests |
|---|---|
| Exit-IP structural JSON budgets | `TestExitJSONComplexity` |
| Mihomo proxy/provider/history/member budgets | `TestMihomoResponseBudgets` |
| Non-acknowledging collector isolation | `TestUnacknowledgedDestinationCannotStopHealthyDelivery`, `TestInvalidACKCollectorIsolationEndToEnd` |
| Observer history reservations across restart | `TestHistoryReservationSurvivesCheckpoint`, `TestObserverQuotasSurviveRestartAndReserveHeartbeats` |
| Pre-authentication connection and request admission | `TestConnectionLimitCoversPartialHeadersAndReleases`, `TestPreauthenticationConcurrencyAndRateBounds` |
| Fair snapshot cache computation | `TestViewerCannotReserveAllSnapshotBuilds`, `TestSnapshotViewCacheHasGlobalBudget` |
| Heartbeat project/revision provenance | `TestRecoveredHeartbeatContract` |
| Durable expired observation-ID binding | `TestExpiredEvictedIDRemainsBoundAcrossRestart` |
| Terminal-safe machine-readable remote JSON | `TestJSONOutputEscapesAllBidiControlsLosslessly`, `TestRemoteJSONEscapesBidiControls` |
| Verification commit/tree binding | `test_old_revision_evidence_is_rejected`, `test_mutation_during_verification_is_rejected` |
| Release tag-to-build binding | `test_publish_resolves_lightweight_and_nested_annotated_tags` (matching, moved, missing, nested and cyclic tag fixtures) |

## Boundaries Beyond Automated Suite Claims

Verifying whether virtual network interfaces or host tunnel adapters successfully bypass underlying physical drivers requires empirical testing on target hardware. Mock scripts, cross-compilation passes, and loopback unit tests do not substitute for live hardware verification.

These tests do not diagnose upstream transit filtering entities, outage durations, or broad network policy enforcement; no claims are made regarding external user session persistence, unbounded streaming protocols, or general browser compatibility. Topophany does not bundle proxy core runtimes, scan external proxy provider lists, or automate routing remediation. Proxy transport authentication is verified through structured tests on dedicated listeners rather than reimplementing complex external tunneling protocols.

Interoperability across live OpenSSH, Tailscale, and WireGuard tunnels and native execution on Windows and macOS must be verified according to [ACCEPTANCE.md](ACCEPTANCE.md). Automated leader-election or active failover mutation is deliberately not implemented; clustering guarantees are limited to independent observation ingestion and read-only client failover.
