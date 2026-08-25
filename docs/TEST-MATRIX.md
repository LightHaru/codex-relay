# Shared-Memory Router v2 and explainability test matrix

This matrix maps every required Router v2 acceptance row to deterministic local
evidence. All tests use temporary Router/account homes, fake JSONL app-server
children, or renderer/patcher fixtures. They do not sign in, call live ChatGPT
quota endpoints, consume reset credits, patch an installed app, or stop either
the official Codex app or an installed Relay.

Status legend:

- **PASS** — exercised successfully in the local fixture suite on 2026-08-24.
- **LIVE PENDING** — intentionally requires permission-gated installed-app E2E;
  it is not represented as automated proof.
- **LIVE PASS** — exercised with explicitly authorized real subscriptions on
  the recorded Windows build; no reset credit was used.

## Explainable Routing 0.4.1 delta

These rows supplement the Router-v2 rows below. They are fixture evidence, not
a claim that a real subscription consumed quota during this development turn.

| Requirement | Status | Automated evidence |
| --- | --- | --- |
| Preview is deterministic and cannot mutate cursor, deficits, dispatches or reservations | PASS | `TestExplainabilityPreviewDoesNotMutateScheduler` |
| Owner, active worker, last completed worker, previous worker and last quota worker remain distinct | PASS | `TestExplainabilityContractSeparatesOwnerWorkerAndQuotaAttribution`; UI inspector test |
| Sticky/Rotate/Balanced and unknown-profile effective Sticky remain policy-aware | PASS | `TestUnknownAppServerProfileKeepsBalancedTaskSticky`; policy integration tests; pure route preview tests |
| Depleted, cooldown, open circuit, disconnected, disabled, unknown and stale reasons are fixed and truthful | PASS | `TestExplainabilityWorkerReasonsAreStandardizedAndQuotaTruthful` |
| Unknown/stale quota is never returned as confirmed | PASS | `TestExplainabilityWorkerReasonsAreStandardizedAndQuotaTruthful` |
| Handoff source, target, reason and generations survive in the projection | PASS | `TestExplainabilityHandoffSummaryIncludesReasonAndGenerations`; handoff integration suite |
| Timeline event IDs are stable and one logical event is not duplicated | PASS | `TestExplainabilityTimelineUsesStableUniqueEventIDs`; `TestFailedReservationRollbackCreatesOneTimelineEvent` |
| Failed reservation is rolled back and represented once | PASS | `TestFailedReservationRollbackCreatesOneTimelineEvent`; existing scheduler rollback tests |
| Quota consumption is confirmed only from a newer, measurably lower snapshot | PASS | `TestQuotaAttributionRequiresBeforeAndNewerAfterSnapshot` |
| Projection rejects raw emails, paths and arbitrary/corrupt error text | PASS | `TestExplainabilityResponsesDoNotExposeRawIdentityErrorsOrPaths`; control redaction tests |
| Task inspector shows actual/last/preview, reasons, timeline, pool and native keyboard disclosure | PASS | UI tests `task-view route badge...` and `task route inspector distinguishes...` |
| A replayed terminal SSE event shows at most one toast in the renderer session | PASS | UI test `replayed handoff event shows only one session-scoped toast` |
| Partial bridge/API failures do not replace or crash the native task/Settings UI | PASS | existing bridge fail-closed and partial Usage tests |
| Long labels truncate in-flow; no fixed task/profile overlay exists | PASS | inspector CSS/source assertions and normal-flow renderer tests |
| Usage panel remains only inside the native Usage & billing child page | PASS | Usage surface DOM tests and exact Store renderer fixtures |
| Current and all reviewed older Store profiles still patch exact anchors | PASS | 26 Windows patcher tests (one environment-optional test skipped); seven exact profiles |
| Balanced fairness, Rotate, Sticky, Goal, old/archived chat, concurrency and late-generation safety do not regress | PASS | Router-v2 rows 1–31 and 41–48 below |
| Control routes continue to require the per-install token | PASS | control API token-protection tests |

Retention remains bounded in two places: the persisted decision ledger retains
at most 1,000 decisions and each task inspector response returns at most 100
recent events. Renderer toasts remember at most 100 terminal event IDs per
session. The inspector is a projection; corrupt projection input is sanitized
at read time and cannot rewrite scheduler or ownership state.

The local Windows validation host has no GCC and reports `CGO_ENABLED=0`, so it
must not be cited as race-detector evidence. `.github/workflows/ci.yml` runs
`go test -race ./internal/state ./internal/mux ./internal/control` on macOS for
every push to `main` and every pull request, where the CGO toolchain is present.

