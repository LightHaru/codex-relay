# Architecture

> The routing core now uses the v2 shared-memory scheduler, generation and
> transactional handoff design documented in
> [`SHARED-MEMORY-ROUTER.md`](SHARED-MEMORY-ROUTER.md). Where older wording below
> describes strict round-robin or permanently sticky ownership, the v2 document
> is authoritative.

The independently built desktop uses bundle identifier `app.cdxmux.multi`; its
Computer Use helper uses `com.cdxmux.sky.CUAService`. Neither identifier is used
by the official ChatGPT installation. These identifiers and the `.codex-mux`
state directory remain stable across the product rename so existing macOS
privacy grants, connected accounts, and persisted thread routes continue to
work.

Codex Relay replaces the copied app's bundled `codex` executable
with a small Go multiplexer and keeps the original binary beside it as
`codex.real`.

## Request routing

The desktop app opens one JSON-RPC app-server connection to the multiplexer.
The multiplexer starts one real app-server child for every enabled account,
each with its own `CODEX_HOME` and `CODEX_SQLITE_HOME`.

New threads use persistent weighted-deficit round robin across enabled,
connected subscriptions with capacity in every reported quota window. Fresh
short/long quota sets the weight, a low-water reserve protects the final 5%,
active reservations prevent over-allocation, and unknown quota remains a
last-resort pool. Deficits and cursor survive restart.
Quota eligibility fuses the isolated child's `account/rateLimits/read` result
with that account's authenticated native Usage projection. A 100%-used window
or explicit deny is ineligible even if another flag says `allowed=true`.
Observed turn rejection is the strongest signal and invalidates cached Usage;
an open circuit closes from a newer reset generation with confirmed capacity,
or from a successful half-open probation turn when an older build exposes no
stable reset generation.
The controller/Primary owns shared Relay configuration but is not a routing
lock for new chats. On Windows, it is never used as a bridge to the Store app's
native home. Once a thread ID is known, `state.json` persists its owner.
Requests, responses, approvals, and notifications are rewritten only as needed
to preserve one coherent desktop session. The initial `initialize` handshake is
sent to all children concurrently so one disconnected account cannot block the
whole desktop connection.

Worker selection follows Sticky, Balanced (default), or Rotate policies and is
evaluated only at completed-turn boundaries. A change checkpoints canonical
Relay Memory, incrementally materializes the rollout, resumes the target, and
only then advances ownership generation. A legacy/unassigned thread that is already in a Relay-owned home is
located from that home before the same failover path is applied. If a known
thread still points at the former native Store `sessions` directory, Relay may
read and copy that single rollout into the selected Relay home before resume;
it never reads native credentials/configuration, starts a child there, or edits
the source file. Store-only history is not scanned or imported in bulk.

Each logical turn has a persistent attempt ID and request hash. The per-thread
coordinator rejects a second active `turn/start`, keeps approval/tool callbacks
bound to the child that created them, and suppresses late notifications from a
superseded route generation. Immediate or pre-side-effect quota rejection can
select a new worker once; a quota failure after visible output or a side effect
marks the task `recovery-required`.
Goal continuations may originate inside app-server without a renderer
`turn/start`. A quota-failed terminal Goal turn is therefore deduplicated by
thread/turn/account and handed off from its completed rollout boundary; no
previous request, command, or tool side effect is replayed.

The handoff journal uses `PREPARED`, `COPIED`, `RESUMED`, `COMMITTED`,
`FAILED`, and `ROLLED_BACK`. Source ownership remains authoritative through
resume and `thread/read` verification. Relay compares the target thread ID,
rollout SHA-256, and size before one atomic state transition advances owner and
generation. Startup rolls any uncommitted phase back to the source generation,
which is safe because resume alone never authorizes target output.
Checkpointing first asks the loaded source child for its exact rollout path.
When only disk recovery is available, matching locked-file sibling generations
are ranked by modification generation rather than lexical filename so a stale
original cannot replace newer history.

For the distinct upstream error `Selected model is at capacity`, the mux keeps
the exact original `turn/start` request (including `model`) and retries it on
the same account at most three times with short exponential backoff. This is
intentionally separate from quota failover: a busy model must not silently
change the selected model or consume a different subscription.

## Account isolation

On Windows, the independent Relay primary uses
`%APPDATA%\Codex Relay\codex-home`; the official Store app keeps using
`%USERPROFILE%\.codex`. Added Relay accounts use
`%USERPROFILE%\.codex-mux\accounts\<id>\codex-home`. The two desktop apps never
share a primary credential store or conversation database. On platforms where
Relay is launched without the Windows copy marker, the existing native
`~/.codex` primary behavior is retained for compatibility.

