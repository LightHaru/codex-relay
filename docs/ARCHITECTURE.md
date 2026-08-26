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

### Sticky source state machine

```text
PROBATION → ACTIVE_SOURCE → (quota evidence) → DEPLETED
                                      │
                                      ▼
                                next eligible source
```

The current source is reused for every new request until explicit evidence says
it cannot continue. Valid evidence is a structured quota/rate-limit rejection,
`quota_exhausted`, `usage_limit`, `limit_reached=true`, `allowed=false`, a
100%-used reported window, or an explicit app-server credential rejection.
Timeouts and generic network failures never prove quota exhaustion. Unknown
quota is probation-only and is not represented as confirmed headroom.

At A→B, the same request bytes, model, session, thread and logical turn are
retried before any visible output. The authority connection and public identity
remain unchanged. There is no round-robin, fair-share, pre-emptive balancing,
new task, or owner-change event.

## Output and side-effect safety

The transport buffers a bounded prefix of SSE data. It may retry a structured
quota failure while no assistant output, tool call, approval, command, file
mutation or other side effect has been committed. Once a visible item or side
effect is observed, replay is unsafe: the lease becomes `RECOVERY_REQUIRED`,
the source is depleted for future turns, and Relay emits a pool-level recovery
event without exposing upstream account details. It does not claim a seamless
continuation unless the upstream protocol supplies and the test proves a
continuation primitive.

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

On startup, expired leases become recovery-required, TaskRecords remain bound
to the same thread, and v3 state is loaded without choosing a new worker. A
continuous `state.json.v2.rollback` projection contains a conservative v2
view with no reservations and recovery markers for active tasks. Its manifest
contains a SHA-256 and source pool revision.

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