## Required rows 1–48

| # | Requirement | Status | Automated evidence |
| ---: | --- | --- | --- |
| 1 | A=0, B=22, C=100; depleted skipped, weighted fairness, no starvation; strict rotation | PASS | `TestSchedulerZeroTwentyTwoHundredQuotaDistribution`; `TestRotatePolicyMovesOnlyAtCompletedTurnBoundaries` |
| 2 | Unknown quota loses to known quota and is probation-only fallback | PASS | `TestQuotaFallbackSelectsSecondaryWhenPrimaryHasNoCapacity`; `TestFairShareDoesNotPreferUnknownQuotaOverKnownCapacity` |
| 3 | Scheduler cursor and deficits survive restart | PASS | `TestBalancedPolicyUsesPersistentQuotaWeightedDeficits`; `TestRoutingStateSurvivesRestartAndRejectsUnknownPolicy` |
| 4 | Concurrent reservations are atomic | PASS | `TestConcurrentSchedulerReservationsAreAtomic` |
| 5 | Failed send/handoff reverses the exact reservation and dispatch charge | PASS | `TestFailedSendRollsBackSchedulerDispatchCredit`; `TestReselectingSameLogicalTurnReplacesSchedulerCharge`; `TestFailedTargetResumeRollsBackHandoffAndSourceOwnership` |
| 6 | Quota circuit stays out until a newer confirmed refresh | PASS | `TestCircuitBreakerRequiresQuotaRefreshAfterCooldown`; `TestBalancedSchedulerPrefersFreshKnownQuotaAndSkipsOpenCircuit` |
| 7 | Rotate sends three completed turns B → C → B with one thread ID | PASS | `TestRotatePolicyMovesOnlyAtCompletedTurnBoundaries` |
| 8 | Tool/approval and response remain on the creating child | PASS | `TestApprovalResponseReturnsToTheChildThatCreatedIt`; `TestThreadlessInterruptUsesTheSingleActiveWorkerBinding` |
| 9 | Duplicate `turn/start` is rejected without replacing the attempt | PASS | `TestTurnCoordinatorSerializesOneActiveTurnPerThread` |
| 10 | Immediate quota rejection fails over once and hides source quota error | PASS | `TestRouteTurnFailsOverToSecondaryAndPersistsNewOwner`; `TestRouteNewThreadRetriesWhenSelectedAccountReportsUsageLimit` |
| 11 | Async quota rejection before side effect fails over once without duplicate output | PASS | `TestRouteTurnFailsOverAfterAsyncUsageLimit` |
| 12 | Async quota rejection after side effect does not blind-replay | PASS | `TestQuotaFailureAfterSideEffectsRequiresRecoveryInsteadOfRetry`; `TestAsyncQuotaNotificationAfterSideEffectDoesNotCreateHandoff` |
| 13 | Error plus failed completion creates one migration | PASS | `TestRouteTurnFailsOverAfterAsyncUsageLimit` |
| 14 | Late source-generation events are suppressed | PASS | `TestStaleWorkerNotificationsAreSuppressedByCurrentGeneration` |
| 15 | PREPARED crash recovery | PASS | `TestStartupRollsBackInterruptedHandoffToSourceGeneration/PREPARED` |
| 16 | COPIED crash recovery | PASS | `TestStartupRollsBackInterruptedHandoffToSourceGeneration/COPIED` |
| 17 | RESUMED crash recovery | PASS | `TestStartupRollsBackInterruptedHandoffToSourceGeneration/RESUMED` |
| 18 | Hash/size mismatch fails closed | PASS | `TestCheckpointHashMismatchFailsClosed` |
| 19 | Prefix mismatch triggers full rebuild | PASS | `TestIncrementalHistoryMaterializationFallsBackOnPrefixMismatch` |
| 20 | Resume reports no rollout found safely | PASS | `TestLoadThreadResumeInfoReportsNoRolloutFound` |
| 21 | Unavailable target creates no handoff journal | PASS | `TestTargetChildUnavailableDoesNotPrepareHandoff` |
| 22 | Absolute rollout path | PASS | `TestCopyThreadHistoryCopiesOnlyTheSessionsRelativeRollout` |
| 23 | `CODEX_HOME`-relative rollout path | PASS | `TestCopyThreadHistoryResolvesCodexRelativeSessionsPath` |
| 24 | `sessions` path | PASS | `TestCopyThreadHistoryCopiesOnlyTheSessionsRelativeRollout` |
| 25 | `archived_sessions` path | PASS | `TestCopyThreadHistoryCopiesArchivedRolloutToArchivedSessions` |
| 26 | Windows extended-length path | PASS | `TestCopyThreadHistoryResolvesWindowsExtendedLengthPath` |
| 27 | Source and target symlink/junction escapes | PASS | `TestIncrementalHistoryRejectsSourceSymlinkEscape`; `TestIncrementalHistoryRejectsTargetSymlinkEscape` (Windows junction fallback runs without Developer Mode) |
| 28 | Oversized rollout is rejected before copying | PASS | `TestIncrementalHistoryRejectsOversizedSparseRollout` |
| 29 | Verified prefix uses incremental append | PASS | `TestIncrementalHistoryMaterializationAppendsVerifiedSuffix`; `TestCompletedTurnRefreshesCanonicalRelayCheckpoint` |
| 30 | Multiple replicas do not override one authoritative generation | PASS | `TestCanonicalCheckpointSelectsVerifiedReplicaOnly` |
| 31 | Replicated thread appears once in the merged list | PASS | `TestMergeThreadCandidatesReturnsOneLogicalTaskForManyReplicas` |
| 32 | Each child has its own `CODEX_HOME` | PASS | `TestChildEnvironmentKeepsCodexAndSQLiteHomesIsolated` |
| 33 | Each child has its own `CODEX_SQLITE_HOME` | PASS | `TestChildEnvironmentKeepsCodexAndSQLiteHomesIsolated` |
| 34 | Account-scoped calls read only that account credential | PASS | `TestFetchUsageStatusUsesIsolatedAccountCredential`; `TestFetchRateLimitResetCreditsUsesSelectedAccountCredentials` |
| 35 | Relay does not use `%USERPROFILE%\.codex` as its Windows primary home | PASS | `TestResolvePrimaryCodexHomeUsesDedicatedRelayHomeOnWindows`; `TestOpenIsolatedMovesEveryLegacyNativeAccountWithoutTouchingNativeHome` |
| 36 | Official Codex home/files remain unchanged | PASS | `TestOpenIsolatedMigratesNativePrimaryWithoutTouchingNativeHome`; `TestOpenIsolatedMovesEveryLegacyNativeAccountWithoutTouchingNativeHome` |
| 37 | Login callback/poll/cancel remains scoped to the selected account | PASS | `TestCancelLoginPreservesAlreadyConnectedSecondary`; UI tests `browser login completion...`, `stale browser-login poll...`, and `official browser login...` |
| 38 | Tokens/prompts/paths are absent from routing diagnostics | PASS | `TestCanonicalTurnLedgerStoresRequestHashNotPromptOrPaths`; `TestThreadRouteAPIRedactsCanonicalRolloutPath`; token-protection control tests; release secret scan |
| 39 | Current task route can differ from Relay Controller in UI | PASS | UI test `profile menu distinguishes Relay Controller, Current Task Route, and routing policy` |
| 40 | Handoff SSE refreshes the route badge | PASS | UI test `handoff SSE refreshes the open route badge` |
| 41 | Routing policy persists across restart | PASS | `TestRoutingStateSurvivesRestartAndRejectsUnknownPolicy`; control policy API test |
| 42 | Usage & billing remains the native child page | PASS | UI test `the Windows bridge keeps the native Usage & billing page and adds an in-flow Relay surface`; exact renderer patch fixtures |
| 43 | Sidebar is not covered by a fixed overlay | PASS | Same UI source assertion plus `task-view route badge...in normal flow` |
| 44 | Other Settings pages remain reachable/unreplaced | PASS | Version-pinned exact-anchor patch fixtures; Usage surface test asserts no Settings-shell replacement |
| 45 | English and Vietnamese routing UI are clear | PASS | UI tests `profile menu distinguishes...` and `routing panel provides clear Vietnamese labels` |
| 46 | Older reviewed ASAR profiles still patch | PASS | `test_patches_the_current_renderer_profile`; `test_patches_the_store_26_818_3698_renderer_profile`; legacy profile assertions |
| 47 | Reviewed 26.818.8289 plus previous 26.818.5345, 26.818.5229 and 26.818.4152 profiles patch | PASS | `test_patches_the_store_26_818_8289_profile`; `test_patches_the_store_26_818_5345_profile`; `test_patches_the_store_26_818_5229_profile`; `test_patches_the_store_26_818_4152_usage_billing_bridge`; selected-account reset-hook test |
| 48 | Unknown app-server profile fails closed to effective Sticky | PASS | `TestResolveCompatibilityProfileFailsClosedWithoutReviewedManifest`; `TestUnknownAppServerProfileKeepsBalancedTaskSticky`; `TestUnknownAppServerProfileDoesNotMigrateDepletedExistingTask`; patcher unknown-profile test |