Managed configuration is copied from the Relay primary account, excluding
credential-store settings and project trust. Every Relay account, including
the primary, forces file-backed CLI and MCP OAuth credentials. When an older
Windows state file still points its `primary` entry at `%USERPROFILE%\.codex`,
Relay changes only that metadata to the dedicated home and removes the old
native-chat owner mappings; it never copies, deletes, or edits the official
Codex credentials or configuration. A requested legacy rollout may be read and
copied into an isolated Relay home as described above; the official source
file remains unchanged.

## Desktop integration

The patcher extracts `app.asar`, verifies exact upstream anchors, inserts the
account UI, disables the copied app's native self-update, and repacks the
archive with an updated integrity hash. On Windows, the native Settings → Usage
& billing page remains the shell; the version-neutral DOM bridge mounts an
in-flow **All connected subscriptions** panel only inside that page's content
column. The version-pinned renderer bridge keeps the native single-account
Usage and reset controls compatible with older profiles, while Account settings
calls the token-protected per-account `rate-limit-resets` endpoints and renders
the same **Usage limit resets** cards. The panel reads `/v1/usage/all` and never
uses the official Codex credential as a Relay fallback.
OAuth tokens stay outside the renderer. Windows also injects a small,
version-neutral update bridge. It checks the Router's source-only GitHub release
manifest, then hands a hash-verified archive to an updater executable stored
outside the managed app so the app can quit and restart safely. The official
Store package is never replaced. The app receives a separate Chromium profile
and URL scheme.

The copied Computer Use service, Node runtime, and callers are re-signed under
one Apple team. The helper uses a separate bundle identity and socket, avoiding
the official app's privacy grants and app-group container.

## Plugin behavior

Plugin definitions and managed MCP configuration are shared. The Plugins page
adds an account selector and marks Apps, MCP status, and MCP OAuth requests with
the selected account ID. The multiplexer removes that private routing marker
before forwarding the strict RPC request to the chosen child.

## Control API

The renderer talks to a loopback-only HTTP service on port 48123. All private
routes require a random 256-bit token. CORS is limited to the copied app's
known packaged renderer origins (`app://-` and the opaque `null`/`file://`
origin emitted when Windows loads `webview/index.html` from a file URL). The
trusted renderer responses also authorize Chromium Private Network Access for
the loopback target. The service exposes account metadata, per-subscription
Usage payloads with an explicit collection summary, controller-scoped native
Usage payloads, profile data, thread ownership,
login/logout actions, a persisted pending-login marker/cancellation action,
per-account reset-credit read/redeem endpoints, and an authenticated SSE event
stream; it never returns OAuth tokens. Browser sign-in
is initiated by the official child app-server, which owns the localhost
callback and writes credentials only in that subscription's isolated Codex
home.

Routing observability is exposed through token-protected
`/v1/router/status`, `/v1/thread-route`, `/v1/routing/decisions`,
`/v1/routing/policy`, `/v1/thread-route/recover`, and `/v1/events`. API
checkpoints redact the local rollout path. Routing decisions and attempt
ledgers contain fixed reasons, event types, account/thread identifiers, and a
SHA-256 request digest—not prompt text, file content, OAuth data, or arbitrary
upstream errors.

### Explainability projection (contract version 1)

Release 0.4.1 does not create a second routing state. `RouterStatus` and
`ThreadRouteStatus` project the authoritative state-v2 scheduler, account
health, `ThreadRoute`, `TurnAttempt`, decision ledger and handoff journal into a
versioned, null-safe renderer contract. The projection distinguishes the
Controller, canonical owner, active-turn worker, last-completed worker,
last-confirmed quota-consuming worker, previous worker and policy-aware next
candidate. `nextCandidateIsPreview` is always explicit.

Candidate preview clones scheduler data and performs no state write. Sticky
previews the eligible owner; Rotate previews the first eligible non-owner;
Balanced sorts the quota-weighted deficit score. Reading status cannot advance
cursor/dispatches, change deficits, reserve a turn, open a circuit, create a
migration or update ownership.

Timeline rows are a bounded merge of the existing decision, turn and handoff
journals. Deterministic IDs such as `<attempt>:turn_reserved`,
`<attempt>:reservation_rolled_back` and `<handoff>:committed` deduplicate one
logical event. The decision ledger remains capped at 1,000 and the per-task
response returns at most 100 recent rows. Terminal quota attribution runs
asynchronously and stores only before/after effective percentages and
observation times. A consuming worker is confirmed only when the newer
snapshot decreases; unchanged/reset-crossing data remains unconfirmed.
