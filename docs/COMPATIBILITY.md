# Compatibility

## Release 0.5.9 — Transactional pool responses

The production Relay Gateway treats one complete model response as the commit
boundary. It buffers semantic SSE frames until `response.completed`, while a
standards-shaped, deliberately unknown `relay.keepalive` SSE event keeps the
native connection open. Before
that boundary, quota and transport failures can be retried through another
eligible credential without exposing partial assistant text or function calls.

This deliberately trades token-by-token rendering for atomic continuation and
exactly-once publication. Codex still receives the native Responses protocol;
the Relay API, task authority, history and tool loop remain singular. Legacy
`RECOVERY_REQUIRED` markers are removed during the first v0.5.9 startup.

The same Gateway now accepts the native `/v1/responses/compact` route and
forwards the opaque compact request and response without interpreting or
rewriting either payload. It retains the native thread/session headers, can
rotate a depleted credential before publishing the compact result, and never
creates a task-restart marker. Native shell-call response items are also
forwarded with their complete command string intact.

## Release 0.5.8 — Transparent post-output streaming

Production Relay no longer owns an idle deadline after a Responses stream has
started. It forwards `relay.keepalive` SSE events while waiting for the
upstream terminal event, transport close, or real downstream cancellation.
The configurable idle cutoff remains available only to deterministic tests.

When a compatible upstream cleanly closes after sending a complete
`response.output_item.done` frame, Relay can safely emit the omitted
`response.completed`: the output boundary is already delivered and no request
or tool is replayed. A partial subsequent frame never takes this path and
continues to fail closed.

## Release 0.5.7 — Long post-output pauses and native activity details

The public Responses and app-server protocols remain unchanged. Relay sends
`relay.keepalive` SSE events while allowing up to 90 seconds for the next upstream
event after output begins, instead of classifying an ordinary three-second
reasoning or tool pause as a failed stream. A distinct new logical turn is the
explicit continuation boundary that clears older recovery leases for that
thread; the previous turn is still never replayed.

Historical activity compatibility can be checked without exposing content:

```powershell
python scripts/probe_thread_activity.py --executable <codex.real.exe> `
  --codex-home <managed-codex-home> --thread-id <thread-id>
