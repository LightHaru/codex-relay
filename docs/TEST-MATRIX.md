# Unified Pool Gateway test matrix

This matrix distinguishes deterministic proof, local app proof and real-account
proof. `PASS` means the named test ran and its final exit code was zero.
`LIVE PENDING` is intentional until an authorized real-quota run records the
transition. No test in this repository should print credentials, prompts,
history contents or full account identity.

## Deterministic state and transport

| Invariant | Evidence | Status |
| --- | --- | --- |
| State v1/v2 migrate to v3 with backups | `internal/state/store_test.go` migration tests | PASS |
| v2 rollback projection is hashed and recovery-safe | `TestV3ContinuouslyWritesRecoverySafeV2RollbackProjection` | PASS |
| Pool revision uses CAS | `TestPoolCASHeartbeatAndCrashRecovery` | PASS |
| Concurrent quota rejection commits one transition | `TestConcurrentQuotaRejectionsCommitOnePoolTransition` | PASS |
| Unified Gateway fair-shares confirmed sources | `TestBalancedPoolLeaseUsesOnePoolCursorAcrossConfirmedSources`; balanced transport test | PASS |
| Legacy sticky lease compatibility remains available | `TestPoolStickyUntilExplicitQuotaRejection`; 20-turn transport test | PASS |
| Explicit A→B failover keeps exact request bytes | `TestTransportFailsOverSameRequestBeforeOutput` | PASS |
| Early SSE quota retries; late SSE never replays | `TestTransportRetriesEarlyStreamQuotaButNotLateQuota` | PASS |
| Late quota marks future source depleted and task recovery | pool/transport late-stream assertions | PASS |
| All-depleted does not fabricate completion or leak a lease | `TestPoolDepletedRequestDoesNotLeaveLeaseOrFakeCompletedTurn` | PASS |
| Heartbeat and expired lease recovery | `TestPoolCASHeartbeatAndCrashRecovery` | PASS |
| Restart releases pre-commit lease for same request ID | `TestPoolRestartReleasesUncommittedLeaseForSameRequestReplay`, `TestGatewayReplayAfterRestartDoesNotReturnLogicalTurnConflict` | PASS |
| Restart preserves post-commit no-replay recovery | `TestPoolRestartKeepsCommittedLeaseRecoveryRequired` | PASS |
| Concurrent duplicate request single-flight | `TestConcurrentDuplicateLogicalTurnJoinsOneUpstreamFlight` | PASS |
| Terminal-aware SSE and truncated EOF failover | `TestTransportRetriesCleanEOFWithoutTerminalOnNextSource`, `TestTransportDoesNotAcceptPartialCompletedEventAsTerminal` | PASS |
| Nested output-item completion is not a Responses terminal | `TestUnifiedGatewayPostCommitIdleEmitsRecoverableTerminal`; terminal classifier regression | PASS |
| Post-output truncated SSE never replays | `TestTransportDoesNotReplayTruncatedStreamAfterVisibleOutput` | PASS |
| Post-output idle/canceled stream emits a recovery terminal and clean mux message | `TestTransportConvertsIdlePostCommitStreamToRecoveryTerminal`, `TestSanitizeRelayRecoveryNotificationRemovesNativeStreamPrefix` | PASS |
| Post-output client cancellation never fabricates `response.completed` | `TestForwardSSECanceledClientAfterOutputItemEmitsRecovery` | PASS |
| Temporary upstream 502 rotates without quota depletion | `TestTransportRetriesTemporaryHTTPBadGatewayAcrossSources` | PASS |
| Transport cooldown remains separate from quota/auth | `TestRepeatedTransientFailuresOpenCooldownWithoutChangingQuota` | PASS |
| All-source transient exhaustion clears lease and reports reference | `TestAllSourcesTransientFailureReturnsOneDiagnosticAndClearsLease` | PASS |
| Tokens and arbitrary upstream errors are absent from responses | gateway and explainability sanitization tests | PASS |
| Local bearer is required | `TestTransportRequiresItsLocalBearerToken` | PASS |
| Unknown quota enters probation, not confirmed capacity | `TestPrimeCredentialSourcesUsesProbationWithoutGuessingQuota` | PASS |
| Isolated config sync is idempotent and keeps one Relay provider table | `TestSyncIsolatedConfigDoesNotDuplicateRelayProviderWhenSourceIsTarget` | PASS |
| Secondary management config does not inherit an undefined Relay provider | `TestSyncIsolatedConfigOmitsRelayDefaultForSecondaryWithoutProvider` | PASS |

## Local app-server E2E

