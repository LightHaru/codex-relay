# Shared-Memory Quota Router v2

This document is the authoritative routing design. Codex Relay presents one
logical task identity while running one isolated official `codex app-server`
worker per connected Relay subscription. The official Codex installation and
`%USERPROFILE%\.codex` are outside this pool.

## Authority and invariants

- Relay Memory, not an account worker, is the logical task authority.
- A thread has one current worker generation and at most one active turn.
- A worker change occurs only at a completed-turn boundary.
- A new task's first turn stays on the worker that accepted `thread/start`;
  there is no migration until its first authoritative rollout is checkpointed.
- The source generation remains authoritative until target resume succeeds and
  the handoff commits.
- A turn is automatically retried on quota failure only before any command,
  approval, hook, file change, or tool side effect starts.
- Unknown capabilities never enable incomplete-turn resume.

`ThreadRoute` stores worker, generation, canonical SHA-256/size, active attempt,
active handoff, and recovery state. `TurnAttempt` stores phase and side-effect
evidence. The canonical rollout below `relay-memory` is refreshed after a
successful completed turn.

## Scheduling

Relay exposes one additive quota pool. Every connected ChatGPT subscription
contributes at most `100%` to pool capacity, so five fully available workers
report `500% / 500%`. A mixed pool reports the confirmed sum, such as
`155% / 500%`; quota that has not refreshed is reported separately as unknown
instead of being fabricated. This is a Relay scheduling abstraction, not a
credential or entitlement merge: upstream tokens, billing, reset credits and
rate-limit windows remain isolated per worker.

Balanced is the default. Persistent weighted-deficit round robin uses the least
remaining value across fresh short and long quota windows as weight. It skips
depleted and open-circuit workers, prefers known quota, keeps the last 5% in
reserve when another worker is safe, and accounts for active reservations.
Deficits, global cursor, per-worker dispatch counts/last-selection timestamps,
reservations, policy, health, and decisions survive restart.

The three policies are:

- `sticky`: keep an eligible current worker;
- `balanced`: retain or change the worker using the quota scheduler;
- `rotate-completed-turn`: exclude the current worker at the next safe boundary
  when another eligible worker exists.

## Incremental materialization

History inputs must be regular `.jsonl` files under managed `sessions` or
`archived_sessions` roots. Absolute, home-relative, root-relative, archived and
Windows extended-length paths are normalized by compatibility adapters. Path
traversal, symlink escapes, non-regular files and files over 1 GiB are rejected.

When the destination is a verified prefix of the stable source, only the suffix
is copied. A mismatch causes a full rebuild. Both paths write a mode-`0600`
sibling temporary file, calculate SHA-256, fsync, re-check source size/modtime,
and atomically replace the destination. The source is never modified.

## Handoff and crash recovery

The persistent journal advances through:

1. `PREPARED` — source generation remains owner;
2. `COPIED` — canonical checkpoint and target materialization match by hash and
   size;
3. `RESUMED` — target accepts the same thread ID;
4. `COMMITTED` — owner/generation advance atomically.

Goal metadata uses a separate app-server protocol from rollout history. Before
preparing a handoff, Relay reads `thread/goal/get` from the source. After target
resume and history verification, it restores the objective/status/token budget
with `thread/goal/set` and verifies the response before `COMMITTED`. A
`usageLimited` goal becomes `active` on an eligible target. For budgeted goals,
the target receives only the remaining token budget (`budget - tokensUsed`) so
a migration cannot reset and accidentally expand the safety limit. The
user-authored objective is never included in routing status, SSE, decisions or
handoff journals.

Failure restores source ownership and records `FAILED`. Startup turns any
incomplete handoff into `ROLLED_BACK`. An unfinished turn after restart becomes
`RECOVERY_REQUIRED`, releases its reservation, and blocks the next turn until
the user reviews and acknowledges the task. This deliberately favors duplicate
side-effect prevention over speculative continuation.

## Compatibility profiles

Successful `thread/read` and ID-only `thread/resume` operations are recorded as
negotiated account capabilities. Optional `cwd` and `modelProvider` resume
fields are tried first; the minimal ID-only adapter supports older reviewed
Windows app-server builds. The installer enables safe handoff only when the
source `app.asar` hash maps to an exact reviewed profile. A missing/unknown
profile makes the effective policy Sticky and disables all cross-account
copy/resume. No current profile claims safe incomplete-turn resume.

## State migration

Schema v2 persists accounts, v1 owner compatibility, routes, scheduler state,
health, capabilities, attempts, handoffs, checkpoints and a bounded decision
ledger. Schema v1 is copied to `state.json.v1.backup`, each owner becomes
generation 1, Balanced becomes the default, and v2 is atomically written.
Normal writes retain `state.json.backup`; a corrupt primary state is restored
from the last valid backup.

## Local observability

All endpoints bind to loopback and require the per-install token:

- `GET /v1/router/status`
- `GET /v1/thread-route?threadId=...`
- `GET /v1/routing/decisions`
- `PUT /v1/routing/policy`
- `POST /v1/thread-route/recover`
- `GET /v1/events` for scheduler, handoff, checkpoint, circuit and recovery SSE
  events.

