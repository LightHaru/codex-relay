# Unified Pool Gateway v3

This document is the implementation contract for the state and transport
layers. It supersedes the older fair-share/worker-routing description.

## Public invariant

```text
one Codex client
       ↓
one Relay API + one Relay identity
       ↓
one logical task authority
       ↓
one PoolQuotaLedger with hidden credential sources
```

The pool is a routing capacity abstraction, not a merged OpenAI account. A
source's upstream usage remains attributable to that real credential inside
private evidence only.

## State v3

`PoolState` contains a stable pool ID, monotonic revision, membership/quota
epochs, current source, source order and private `CredentialSourceState` values.
It also contains active `PoolLease` values, normalized confirmed/unknown/max
headroom, reset metadata, health, failover count and the last transition.

`TaskRecord` is account-neutral: thread/session/task identity, canonical
generation/checkpoint, Goal ID when supplied, active lease and recovery state.
It never stores OAuth material, prompt text, model output or arbitrary error
payloads.

## Sticky source algorithm

1. On the first request, use the first eligible source in deterministic pool
   order (or the persisted active source).
2. Reuse it for every later request while it remains eligible.
3. Accept only explicit quota/rate-limit evidence as depletion: structured
   rejection, `quota_exhausted`, `usage_limit`, `limit_reached=true`,
   `allowed=false`, a 100%-used reported window or an explicit credential
   rejection.
4. Atomically mark the source depleted, increment the pool revision and choose
   the next eligible source.
5. Retry the exact body on that source only if no output or side effect exists.
6. Repeat until a source succeeds or the pool is empty.

Unknown quota is `PROBATION`, not confirmed capacity. A timeout, generic network
error or model-capacity error never advances the quota source.

## Lease lifecycle

```text
PREPARED → BOUND → DISPATCHED → ACCEPTED → STREAMING → COMPLETED
                                      └──────────────→ RECOVERY_REQUIRED
```

The lease carries pool/session/thread/turn IDs, source revisions, heartbeat,
expiry, failover/retry counts and excluded sources. Duplicate notifications
are idempotent. Concurrent rejections commit one source transition; other
leases rebind to the already selected next source.

After visible output or a side effect, quota rejection leaves the failed lease
bound to its original source in `RECOVERY_REQUIRED` while the pool advances for
future turns. No replay, new thread, new Goal or public handoff is permitted.
An all-depleted pre-output request is aborted without a fake completion and
without a live lease; its TaskRecord remains resumable after reset/addition.

## Canonical memory

The single authority home is the writable task home. Canonical Relay Memory is
updated only after a completed turn and contains verified JSONL, hashes, sizes,
route and Goal checkpoint metadata. Materialization checks managed
`sessions`/`archived_sessions` containment, regular-file type, reparse/symlink
safety, stable size/mtime, SHA-256 and atomic installation. A Windows locked
destination gets an immutable sibling generation; the old file is untouched.

## Protocol boundary

The authority app-server is configured with a loopback Responses provider. The
Gateway strips incoming auth, reads only the selected source auth file, builds
the upstream credential headers and forwards standard SSE. Session/thread and
client request IDs remain stable across a pre-output retry. Source identifiers
are never required in the public request or returned in the v2 status/route
projection.

## Recovery and rollback

Heartbeats extend long turns. On restart, expired leases and active TaskRecords
are marked recovery-required; Relay does not guess that a tool or file change
was safe to replay. State v3 writes a conservative `state.json.v2.rollback` and
SHA-256 manifest on every commit. The projection has no reservations, maps
threads to the stable authority and marks uncertain tasks for review.

## Required proof

Deterministic tests must prove sticky selection, exact-body retry, atomic/CAS
transitions, crash recovery, no replay, migration/rollback and sanitization.
The installed-app E2E must prove one task authority and A→B→C→D mechanics with
a fake upstream. Only an authorized live-account run can mark actual quota
transitions `LIVE PASS`; partial-stream seamless continuation remains
`NOT PROVEN` unless the upstream protocol provides a tested primitive.