| Scenario | Evidence | Status |
| --- | --- | --- |
| One installed `codex.real.exe` task authority | `TestUnifiedGatewayUsesOneTaskAuthorityAndFailsOverInsideRequest` | PASS |
| Same thread/session/task through A→B→C→D | same test; exact source order and request-body hashes | PASS |
| No public account owner or move-chat event | contract-v2 route/status tests | PASS |
| Tool/approval/Goal traffic remains on authority | mux unified routing tests | PASS |
| New and existing thread use the same authority | mux routing and resume tests | PASS |
| Restart/crash keeps TaskRecord and canonical generation | state migration/recovery tests | PASS |
| Post-output idle recovery with the installed real app-server | `TestUnifiedGatewayPostCommitIdleEmitsRecoverableTerminal` (real `codex.real.exe`) | PASS |
| Native compact keeps the same live task and continuation checkpoint | `scripts/live_compact_continuity_e2e.py` with installed Relay wrapper | PASS |
| Native command activity retains the actual command text | `scripts/live_command_visibility_e2e.py` with installed Relay wrapper | PASS |
| Production Gateway completes one authorized real-account turn and releases its lease | `TestLiveAccountPoolSmoke` (opt-in) | PASS |
| Windows path, locked rollout, hash/size and symlink safety | canonical history tests | PASS |
| Native Usage & billing stays in content column | `ui/test-windows-router-menu.cjs`; renderer fixtures | PASS |
| Other Settings pages/sidebar remain reachable | UI bridge/fixture tests | PASS |

Run the installed app-server case with:

```powershell
$env:CODEX_RELAY_REAL_E2E = 'C:\path\to\codex.real.exe'
go test ./internal/mux -run TestUnifiedGatewayUsesOneTaskAuthorityAndFailsOverInsideRequest -count=1 -v
```

The test uses a fake upstream and fake source credentials in a temporary home;
it is not a real-quota test.

## Required live-account gates

| Scenario | Required evidence | Status |
| --- | --- | --- |
| One short new chat with live source | `live_app_server_e2e.py`; sanitized terminal and final exit code | PASS |
| A genuinely rejected for quota, then B continues same turn | structured rejection, transition revision, same thread/session/body, no public switch | LIVE PENDING |
| B→C and C→D, if each is genuinely rejected | each rejection and next source observed independently | LIVE PENDING |
| Long chat across quota boundary | one task, no duplicate/lost output, canonical hash/size, no new Goal | LIVE PENDING |
| Existing chat with canonical checkpoint | resume succeeds without external-source path error | LIVE PENDING |
| Archived chat | one logical task, no history copy outside managed roots | LIVE PENDING |
| Goal continuation | live wrapper Goal set/get with matching objective and completed turn | PASS |
| Tool/approval boundary | no replay after side effect; recovery marker if partial | LIVE PENDING |
| All sources depleted | one pool-level error and no leaked identity | LIVE PENDING |
| Live quota visibility for every enabled source home with credentials | `probe_live_rate_limits.py`; 7/7 structured responses | PASS |
| Control readiness, aggregate snapshot, auth boundary, and SSE reconnect | live loopback control probe; two connect/close/reconnect cycles | PASS |
| Official Codex preservation | official process remains open; installer scope is limited to Relay copy | PASS |

Live tests require explicit authorization, minimal prompts, no reset-credit
consumption and no destructive actions. Store only sanitized prefixes, counts,
hashes, states, reasons and exit codes. Never store OAuth material, account
emails/IDs, raw prompt/output or full paths.

## Compatibility/release gates

```powershell
npm ci --ignore-scripts
npm run check
npm run release:check
git diff --check
```

The release gate must also verify the selected renderer profile, the probe output,
the state-v3 migration/rollback, UI placement, updater path/hash validation and
the complete worktree diff. `go test -race` is recommended on a CGO-capable CI
runner; this Windows environment reports `CGO_ENABLED=0`, so a local non-race
stress test is not race-detector evidence.

## Evidence report template

Each E2E report under `docs/evidence/` must contain:

- version/commit, compatibility profile and pool schema version;
- sanitized session/thread prefix and turn count;
- observed source transition order and rejection category;
- pool/lease revisions and states, quota snapshots before/after;
- canonical generation/hash/size and Goal continuity;
- duplicate/lost-output, side-effect and replay assertions;
- process isolation, official Codex preservation and final command/exit code;
- an explicit `PASS`, `LIVE PENDING` or `NOT PROVEN` for every claim.

Do not mark the Goal complete while any required live row is pending or while
partial-stream continuation lacks protocol evidence.
