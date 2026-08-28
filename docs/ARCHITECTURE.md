# Unified Pool Gateway architecture

## Product boundary

Codex Relay exposes one logical gateway to the desktop client. The public
contract has one Relay API, one identity (`relay`), one task authority, one
thread/session identity, one Goal state and one canonical history. Connected
ChatGPT subscriptions are credential sources only. They are never public task
owners and a credential transition is never a chat move.

```text
Codex client
    │ POST /v1/responses (local custom provider)
    ▼
RelayGatewayWorker
    ├── TaskAuthority (one app-server connection)
    ├── PoolQuotaLedger (state v3)
    ├── canonical Relay Memory
    └── hidden credential transport
          ├── source A
          ├── source B
          ├── source C
          └── source D
```

The per-source app-server processes that remain in the process table are
management-only children for sign-in, account settings and quota probes. They
do not receive ordinary thread, turn, Goal, tool or approval traffic. The
single task authority is the only child receiving the public task stream.

## Components

### RelayGatewayWorker

The worker owns the client connection, thread and session IDs, logical turn IDs,
Goal lifecycle, tool/approval callbacks, canonical history and output stream.
It starts one loopback listener with a random bearer token. The custom provider
configuration is installed only in the authority home:

- `model_provider = relay_pool`;
- `base_url = http://127.0.0.1:<random>/v1`;
- `wire_api = responses`;
- `env_key` is the loopback token;
- upstream credentials are never written into the authority configuration.

Public `RouterStatus` and `ThreadRouteStatus` contract v2 return the stable pool
identity and aggregate state. Source IDs, emails, worker lists, candidates,
handoffs and raw error text are management/diagnostic data, not normal task
protocol data.

### PoolQuotaLedger

`state.PoolState` is the single source of routing truth. It stores pool ID and
revision, source membership/evidence, active source, active leases, normalized
headroom, reset metadata, health, failover count and the last transition. The
per-source evidence remains private so the ledger can make an informed choice:
`allowed`, limit reached, short/long windows, reset epoch, observation time,
authentication and circuit state.

`TaskRecord` binds one thread to canonical generation/checkpoint, Goal ID when
available, the active lease and recovery state. `PoolLease` binds one logical
turn to one pool, session, thread and credential source. CAS updates and one
transition record make concurrent quota notifications idempotent.

### Quota-aware pool state machine

```text
PROBATION → ELIGIBLE → (fair-share dispatch) → ACTIVE_SOURCE
                                  │
                                  └── (quota evidence) → DEPLETED
                                      │
                                      ▼
                                next eligible source
```

The pool cursor selects the next confirmed eligible source for each request;
the last selected source is retained only for diagnostics. Valid evidence is a
structured quota/rate-limit rejection,
`quota_exhausted`, `usage_limit`, `limit_reached=true`, `allowed=false`, a
100%-used reported window, or an explicit app-server credential rejection.
Timeouts and generic network failures never prove quota exhaustion. They update
a separate transport-health circuit (`suspect` → `cooldown` → eligible probe)
without disconnecting the source or changing its quota evidence. Unknown quota
is probation-only and is not represented as confirmed headroom.

The Gateway reads both quota windows and uses a persistent cursor to fair-share
requests across confirmed eligible sources. A source with explicit quota
exhaustion is removed from the eligible set; unknown probation sources are held
back while confirmed sources remain. At A→B, the same request bytes, model,
session, thread and logical turn are retried before any visible output. The
authority connection and public identity remain unchanged: changing a hidden
credential is not a worker handoff, task move or owner-change event.

## Output and side-effect safety

The transport buffers a bounded prefix of SSE data and requires an explicit
Responses terminal event. A clean EOF without `response.completed`,
`response.failed`, or `response.incomplete` is an incomplete stream, not a
success. Relay may retry a structured quota rejection or transient
connection/HTTP/stream failure while no assistant output, tool call, approval,
command, file mutation or other side effect has been committed. Once a visible
item or side effect is observed, replay is unsafe: the lease becomes
`RECOVERY_REQUIRED` and Relay emits a pool-level recovery event without
exposing upstream account details. It does not claim a seamless continuation
unless the upstream protocol supplies and the test proves a continuation
primitive.

The final `response.output_item.done` frame is not itself a terminal response;
its nested item commonly has `status="completed"`. Relay classifies terminal
events from the top-level SSE event/envelope only, holds that boundary for a
bounded grace window, and sends an invisible keepalive while waiting. If the
provider remains silent or the native client closes the request, the held frame
is followed by a standards-shaped recovery terminal. The unified mux keeps the
failed-turn/recovery state but removes the native generic
`stream disconnected before completion` prefix from the user-facing message.

When all sources are depleted, the request ends with one sanitized pool error;
the failed turn is not recorded as completed. A reset or a newly connected
source can serve the same task later. Successful completion clears the active
lease and recovery marker without changing the task identity.

## Canonical history and restart

The authority home is the only writable task home. Existing chats are verified
against a Relay canonical checkpoint; source files are regular JSONL files under
managed `sessions`/`archived_sessions` roots. Paths are containment-checked,
symlink/reparse escapes are rejected, bytes and SHA-256 are verified, and
Windows access-denied replacement uses an immutable sibling generation. The
official `%USERPROFILE%\\.codex` home is never used as Relay's writable home.

Before the loopback Gateway starts listening, startup recovery distinguishes
the commit boundary. A persisted lease with no visible output or side effect is
released atomically so the native app may replay the same request ID after a
crash/reboot. A committed or side-effecting lease becomes
`RECOVERY_REQUIRED` and cannot be replayed. Concurrent duplicates inside one
process join a single in-flight response and a bounded 30-second replay cache;
only one attempt may commit. TaskRecords remain bound to the same thread, and
v3 state is loaded without choosing a new worker. A continuous
`state.json.v2.rollback` projection contains a conservative v2 view with no
reservations and recovery markers for active tasks. Its manifest contains a
SHA-256 and source pool revision.

## UI and management boundary

The native Settings shell remains authoritative. The pool summary is inserted
only in the content column of Settings → Usage & billing. It does not add a
sidebar item or fixed overlay and does not replace other Settings pages. Source
emails and sign-in/remove controls remain in the account-management dialog.
Normal task badges show only pool name, generation, aggregate remaining quota,
health and recovery state.

## Compatibility and release boundary

Renderer patching is exact-anchor and version-profile based. Unknown Store
bundles fail closed. The router core is version-neutral only after the actual
app-server `responses` schema and headers are probed. A release is not claimed
compatible merely because Go tests pass; it needs the profile fixtures,
app-server probe, migration tests and (when authorized) live evidence.

Do not publish, push, overwrite the production app or install over a running
Relay until all local gates pass and the release operator confirms the step.