## Additional evidence

- Canonical goal fields remain JSON `null` when upstream does not provide them:
  `TestCompletedTurnRefreshesCanonicalRelayCheckpoint`.
- State v1 is backed up and migrated atomically to v2; corrupt primary state is
  recovered from the last valid backup: `TestV1StateMigratesAtomicallyToV2WithBackupAndDefaultPolicy`
  and `TestStoreRecoversCorruptPrimaryStateFromLastValidBackup`.
- `thread/read` must return the same thread ID and the target rollout must match
  the copied SHA-256/size before commit; the fake app-server integration covers
  the complete handoff path.
- `account-skipped`, circuit, attempt, handoff, completion, and recovery events
  carry timestamps and sanitized Router reasons. `TestSchedulerPublishesSanitizedAccountSkippedEvents`
  covers the previously missing skip event.
- A newly created task cannot migrate before its first rollout exists:
  `TestFirstTurnStaysOnWorkerThatCreatedNewThread`. After the first completed
  turn, Balanced and Rotate may hand the verified canonical rollout to another
  eligible worker.
- App-server tooling and the retry visibility boundary are covered by the
  tooling normalization tests and `TestUserMessageItemDoesNotCountAsVisibleAssistantOutput`.
- `TestCompletedBoundaryHandoffTransfersActiveGoalToTarget` proves that a
  source `usageLimited` Goal is restored as `active` on the target before the
  turn is sent.

