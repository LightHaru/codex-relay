# Changelog

All notable changes follow [Keep a Changelog](https://keepachangelog.com/) and
this project uses [Semantic Versioning](https://semver.org/).

## [0.5.9] - 2026-08-28

### Changed

- The unified production Gateway now publishes each model response as one
  transaction. Assistant deltas and tool calls stay private until the upstream
  sends `response.completed`; only SSE keepalive comments are visible while it
  works.
- A quota rejection, disconnect, truncated frame, or transient source failure
  before that terminal boundary can rotate to another pool source without
  leaking partial output or executing a buffered tool call twice.
- Native remote compaction now passes through `/v1/responses/compact` without
  parsing or rewriting its opaque payload. Compaction can fail over before
  publication and keeps the same thread/task headers without creating restart
  or recovery markers.
- Native shell-call payloads, including the complete command string used by
  Codex's running-command UI, are preserved exactly once through the
  transactional response boundary.
- Upgrading removes legacy `RECOVERY_REQUIRED` leases and task markers at
  startup. Transactional production requests no longer create that state.

### Validation

- Added deterministic E2E coverage for a quota rejection after buffered text,
  a disconnect after a buffered function call, native compaction passthrough
  and failover, full command payload preservation, single-publication
  semantics, and migration of legacy recovery markers.

Release: https://github.com/LightHaru/codex-relay/releases/tag/v0.5.9

## [0.5.8] - 2026-08-28

### Fixed

- Production Gateway streams no longer have a post-output idle deadline.
  Relay now behaves as a transparent API intermediary: it sends downstream
  keepalives and waits for an upstream terminal/close or an actual client
  cancellation.
- If a compatible upstream cleanly closes immediately after a complete
  `response.output_item.done` boundary but omits only `response.completed`,
  Relay now emits the missing completion terminal without replaying the
  request or any tool side effect.
- Unterminated frames and disconnects before a complete output-item boundary
  remain fail-closed, so truncated content is never reported as completed.

### Validation

- Added regressions proving production has no idle cutoff, explicit test-only
  cutoffs still work, and a clean close after an output-item boundary completes
  with no recovery lease.

Release: https://github.com/LightHaru/codex-relay/releases/tag/v0.5.8

## [0.5.7] - 2026-08-28

### Fixed

- Post-output SSE gaps are no longer declared dead after only three seconds.
  Relay now keeps the native connection alive and allows a realistic upstream
  reasoning/tool pause before entering fail-closed recovery.
- Starting a distinct new turn on a recovered thread removes every orphaned
  `RECOVERY_REQUIRED` lease from older builds. The previous logical turn is
  never replayed, while pool status no longer accumulates phantom requests.
- Added a read-only native activity probe that verifies historical
  `commandExecution` details or titled Codex `js` tool activities without
  printing commands, outputs, prompts, or credentials.

### Validation

- Added deterministic Gateway coverage for the configurable idle boundary and
  state coverage for continuing a thread with multiple stale recovery leases.
- Full Go, JavaScript, Python, Windows patcher, and shell validation passed.

Release: https://github.com/LightHaru/codex-relay/releases/tag/v0.5.7

## [0.5.6] - 2026-08-28

### Fixed

- Gateway idempotency now combines the native client request ID with an opaque
  request-body fingerprint. Current Codex builds may reuse one
  `X-Client-Request-Id` across distinct turns; Relay no longer replays the
  previous turn's cached SSE response or skips the next pool dispatch.
- Exact duplicate requests with the same ID and body still join one in-flight
  request and receive one bounded replay, preserving restart/renderer retry
  safety without collapsing later Goal turns.
- The real app-server Goal probe now supports a finite token budget, reports
  bounded sanitized failures, and fails with useful child-process diagnostics
  instead of dumping complete turn histories.
- Added an exact reviewed renderer profile for Microsoft Store
  `26.825.3734.0`; installation remains fail-closed for every unknown ASAR.

### Validation

- Added a regression proving that the same native request ID with different
  request bodies dispatches two distinct logical turns while an exact duplicate
  remains single-flight.
- The real `codex.real.exe` full-stack fixture completed 20 turns on one thread,
  injected three structured quota rejections, performed 23 upstream dispatches,
  preserved the one task authority, and left no active lease.
- Five operator-authorized real credential sources each completed a minimal
  Responses request. A finite real Goal completed, became `budgetLimited`, then
  resumed after an app-server restart with the same objective and thread.

Release: https://github.com/LightHaru/codex-relay/releases/tag/v0.5.6

## [0.5.5] - 2026-08-28

### Fixed

- The unified Gateway now serves authenticated `GET /v1/models` requests and
  forwards the current Codex `client_version` query to the native ChatGPT model
  catalog. New Codex builds no longer report a misleading desktop network error
  while ordinary `/v1/responses` turns continue to work.
- Model discovery uses the Relay authority credential first, may fall back to
  another enabled credential only when discovery fails, and never acquires a
  quota lease or changes quota health.
- A validated in-memory catalog cache collapses parallel startup refreshes and
  preserves the last accepted catalog during a temporary provider-edge error.
  Invalid or oversized upstream payloads return a sanitized local diagnostic.

### Validation

- Added regressions for query/header forwarding, local bearer isolation,
  controller credential fallback, caching, no quota lease, and invalid catalog
  sanitization.
- The installed real app-server probe must complete a live turn with no
  `failed to refresh available models` or local `/v1/models` 404 diagnostic.

Release: https://github.com/LightHaru/codex-relay/releases/tag/v0.5.5

## [0.5.4] - 2026-08-28

### Fixed

- Startup now reclaims only stale pre-commit leases before the loopback
  Gateway accepts work, so a reboot cannot leave the same logical turn blocked
  by a false `409` active-request conflict. Committed leases remain
  `recovery-required` and are never replayed.
- The Responses SSE classifier no longer mistakes a nested
  `item.status="completed"` on `response.output_item.done` for the terminal
  `response.completed` event. A short terminal grace window and invisible SSE
  keepalive cover providers that pause after the final output item.
- If the upstream stream closes or the native app-server cancels after output
  began without a terminal event, Relay emits a standards-shaped recovery
  event, preserves the lease safety state, and the unified mux replaces the
  native generic stream-disconnect prefix with an actionable Relay message.

### Validation

- Added regression coverage for top-level terminal classification, stale
  restart leases, post-output idle/partial streams, and recovery notification
  sanitization.
- Real installed `codex.real.exe` app-server E2E now covers the idle
  post-output recovery path without the old `stream closed before
  response.completed` diagnostic.

Release: https://github.com/LightHaru/codex-relay/releases/tag/v0.5.4

## [0.5.3] - 2026-08-28

### Fixed

- Relay now releases persisted pre-commit leases before the loopback Gateway
  starts accepting work. Replaying the same native request after a machine or
  app restart no longer fails with `409 logical_turn_already_active`.
- Duplicate concurrent requests for the same logical turn join one in-process
  flight and receive the same bounded, short-lived response replay. Only one
  upstream attempt may commit, so startup races cannot duplicate model/tool
  work.
- A clean or unexpected EOF without an explicit Responses terminal event is no
  longer treated as successful. Before output it rotates to another eligible
  source; after output/tool side effects it remains recovery-required and is
  never replayed.
- Temporary connection errors and upstream HTTP 408/425/500/502/503/504 now
  fail over inside the same logical request. Transport health is tracked
  separately from quota/auth state and repeated failures open a bounded source
  cooldown instead of falsely depleting or disconnecting the account.
- Exhausted transient retries now return one sanitized diagnostic containing
  the safe error class, attempt count and local correlation reference.

### Validation

- Added regression coverage for process-restart request replay, expired lease
  reclamation, committed restart recovery, concurrent duplicate single-flight,
  terminal-aware SSE parsing, partial terminal frames, pre/post-commit stream
  truncation, temporary HTTP 502 failover, transport circuit cooldown and
  all-source retry exhaustion.

## [0.5.2] - 2026-08-27

### Fixed

- The unified gateway now accepts chunked Responses SSE streams when the
  provider omits `Content-Type`, while preserving the stream body and the
  single Relay lease. This matches the current ChatGPT endpoint and prevents
  a healthy fallback account from being reported as an unsupported response.
- The live-account E2E fixture now sends the provider-required `store:false`
  flag and includes the bounded diagnostic body when a real smoke turn fails.
- Unified Gateway dispatch now uses a persistent quota-aware fair-share cursor
  across confirmed sources. A healthy account can no longer monopolise the
  aggregate pool simply because it was the first or last `ActiveSourceID`;
  depleted sources are skipped and pre-output retries remain inside the same
  logical request.

### Validation

- Added a regression test for quota failover into an SSE stream without a
  `Content-Type` header.
- Authorized live-account E2E: Aira quota rejection → next source, three
  completed turns, one failover, same pool authority. Deterministic fair-share
  and installed app-server E2E cover source rotation without changing the
  public worker.

Release: https://github.com/LightHaru/codex-relay/releases/tag/v0.5.2

## [0.5.1] - 2026-08-27

### Fixed

- The unified gateway now recognizes the provider's “You're out of Codex
  messages”/“run out of messages” quota response, including `response.failed`
  SSE events, failed JSON envelopes and raw JSON returned with an SSE content
  type. The same logical request can therefore continue through the next
  eligible source in the shared pool instead of surfacing a native `-32600`
  error after only the controller account is tried.
- Quota evidence matching is shared by HTTP and streaming paths, while normal
  successful JSON responses and model context errors remain fail-closed.

### Validation

- Added regression coverage for Codex message-limit SSE and JSON error shapes,
  plus the real installed app-server unified-pool E2E using the exact message
  shown by the native UI.

Release: https://github.com/LightHaru/codex-relay/releases/tag/v0.5.1

## [0.5.0] - 2026-08-26

### Added

- Unified Pool Gateway: one public Relay API, identity, task authority, thread,
  session, Goal and canonical history over a hidden credential pool.
- State-v3 `PoolQuotaLedger`, sticky-until-depleted source selection, atomic
  leases, heartbeats, crash recovery and a continuously verified v2 rollback
  projection.
- Local Responses transport that retries the exact request before output, keeps
  the source transition private, and blocks unsafe replay after side effects.
- Contract-v2 public status projections that expose only `Codex Relay Pool`;
  source metadata remains in token-protected account management.
- Real installed `codex.real.exe` E2E coverage with a deterministic A→B→C→D
  upstream fixture, plus bilingual documentation for installation, security,
  compatibility and evidence reporting.

### Fixed

- Streaming `response.failed` events that report only a human-readable
  `usage limit`/`rate limit` message now trigger the same pre-output retry as
  structured quota codes. The pool status also retains a bounded error code
  and message so Usage & billing can explain a terminal Relay failure instead
  of leaving the native UI with only an exclamation mark.
- Background management and initialization failures (`account/*`,
  `app/*`, plugin, and MCP refreshes) no longer appear as public Relay error
  toasts when the app is merely starting. The native response is still
  forwarded to the requesting settings surface, and the bounded diagnostic
  remains available in Relay account/Usage status; task- and turn-scoped
  failures still show their actionable error notification.

### Changed

- Usage & billing and task badges now show one aggregate pool rather than
  per-worker routing identities. The native Settings shell and official Codex
  process remain untouched.
- Late quota failure advances the source for future turns but keeps the already
  observable turn in `recovery-required`; all-depleted failures do not fabricate
  a completed turn or leave a live lease.

### Validation

- Go state, gateway and mux suites pass, including CAS/concurrency, migration,
  rollback, sanitization, task lease persistence and real app-server E2E.
- Four authorized real-account Relay smoke turns passed through isolated source
  homes. No source returned a live quota rejection in that run, so real
  A→B→C→D depletion evidence remains intentionally unclaimed.

Release: https://github.com/LightHaru/codex-relay/releases/tag/v0.5.0

## [0.4.5] - 2026-08-25

### Fixed

- Quota routing now cross-checks each isolated app-server snapshot with that
  subscription's native Usage response. Explicit `allowed`/`limit_reached`
  signals and both quota windows are evaluated together; `allowed=true` can no
  longer make a still-100%-used window eligible.
- A real upstream `usageLimitExceeded` rejection now has higher precedence than
  cached Usage data and immediately invalidates that account's quota cache.
  Duplicate `error` plus `turn/completed` notifications count as one circuit
  failure instead of extending cooldown twice.
- Open quota circuits remember the blocking reset generation. A fresh snapshot
  from the same epoch remains probationary, while a newer reset epoch with
  confirmed capacity safely returns the account to the pool. A successful
  probation turn remains the fallback recovery proof for older Codex builds.
- Goal-owned turns that start inside app-server are now recognized even when no
  renderer `turn/start` exists. At a terminal quota failure, Relay transfers
  the exact durable rollout and Goal state to another eligible worker without
  replaying the failed turn or any earlier command/tool side effect.
- Canonical checkpoints query the source app-server's exact active rollout
  path. Disk fallback now selects the newest sibling generation, preventing a
  locked stale Windows rollout from replacing newer task history during
  failover.

### Validation

- Added regressions for conflicting Usage flags/windows, explicit quota deny,
  reset-generation circuit recovery, camelCase and snake_case quota errors,
  harmless user text containing “quota”, duplicate terminal events, autonomous
  Goal failover without `turn/start` replay, and newest-generation history
  selection.
- Verified against the generated app-server schema from Codex Windows
  Store `26.818.8289.0` (`codex-cli 0.149.0-alpha.4.3`) and the isolated
  multi-account Usage payload shape. Its changed renderer aliases are recorded
  under an exact reviewed profile with an exact-anchor fixture.

## [0.4.4] - 2026-08-25

### Fixed

- Windows handoffs no longer fail when the target app-server retains a
  non-delete-sharing handle to an older rollout. Relay first attempts the
  normal atomic replacement, then installs the already verified canonical
  checkpoint as a unique immutable sibling generation when Windows returns
  access denied.
- The exact installed generation is passed to `thread/resume` and remains
  subject to the existing target-home, SHA-256, and size verification before
  ownership can commit. The locked old rollout is neither modified nor treated
  as authoritative.
- Relay asks target and former source workers to release stale task handles.
  If a target still rejects the exact path, Relay may restart only that idle,
  Router-owned app-server child and retry once. The native Store Codex process
  is never targeted.

### Validation

- Added a deterministic Windows-permission regression proving that a locked
  destination preserves its old bytes while the canonical bytes are installed
  in a discoverable sibling rollout.
- Re-ran canonical-path, legacy path-unsupported, failed-resume rollback, and
  goal-transfer integration coverage.
- Completed a real affected-task handoff from Subscription 4 to Subscription 2:
  ownership committed at generation 8, the turn completed, and the target
  rollout matched the canonical checkpoint by path containment, SHA-256, and
  size. See `docs/evidence/v0.4.4-locked-rollout-e2e-2026-08-25.md`.

## [0.4.3] - 2026-08-25

### Fixed

- Cross-account resume now prefers the exact canonical rollout materialized
  inside the target subscription home. This overrides a stale Codex
  `state_5.sqlite` row that still points the same thread ID at the former native
  `.codex` sessions directory and prevents the repeated
  `existing chat history is outside the source sessions directory` failure.
- Post-resume verification remains fail-closed: the returned path must resolve
  inside the target worker's managed history root and its SHA-256 and size must
  equal the committed canonical checkpoint. Older app-server builds that reject
  path-based resume retain the verified ID-only compatibility fallback.

### Validation

- Added an integration regression with a stale external target index plus a
  valid target-home replica; the handoff must resume by the replica path and
  commit before the turn is dispatched.
- Added a compatibility regression proving that a reviewed path-unsupported
  app-server can still use ID-only resume only when target-path verification
  succeeds.

## [0.4.2] - 2026-08-24

### Fixed

- Balanced routing now uses one deterministic tie-break contract for both the
  real scheduler and its read-only next-worker preview. Normalized service
  (`dispatches / current quota weight`) prevents a historically overused,
  low-quota worker from jumping back ahead of underused full-quota workers;
  weighted deficit smooths rotation inside the least-served tier.
- The preview no longer disagrees with the next real dispatch when candidates
  have equal quota-weighted scores.

### Validation

- Added a five-subscription anti-starvation regression: all five full-quota
  workers must receive one dispatch before any worker repeats.
- Added a mixed-pool regression proving that four unused 100% workers are
  selected before a previously active worker with 31% remaining.
- Added a persisted-history regression matching the production imbalance and
  proving that a 26% worker with five prior dispatches cannot outrank 100%
  workers with one dispatch merely because it retained old deficit credit.

## [0.4.1] - 2026-08-24

### Added

- A versioned, null-safe routing explainability contract on the existing
  token-protected Router status and thread-route APIs. It distinguishes Relay
  Controller, current task owner, active turn worker, last completed worker,
  last quota-consuming worker, previous worker, and the next-candidate preview.
- Per-worker eligibility, health/circuit state, confirmed quota windows,
  freshness, reservation/dispatch data, scheduler deficit and normalized score
  components with fixed selected/skipped reason codes.
- A bounded per-task route timeline derived from the authoritative decision,
  turn and handoff journals. Stable event IDs deduplicate reservations, worker
  selection, completion, rollback, quota attribution, handoff phases, recovery
  and circuit events across refreshes and restarts.
- Truthful before/after quota attribution. Relay reports a confirmed consuming
  worker only when a newer upstream snapshot shows a measurable decrease;
  unchanged, stale, unavailable or reset-crossing snapshots remain explicitly
  unconfirmed.
- An expandable, keyboard-accessible Routing Inspector beside the native task
  composer. The compact row shows the actual worker, mode, last worker,
  confirmed quota and next preview; expanded sections explain selection,
  skipped accounts, timeline, handoff generations and the additive pool.
- Session-deduplicated, non-blocking notifications for real quota failover,
  handoff failure, recovery-required, all-depleted and safe policy downgrade
  events.

### Changed

- Sticky and Rotate previews now reflect their real next-turn semantics without
  mutating cursor, deficits, dispatch counts, reservations, ownership, handoff
  state or circuit state. Balanced preview remains score ordered.
- The pool projection now includes its oldest confirmed quota timestamp and
  explicitly identifies the number of eligible, depleted and unknown workers.
- Handoff journals retain a fixed reason code and safe explanation so source,
  target and generation changes remain verifiable after restart.
- English and Vietnamese community copy now explains that `500%` is routing
  capacity across five isolated subscriptions, never one merged OpenAI plan or
  billing balance.

### Security

- Explainability projections redact full email/username fields, absolute rollout
  paths and arbitrary stored errors. Decisions, health and handoff reasons are
  converted to bounded Router-owned text before they reach renderer JSON or SSE.
- No prompt, Goal objective, tool arguments, file contents, OAuth material,
  cookies, passwords, `auth.json`, workspace path or control token is added to
  the timeline or quota-attribution ledger.

### Validation

- Added deterministic Go tests for non-mutating preview, worker-role separation,
  fixed skipped reasons, handoff source/target/generation, stable event IDs,
  reservation rollback, truthful quota attribution and hostile diagnostic data.
- Added Windows renderer tests for the bilingual expandable inspector, owner /
  active / last / preview separation and one-toast-per-event behavior while
  retaining the native Settings shell and in-flow Usage & billing panel.

## [0.4.0] - 2026-08-23

### Added

- Canonical Relay Memory with per-task route, null-safe goal checkpoint,
  append-only turn/decision ledgers, migration journals, generation
  checkpoints, and an incrementally materialized authoritative rollout.
- Persistent quota-weighted deficit scheduling with atomic reservations,
  low-water reserve, fresh-known-quota priority, probation fallback, restart-
  safe cursor/deficits, exact failed-dispatch rollback, account health, and a
  refresh-gated circuit breaker.
- Per-thread turn coordination, side-effect/idempotency evidence, one active
  worker per logical turn, generation-based late-event suppression, and
  recovery-required handling when blind replay would risk duplicate commands or
  file edits.
- Transactional `PREPARED → COPIED → RESUMED → COMMITTED` handoff with stable
  rollout snapshots, prefix-verified incremental append, SHA-256/size checks,
  target `thread/read` identity verification, atomic owner commit, and startup
  rollback for interrupted phases.
- Sticky, Balanced, and Rotate-every-completed-turn policies; token-protected
  router status/thread route/decision/policy/recovery APIs; sanitized routing
  SSE; and account-skipped/circuit/handoff/turn events.
- Native-flow Windows route controls in the profile menu and task composer.
  Relay Controller and Current Task Route are distinct, and current worker,
  generation, effective policy, next candidate, handoff, and recovery state
  refresh over SSE without overlaying Settings or the sidebar.
- A pool-first quota contract and UI: five connected subscriptions contribute
  a maximum of `500%`, confirmed remaining quota is added (for example
  `155% / 500%`), and per-worker rows are collapsed under diagnostics. Upstream
  credentials and entitlements remain isolated while tasks consume the pool
  through the scheduler.
- A complete 48-row fixture test matrix, Windows junction/reparse-point escape
  protection, exact reviewed-manifest capability gating, and fail-closed Sticky
  behavior for missing or unknown app-server profiles.

### Changed

- State schema v1 owner maps migrate to v2 routes/scheduler/journals with a
  retained v1 backup, atomic writes, last-valid backup recovery, and dual owner
  compatibility for the transition release.
- New and existing task turns now share the persistent scheduler. One logical
  turn keeps the same reservation across selection, handoff, send, retry, and
  completion, so an abandoned candidate cannot retain dispatch credit.
- Routing diagnostics persist request hashes and fixed failure categories
  instead of raw prompts, paths, file contents, or arbitrary upstream errors.
- The Windows installer emits safe-handoff capability only for one of the five
  exact reviewed ASAR hashes, including Store profiles `26.818.4152.0` and
  `26.818.5229.0`.

### Fixed

- A new task's first turn stays on the worker that accepted `thread/start`.
  Balanced/Rotate handoff begins only after a completed-turn boundary, when an
  authoritative rollout exists, preventing a first-turn `history_not_found`
  race.
- App-server tooling subcommands such as `generate-json-schema` bypass the
  interactive sandbox argument rewrite, so installer/profile discovery works
  with current and older reviewed Codex builds.
- A `userMessage` item is no longer treated as visible assistant output. A
  quota rejection after user input but before assistant output or side effects
  can therefore fail over safely; genuine assistant output still fails closed
  to recovery-required.
- Active Goal state is transferred explicitly during a committed worker
  handoff because `thread/goal/*` state is app-server-local rather than part of
  the rollout alone. The target verifies the objective/status before ownership
  commits; `usageLimited` becomes `active` on a capacity-bearing target, and a
  token budget is reduced by already consumed tokens to preserve its remaining
  safety bound.

### Security

- Source and target history validation rejects Windows junction/reparse-point
  escapes as well as POSIX symlinks, traversal, non-regular files, and rollout
  files over 1 GiB.
- Official Codex and Relay credentials remain isolated: each child retains a
  separate `CODEX_HOME`, `CODEX_SQLITE_HOME`, app-server, callback, and
  `auth.json`; Canonical Relay Memory never stores tokens or cookies.

Release: https://github.com/LightHaru/codex-relay/releases/tag/v0.4.0

## [0.3.12] - 2026-08-22

### Added

- Windows Settings → Usage & billing now keeps the official native page shell
  and mounts an in-flow **All connected subscriptions** panel inside its
  content column. It does not add a sidebar item, replace the Settings shell,
  or use a fixed overlay.
- Every account card shows its own plan, credits, quota windows, reset times,
  reset-credit list, and bounded billing payload details. A failed account is
  marked **Unavailable** without hiding healthy subscriptions.

### Fixed

- The current Windows Store renderer routes Usage, reset-credit queries, reset
  mutations, selected usage windows, and the native reset picker through the
  isolated Relay account bridge.
- A Relay Usage bridge failure now fails closed instead of falling through to
  the official Codex browser session, preventing native/Relay account mixing.

Release: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.12

## [0.3.11] - 2026-08-22

### Fixed

- Isolated-mode startup now migrates every legacy subscription entry that
  points at the official `%USERPROFILE%\.codex` home, not only the old
  `Primary` row. The official credential and history files remain untouched;
  the migrated Relay row requires its own sign-in and stale Router ownership
  metadata is cleared.

Release: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.11

## [0.3.10] - 2026-08-22

### Fixed

- Account settings now render native-style **Usage limit resets** cards with a
  scoped **Use reset** action, refresh the selected account after redemption,
  and never consume another subscription's credit.
- Pending browser sign-in intent is persisted per secondary subscription and
  restored after a Relay restart. Disconnected stale accounts no longer show a
  false **Waiting for sign-in** row or cancellation action.
- Unrecorded Windows Store bundles can opt into structural renderer-anchor
  discovery; minifier aliases are inferred only when every feature anchor is
  unique, otherwise the installer fails closed without touching the old copy.

Release: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.10

## [0.3.9] - 2026-08-21

### Fixed

- Windows Relay now uses its own primary `CODEX_HOME` and file-backed
  credential store under `%APPDATA%\Codex Relay\codex-home`; it no longer
  inherits the official Store app's `%USERPROFILE%\.codex` account. Existing
  Relay state is migrated without copying or deleting native credentials, and
  old native-chat owner mappings are dropped from Relay only.
- Quota failover now migrates rollouts returned as absolute or
  `CODEX_HOME`-relative paths by current and older Codex app-server builds.
- A turn that is accepted and then rejected asynchronously by an exhausted
  subscription is now retried on the next available account without leaking
  the native quota error to the desktop client.
- The native Usage bridge now prefers a connected subscription with remaining
  capacity, so an exhausted Relay Primary no longer shows a false
  out-of-messages banner while another subscription can continue the chat.
- Opening an older chat now imports its single legacy rollout into the selected
  Relay account before `thread/resume`, so the chat no longer depends on the
  official Store app's history path after the first successful open.
- Archived chats under `archived_sessions` can now move to another connected
  subscription instead of failing with an "outside the source sessions"
  error.
- Windows extended-length rollout paths (`\\?\C:\...` and extended UNC paths)
  are normalized before the source-history boundary check, so valid chats from
  the current Codex desktop build can be migrated safely.
- Legacy chats without a Router owner mapping are resolved by scanning managed
  rollout history, and startup restores the configured Relay `Primary` account
  metadata so Relay-owned chats can resume after routing is enabled.
- History migration remains restricted to Codex-managed history directories,
  validates symlink resolution, and never modifies the source rollout.
- New chats now use strict round-robin dispatch across connected subscriptions
  with known remaining capacity; quota percentage no longer pins every chat to
  the account with the lowest usage.
- Relay account initialization is concurrent, so a disconnected subscription
  cannot block the desktop handshake for the rest of the pool.
- The native Windows **Usage & billing** page is left unchanged. Account
  settings now show **Usage limit resets** and the full reset-credit details for
  each connected subscription instead.
- Account settings now present each available reset as a native-style card with
  a scoped **Use reset** action; the result refreshes only that subscription's
  balance and never consumes another account's credit.
- Browser sign-in intent is persisted per secondary subscription. Relay restores
  a real unfinished flow after restart, while disconnected stale accounts no
  longer masquerade as **Waiting for sign-in** rows.
- Windows upgrades can structurally discover minifier aliases for an unrecorded
  Store bundle when explicitly requested with `--allow-untested-source`; every
  renderer anchor is still required exactly once and ambiguous updates fail
  closed.

Release: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.9

## [0.3.8] - 2026-08-21

### Fixed

- Windows Usage & billing aggregation is now mounted only inside the native
  Usage subpage, directly after its title and description and before **Your
  plan**. It no longer occupies the Settings shell, sidebar, or neighboring
  navigation content.
- Existing Relay Usage cards are relocated to the correct title anchor when a
  renderer rerenders the Settings page, preventing duplicate or full-page
  overlays.

Release: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.8

## [0.3.7] - 2026-08-21

### Added

- Windows Settings → Usage & billing now shows an **All connected
  subscriptions** panel. Every account is fetched through its isolated Relay
  credential and displayed with its own plan, credits, rate-limit windows,
  reset metadata, spend controls, reset-credit counts, Code Review limits, and
  future Usage fields returned by ChatGPT.
- Added the token-protected `/v1/usage/all` control route. It fetches accounts
  independently, keeps partial failures visible, and never fabricates a
  combined billing balance or redirects account-specific billing actions.
- Added Go and Windows renderer tests for multi-account Usage rendering,
  partial credential failures, unconnected subscriptions, and preservation of
  new upstream Usage fields.
- The Usage panel now selects the smallest content-column ancestor, keeping it
  inside the native Codex settings page instead of inserting it beside the
  sidebar.

### Documentation

- Updated the English and Vietnamese Usage & billing instructions, Windows
  deployment notes, and architecture documentation for the multi-account
  dashboard and its account-scoped billing safety rules.

Release: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.7

## [0.3.6] - 2026-08-20

### Fixed

- Windows **Add another subscription** now hands the official ChatGPT OAuth
  link to the user's default browser. This avoids embedded Electron/Cloudflare
  callback failures while the isolated Codex app-server continues to own the
  localhost callback and credential storage.
- Login flow identifiers now use a strict opaque format so older Electron/Node
  builds cannot reject a valid callback session before polling the Relay.
- The sign-in dialog accepts both `authUrl` and `auth_url` response spellings
  for compatibility with older app-server builds, and keeps the unfinished
  account available for a retry when the browser reports an OAuth error.

## [0.3.5] - 2026-08-20

### Fixed

- Windows Settings → Usage no longer crashes when the reset-account picker
  subscribes to quota state. The renderer patch now calls the React namespace
  belonging to the reviewed `26.818.2441.0` bundle instead of an unrelated
  MCP schema symbol.
- The Windows updater preload now imports Electron's `contextBridge` and
  `ipcRenderer` explicitly, so the in-app update bridge loads in Electron's
  sandboxed preload environment.

## [0.3.4] - 2026-08-20

### Fixed

- Windows Settings → Usage now authorizes Chromium's Private Network Access
  preflight for the trusted packaged renderer. This completes the loopback
  bridge fix for secure `app://-` builds, which could still show the generic
  **“Oops, an error has occurred”** page after the 0.3.3 CORS fix.

## [0.3.3] - 2026-08-20

### Fixed

- Windows Settings → Usage now works from the packaged renderer. The local
  Usage bridge accepts the opaque `null`/`file://` Origin emitted by Electron's
  `file://` page, while retaining an explicit allowlist and token protection.
  This removes the generic **“Oops, an error has occurred”** screen caused by
  the browser rejecting the local request during CORS preflight.

## [0.3.2] - 2026-08-20

### Fixed

- Windows source-release updates now pass the extracted checkout correctly to
  the local installer. The installer recognizes the old 0.3.1 updater's
  checkout-shaped `-Source` argument instead of treating the repository as the
  official ChatGPT app, so **Update now** can complete without a manual repair.
- The one-command bootstrap and future updater helpers no longer pass an
  extracted source checkout as an official Store app path.

## [0.3.1] - 2026-08-20

### Added

- Fair-share selection for new chats across all enabled, connected
  subscriptions with capacity. The selector favors lower current quota use and
  alternates comparable accounts instead of locking new work to Primary.
- Sticky quota failover coverage for chats created before Relay: an unassigned
  old chat starts at Primary to read history, then migrates to an account with
  capacity instead of returning Primary's depleted-quota error.
- Bounded same-account retries for `Selected model is at capacity` that retain
  the exact original selected model and request payload.
- A token-protected Settings → Usage proxy that reads the normal native usage
  payload with isolated account credentials, avoiding a mismatched Store browser
  session; the renderer falls back safely to its native request if unavailable.
- A reviewed one-command Windows bootstrap asset. It validates the published
  source archive URL, SHA-256, and archive paths before running the existing
  staged local installer.

### Changed

- The Windows renderer patcher now requires one additional exact Usage anchor
  for each supported Store `app.asar` profile and fails closed if it changes.
- Updated Vietnamese and English user documentation, Windows deployment notes,
  release instructions, and contributor policy for fair-share routing, old
  chat failover, exact-model retry, Usage recovery, and the one-command first
  install.

## [0.3.0] - 2026-08-20

### Changed

- Renamed the user-facing product and canonical repository to **Codex Relay**.
  The internal `codex-mux` state and the update manifest product identifier are
  intentionally retained so installed 0.2.x copies can update without losing
  account state, thread ownership, or Electron profile data.
- Windows upgrades stage the new Relay copy first, stop only the old managed
  Router root, and move that legacy copy into `~/.codex-mux/backups` before
  launching `%LOCALAPPDATA%\Codex Relay\app`.
- Reworked the Vietnamese and English installation/update guides around the
  direct `Codex Relay` shortcut, automatic in-app source updates, migration,
  compatibility checks, and recovery steps.

## [0.2.0] - 2026-08-20

### Added

- One-command installer with safe source updates, prerequisite checks, signed
  rebuilds, recoverable upgrades, and automatic launch.
- Reset-aware routing that prioritizes weekly quota at risk of expiring and
  gives a bounded boost to subscriptions with banked usage resets.
- Windows x64 preview patcher that creates an independent copy of the Microsoft
  Store app, routes it through `codex-mux.exe`, and leaves the official package
  unchanged.
- Windows profile-menu bridge with pooled usage, connected subscription rows,
  an in-app official browser **Add another subscription** flow, and a
  double-click local-source installer that creates a direct Desktop shortcut.
- Windows browser-login completion closes its dialog automatically and shows
  one non-blocking connected notification, with stale-poll protection.
- Pending Windows sign-ins can be cancelled safely: the Router cancels the
  official child flow, preserves an account that completed in the cancellation
  race, and removes only an unconnected secondary account and its isolated
  home.
- Primary-first routing for new threads, plus short-window capacity checks so
  an available secondary is selected before an avoidable quota failure.
- Version-pinned Windows renderer ports for combined/selected Profile
  statistics, account-scoped Plugins Apps/MCP/OAuth RPCs, and per-account
  rate-limit reset selection.
- Focused Windows bridge and renderer-anchor regression checks.
- Windows compatibility profile for the newer Microsoft Store `26.818.2441.0`
  bundle while retaining the previously reviewed `26.810.7004.0` profile.
- Hash-verified in-app source-release updates with an external Windows helper,
  safe staging, Router-only restart, and automatic relaunch.

## [0.1.0] - 2026-08-15

### Added

- Multi-subscription routing with quota-aware balancing and sticky threads.
- Account isolation, device-code sign-in, pooled usage, and quota failover.
- Native account menu, masked emails, plan labels, and profile photos.
- Combined Profile statistics with per-account selection.
- Account-scoped Apps and MCP connection state in Settings → Plugins.
- Per-account rate-limit reset selection and pooled depletion handling.
- Independently signed Appshots and Computer Use support.
- Fail-closed upstream compatibility checks and deepest-first nested helper signing.
- Loopback-only, token-authenticated diagnostic UI states.
- Source-only CI, draft release automation, security documentation, and smoke tests.

[Unreleased]: https://github.com/LightHaru/codex-relay/compare/v0.5.9...HEAD
[0.5.9]: https://github.com/LightHaru/codex-relay/releases/tag/v0.5.9
[0.5.8]: https://github.com/LightHaru/codex-relay/releases/tag/v0.5.8
[0.5.7]: https://github.com/LightHaru/codex-relay/releases/tag/v0.5.7
[0.5.6]: https://github.com/LightHaru/codex-relay/releases/tag/v0.5.6
[0.5.5]: https://github.com/LightHaru/codex-relay/releases/tag/v0.5.5
[0.5.4]: https://github.com/LightHaru/codex-relay/releases/tag/v0.5.4
[0.5.2]: https://github.com/LightHaru/codex-relay/releases/tag/v0.5.2
[0.5.3]: https://github.com/LightHaru/codex-relay/releases/tag/v0.5.3
[0.5.1]: https://github.com/LightHaru/codex-relay/releases/tag/v0.5.1
[0.5.0]: https://github.com/LightHaru/codex-relay/releases/tag/v0.5.0
[0.4.5]: https://github.com/LightHaru/codex-relay/releases/tag/v0.4.5
[0.4.4]: https://github.com/LightHaru/codex-relay/releases/tag/v0.4.4
[0.4.3]: https://github.com/LightHaru/codex-relay/releases/tag/v0.4.3
[0.4.2]: https://github.com/LightHaru/codex-relay/releases/tag/v0.4.2
[0.4.1]: https://github.com/LightHaru/codex-relay/releases/tag/v0.4.1
[0.4.0]: https://github.com/LightHaru/codex-relay/releases/tag/v0.4.0
[0.3.12]: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.12
[0.3.11]: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.11
[0.3.10]: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.10
[0.3.9]: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.9
[0.3.8]: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.8
[0.3.7]: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.7
[0.3.6]: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.6
[0.3.5]: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.5
[0.3.4]: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.4
[0.3.3]: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.3
[0.3.2]: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.2
[0.3.1]: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.1
[0.3.0]: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.0
[0.2.0]: https://github.com/LightHaru/codex-relay/releases/tag/v0.2.0
[0.1.0]: https://github.com/LightHaru/codex-relay/releases/tag/v0.1.0