```

The probe accepts the native `commandExecution.command` shape and the current
Codex `mcpToolCall` `js` shape when every activity carries a non-generic title.
It reports only counts, field names and a boolean health verdict.

## Release 0.5.6 — Native request-ID reuse compatibility

Current Codex app-server builds can reuse one `X-Client-Request-Id` across
several distinct turns in a thread. Relay now derives its private idempotency
key from that client ID plus an opaque request-body fingerprint. Exact retries
still join one flight, while later chat and Goal turns always receive a fresh
pool dispatch. The public Responses request and SSE formats are unchanged, and
the request body is neither persisted nor logged by this keying step.

## Release 0.5.5 — Dynamic model-catalog Gateway compatibility

Codex app-server 0.150.0 refreshes its catalog from `GET /v1/models` on the
configured provider base URL. Relay now proxies that control-plane request to
the native ChatGPT models endpoint with isolated source credentials, validates
the required top-level `models` array and caches the accepted response. Model
discovery never creates a quota lease, never advances the pool scheduler and
never exposes source credentials. Older app-server builds continue to use the
same `/v1/responses` contract.

## Release 0.5.4 — Restart-safe leases and native terminal recovery

This patch keeps the public Responses provider contract unchanged while
correcting two native app-server edge cases. Startup reclaims stale
pre-commit leases before listening, and the SSE classifier distinguishes the
terminal `response.completed` event from the nested completed item carried by
`response.output_item.done`. When a provider pauses or closes after output
without a terminal event, Relay emits a recovery terminal within a bounded
grace window. The unified mux preserves the failed-turn state but removes the
misleading native `stream disconnected before completion` prefix from the
operator-facing message.

## Release 0.5.3 — Restart-safe leases and terminal-aware Gateway streams

The public Responses provider contract is unchanged. Relay now recovers all
persisted leases before the loopback listener accepts work: a pre-commit lease
is released for same-request replay, while a post-commit lease remains
recovery-required. Concurrent duplicate request IDs join one in-process flight.
The SSE transport requires an explicit terminal event and may rotate hidden
credentials for classified pre-commit connection/HTTP/stream failures. Older
renderers continue to use the same local `/v1/responses` shape; app-server
request and stream retries remain disabled so only the Gateway owns replay.

## Release 0.5.2 — Chunked SSE header compatibility

The current Windows ChatGPT Responses endpoint can return a chunked SSE body
without a `Content-Type` header. Relay sniffs only a small body prefix, restores
it before forwarding, and continues to enforce the same one-authority,
same-request quota failover rules. The hidden credential scheduler uses a
persistent fair-share cursor across confirmed sources, while the public API,
task authority and thread remain unchanged. The body is never buffered as a
full model response just to identify the wire format.

## Release 0.5.1 — Unified Pool Gateway quota-shape fix

The gateway accepts the quota response forms used by the current Windows
Codex app: structured HTTP errors, failed JSON envelopes, and streaming
`response.failed` messages such as “You're out of Codex messages”. These forms
are treated identically and can advance the hidden source inside one logical
request. A normal successful response or a model context error is never routed
as subscription quota exhaustion.

## Release 0.5.0 — Unified Pool Gateway

The Router core is version-neutral only after the installed app-server accepts
the reviewed Responses custom-provider contract. The authority must preserve
the request headers needed by Relay (`Session-Id`, `Thread-Id` and a stable
client request ID) and must stream standard `response.*` SSE events. The local
probe is:

```powershell
python scripts/probe_unified_provider.py --executable <path-to-codex.real.exe>
```

The probe must show one app-server, one thread, one Responses request, streaming
enabled, and no source credential read by the authority. It is a protocol probe,
not quota evidence.

## Renderer profiles

Windows renderer patching remains exact-anchor and hash/profile based. The
reviewed Store profiles from earlier releases are retained in
`scripts/patch_windows.py` and their fixtures. A new or unknown `app.asar`
hash must stop before a partial patch. Structural discovery may be used only in
the explicitly documented test mode and still requires every semantic anchor
exactly once.

The current reviewed Windows sources are Microsoft Store `26.825.3734.0`, with
`app.asar` SHA-256 `c32dcc8424e50be2b5a22c80c196db5c8c71562fc13dc7b7e3b749ebb4806284`,
and `26.825.6671.0`, with `app.asar` SHA-256
`86e791e0eb330a1507057d30e450878f7c958e56e04e718f101ba80549e9baf2`.
The latter ships Codex CLI `0.151.0-alpha.7.2`. Both exact renderer profiles
are pinned under `scripts/windows_profiles/`; the installer does not need the
untested-source override for either build. The 26.825.6671 profile was
admitted only after exact-once structural discovery, the native provider
probe, installed real-binary failover tests, and authorized live compact and
command-visibility probes passed.

The Unified Pool change is inserted through version-neutral Relay bridges, but
the native Settings shell is still a compatibility surface. Every profile must
prove that:

- Usage & billing remains a child page with its original sidebar;
- the pool summary is in the content column only;
- other Settings pages remain reachable;
- no fixed overlay covers the sidebar or shell;
- account-management and login callbacks still use isolated source homes.

## App-server behavior matrix

| Capability | Required behavior | Fail-closed result |
| --- | --- | --- |
| Responses custom provider | `wire_api=responses`, local base URL, chunked SSE (with or without `Content-Type`) | Keep one Relay API in safe mode; disable credential failover |
| Dynamic model catalog | Authenticated `GET /v1/models` returns a native top-level `models` array without a quota lease | Sanitized `models_refresh_failed`; retain a previously validated in-process catalog |
| Stable thread/session headers | IDs are forwarded to one gateway lease | Reject request without retry |
| Structured or message-only quota rejection | HTTP/SSE quota code or a `response.failed` message such as `usage limit`/`rate limit`/`out of Codex messages` | Mark source depleted and retry pre-output |
| Generic timeout/network/HTTP 5xx before commit | Not quota evidence; bounded retry through another eligible credential | Return one sanitized retry-budget error after eligible sources fail |
| EOF without a Responses terminal event | Incomplete stream; retry only before output/side effects | Recovery-required after commit; never replay |
| Duplicate logical request ID | Join the same in-process flight and bounded replay | Never dispatch a second upstream attempt concurrently |
| Process restart with persisted lease | Release pre-commit lease before listen; preserve committed recovery | Recovery-required after commit; never replay |
| Partial stream continuation | Must be proven by upstream primitive | Recovery-required; never replay |
| Thread/history resume | Authority home and checkpoint path verify | Recovery-required; no source move |
| Unknown profile | No verified lifecycle/path contract | Keep one Relay identity, disable source failover and fail closed |

Old app-server builds can continue management/login if their source APIs remain
compatible, but they are not automatically accepted as task authorities. A
profile is release-compatible only after the protocol probe, renderer fixtures,
state migration tests and local E2E pass.

## Upgrade policy

When official Codex updates, do not copy its credentials or overwrite its home.
Run the profile and probe gates against a staged Relay copy. If an anchor or
Responses header changes, publish a new reviewed profile. The user-facing
updater may install a release only after its manifest and SHA-256 pass; it does
not bypass compatibility checks.

## Evidence status

The repository contains deterministic state/gateway tests and a real installed
`codex.real.exe` test with a fake upstream. Those prove the one-authority and
same-request A→B→C→D mechanics. Authorized real-account smoke turns separately
prove credential validity and normal routing, but must not be described as
failover evidence. A real-account transition is `LIVE PASS` only when each
provider rejection and subsequent source is observed without token/PII
disclosure; otherwise use `LIVE PENDING`.
