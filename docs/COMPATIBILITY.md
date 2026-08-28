# Compatibility

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