## Local release gates

Run from the repository root:

```powershell
npm ci --ignore-scripts
npm run check
npm run release:check
git diff --check
```

The final local `npm run check` result on 2026-08-25 was PASS: Go test/vet, 29
Node/UI tests, Python byte-compilation (including the permission-gated live
probe script), 26 Windows patcher tests with one optional renderer test skipped,
and shell syntax completed successfully. The reviewed profile was also applied
to the unpacked real Store `26.818.8289.0` renderer; all seven recorded exact
profiles remain covered.

The Go race detector is an extra gate, not part of `npm run check`. It was not
available on this Windows host because race builds require CGO and no GCC
toolchain is installed. Atomic scheduler concurrency is still exercised by its
24-goroutine reservation test, but a CI runner with CGO should also run:

```powershell
go test -race ./internal/state ./internal/mux
```

## Permission-gated live checks

The operator explicitly authorized minimal real-quota E2E on 2026-08-23. The
following protocol-level checks are historical **LIVE PASS** evidence from
Store `26.818.5229.0`:

- a direct turn and direct resume on the depleted source subscription both
  returned the real upstream `quota_exhausted` result;
- Relay resumed that same existing task, copied and verified its rollout,
  committed generation `1 → 2`, moved it from the depleted worker to an
  available worker, and completed the turn without exposing the source quota
  error;
- a brand-new Relay task completed one minimal turn through the pool; the live
  run exposed and then verified the first-turn rollout-race fix;
- a real active Goal on the depleted worker entered `usageLimited`; Relay then
  committed its history handoff, restored the same objective as `active` on an
  available worker, completed the turn, and left the route idle at generation
  2 with no recovery requirement;
- the official Codex process remained running, Relay credentials stayed in
  isolated homes, and no reset credit was used.

After installing Relay `0.4.5` from Store `26.818.8289.0` on 2026-08-25, a
read-only health check reported the reviewed compatibility profile, five
connected subscriptions, `296% / 500%` confirmed pool quota, three available
workers, two depleted workers, zero active turns, and one pre-existing
recovery-required task. No live turn or reset credit was consumed during this
upgrade verification.

Visual confirmation of the native task badge and Settings placement remains a
manual operator check because automated control of the Codex/ChatGPT desktop UI
is intentionally excluded. A real post-side-effect quota failure is also not
induced because it could duplicate or partially perform user work; the
recovery-required branch remains deterministic fixture evidence.

See [`SMOKE-TEST.md`](SMOKE-TEST.md). Do not stop the official Codex app, click
**Use reset**, or send quota-consuming prompts without separate permission.