The Windows profile menu leads with the additive Shared quota pool, keeps worker
rows collapsed under diagnostics, and displays Relay Controller separately from
Current Task Route. It provides Sticky, Balanced and Rotate controls. The pooled
Usage surface remains only inside the native Usage & billing page
content; it does not become a Settings overlay or sidebar item.

The task view also receives an in-flow route badge. It shows the committed
worker/generation, effective policy, next candidate, latest handoff, and
recovery state, and refreshes from the token-protected SSE stream. The badge is
not a fixed overlay and is removed/reinserted with the native task composer.

### Routing Inspector and truthful attribution

The 0.4.1 task badge is expandable. Its compact state distinguishes the worker
actually running a turn, the last completed worker, the committed owner and the
policy-aware next candidate preview. The expanded inspector shows fixed
selected/skipped reasons, scheduler score components, quota freshness,
reservation state, handoff source/target/generations, and a bounded recent
timeline. Native `<details>/<summary>` supplies keyboard operation; long labels
truncate visually while remaining present as text and accessible names.

The pool summary is explicitly routing capacity. Five full workers may display
`500% / 500%`, but each request still uses exactly one isolated subscription;
billing balances and reset credits are never merged. The pool timestamp is the
oldest confirmed worker snapshot contributing to the displayed sum, so one
fresh worker cannot hide another stale value.

Quota attribution never uses dispatch count as proof of consumption. Relay
captures the effective remaining value before a turn and reads a newer
upstream snapshot after completion. Only a measurable decrease sets
`lastQuotaConsumingAccountId` and `confirmed`; an unchanged snapshot says
`refreshed_no_measurable_change`, a crossed reset says
`refresh_crossed_reset_window`, and missing/stale data remains unavailable or
waiting. This work is asynchronous and does not delay the main message path.

The renderer shows one session-deduplicated toast for real quota failover,
handoff failure, recovery-required, all-depleted or compatibility downgrade.
Balanced/Rotate maintenance handoffs remain in the timeline without producing
default toast noise. Reloading the renderer does not replay the persisted
timeline as notifications.

## Persisted layout

For a task whose safe directory name is `<thread-id>`:

```text
<state-root>/
  state.json
  state.json.backup
  state.json.v1.backup          # only after a v1 migration
  relay-memory/
    sessions/.../<rollout>.jsonl
    archived_sessions/.../<rollout>.jsonl
  threads/<thread-id>/
    route.json
    goal-checkpoint.json
    turn-ledger.jsonl
    routing-decisions.jsonl
    migrations/<handoff-id>.json
    generations/<generation>/checkpoint.json
```

Goal fields not exposed by the app-server remain JSON `null`; Relay does not
invent steps, workspace revision, or continuation truth. The supported
objective/status/token-budget transfer occurs directly between isolated
app-server workers and is absent from routing diagnostics. The authoritative checkpoint stores
history generation, SHA-256, size, and a private local reference. Public route
APIs blank that reference.

## Protocol boundary and limitations

- `threadId`, `thread_id`, nested `thread.id`, nested `turn.threadId`, and
  reviewed `conversationId` shapes are recognized.
- Active approvals, tool callbacks, hooks, interrupts, and cancellations remain
  bound to their creating worker. An unscoped active method is routed only when
  exactly one active worker can be identified; global/config notifications use
  the Controller.
- One long-running turn is never divided among accounts. Automatic retry is
  allowed only for a quota rejection before visible output/side effects. A busy
  selected model retries the same model on the same worker.
- Canonical materialization is append-incremental only after prefix
  verification; otherwise it rebuilds the one rollout. Files over 1 GiB,
  symlink/junction escapes, and paths outside managed `sessions` roots fail
  closed.
- Automated validation uses fake app-server workers and temporary homes. See
  [`TEST-MATRIX.md`](TEST-MATRIX.md). Installed-app E2E is a separate,
  permission-gated procedure in [`SMOKE-TEST.md`](SMOKE-TEST.md).

## Tóm tắt tiếng Việt

Relay Memory là nguồn lịch sử task chuẩn; giao diện xem quota như một pool cộng
dồn (`5` tài khoản đầy đủ = `500% / 500%`, ví dụ thực tế có thể là
`155% / 500%`). Đây là pool định tuyến của Relay, không phải gộp token hay gói
thanh toán: từng tài khoản vẫn là một worker quota
có `CODEX_HOME`, `CODEX_SQLITE_HOME` và credential riêng. Mỗi lượt chỉ chạy trên
một worker. Balanced chia lượt theo quota còn lại, Sticky giữ worker và Rotate
đổi worker sau mỗi lượt hoàn tất. Router chỉ đổi owner sau khi history đã được
copy/append nguyên tử, hash/size đúng, `thread/resume` thành công và
`thread/read` trả đúng thread ID.

Nếu quota lỗi trước output hoặc tác dụng phụ, Router có thể thử một worker khác
một lần. Nếu command, tool, hook, approval hoặc sửa file đã bắt đầu, Relay không
chạy lại mù quáng mà chuyển task sang `recovery required`. Build không nhận
diện profile sẽ hạ policy hiệu lực về Sticky và không handoff xuyên tài khoản.
