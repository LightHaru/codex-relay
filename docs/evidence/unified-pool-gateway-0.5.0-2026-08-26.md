# Unified Pool Gateway 0.5.0 evidence — 2026-08-26

This is a sanitized working-tree and installed-build report. It separates
deterministic proof, installed app-server proof, read-only live quota evidence,
and evidence that is still missing. It contains no credentials, emails,
account IDs, prompts, model output, or full history paths.

| Item | Value |
| --- | --- |
| Version | `0.5.0` (installed and working tree) |
| Source commit | `ce57d37` (`main`, implementation commit) |
| Pool schema | `3` |
| Public contract | Router/route contract `2` in unified mode |
| Installed source | Microsoft Store Codex `26.818.8289.0` |
| Official ASAR hash | `e2f04d6aa921d07981b42368df0a28a8bebe8cd21375d4a1f9286757b51c1313` |

## Deterministic evidence — PASS

- `go test ./...` and `go vet ./...` completed successfully through
  `npm run check`.
- State v3 migration, CAS, concurrent quota transition, heartbeat, crash
  recovery, rollback projection hash, sticky selection and TaskRecord lease
  persistence passed, including Goal ID/session/thread continuity across a
  credential failover.
- Pool projection metrics are recomputed on every mutation and now persist
  `maximumHeadroom`, `confirmedHeadroom`, `unknownHeadroom` and `health`; a
  fresh healthy four-source fixture reports 400% maximum and 400% confirmed
  headroom, while unknown evidence reports a warming pool instead of stale
  zeroes.
- Legacy and cross-source chat resume validates both source and target history
  boundaries; a Windows sharing violation now installs a verified sibling
  rollout generation instead of failing the logical turn with `Access is
  denied`.
- A lease explicitly excludes the currently active source and selects another
  eligible source; concurrent quota rejection still commits one authoritative
  pool transition.
- Gateway kept one source for 20 turns, retried the exact request body before
  output, retried an early-stream quota rejection, and refused late-stream
  replay. Late quota advances the pool for future turns while retaining the
  failed lease in `recovery-required`.
- Unified inbound handling recognizes the sanitized
  `relay_pool_recovery_required` marker and prevents a failed turn from being
  incorrectly finalized as completed.
- The selected Relay controller is the only public task authority. If that
  controller is disabled, unified mode fails closed instead of silently falling
  back to the Relay host or an account imported from the official Codex app.
- `/v1/usage` stays scoped to the selected Relay authority; `/v1/usage/all`
  provides the all-account billing view used only inside the native Usage &
  billing content page.
- Account snapshot errors, pool HTTP errors and protocol errors are bounded to
  an actionable reason/code. If app-server wraps a pool HTTP 429 as JSON-RPC
  `-32600`, the renderer receives the recent pool cause, HTTP status and Relay
  code in its event/toast; it no longer relies on a lone exclamation mark.
- Unified startup management errors are no longer projected as public
  `router-error` toasts: account, app, plugin, and MCP response failures stay
  on their native settings request, while task/turn failures remain visible.
  Regression tests cover both routed management responses and unscoped
  initialization notifications.
- Unknown compatibility profiles keep the single Relay API but disable
  credential failover and fail closed with a sanitized pool error.
- An upstream HTTP 401 quarantines only the affected credential source and
  retries the same pre-output lease on the next source; it is not classified as
  quota depletion.
- All-depleted requests end without a fake completed turn or a live lease.
- Public unified status/route tests found no source identity, worker list,
  candidate, handoff target, raw error or full rollout path.
- JavaScript UI suite passed `32/32`.
- `npm run check` completed successfully: Go tests/vet, JavaScript tests,
  Python checks (with the documented `python3` → `python` fallback), Windows
  patcher tests (`27` pass, `1` skip) and shell syntax.
- `npm run release:check` reported `v0.5.0 metadata is consistent`; `git diff
  --check` passed.
- `go test -race` was attempted previously but cannot start in this Windows
  environment because the installed Go toolchain has CGO disabled. CI with
  CGO enabled remains a release gate; no local race pass is claimed.

## Installed app-server E2E — PASS (fake upstream)

Command:

```powershell
$env:CODEX_RELAY_REAL_E2E = '<installed codex.real.exe>'
go test ./internal/mux -run TestUnifiedGatewayUsesOneTaskAuthorityAndFailsOverInsideRequest -count=1 -v
```

Final exit code after the latest install: `0`. A stability run before the last
install also completed `2/2` with exit code `0`.

The test launched the installed `codex.real.exe` in temporary isolated homes,
used four fake credential sources and a fake Responses upstream, and ran 20
logical turns (23 upstream attempts). It observed one task authority with
sticky source order A→B→B→B→C→C→C→D and then D for the remaining turns. Each
pre-output failover pair carried identical request bytes; depleted sources
were never reused; thread ownership stayed on the authority and the pool
recorded three transitions. The upstream was fake, so this is not live quota
evidence.

The installed protocol probe also exited `0`: one app-server, one thread, one
streaming `POST /v1/responses`, bearer authentication present, and no user
credential read from the temporary probe home. This confirms the reviewed
custom-provider contract for the installed profile; it does not prove live
quota failover.

## Installed Windows and identity isolation — PASS

- The installer built and launched the independent app from the working tree,
  moved the prior Relay copy to the state-owned backup area, recreated the
  `Codex Relay.lnk` shortcut, and preserved the pool ledger/source homes.
- The installed manifest is `0.5.0` with the reviewed Store hash above and the
  `windows-reviewed-*` compatibility profile.
- The live control API reported one selected Relay authority, four connected
  quota sources, pool `healthy`, 400% maximum, 330% confirmed remaining, 70%
  confirmed used, four known, zero unknown, four
  available, zero depleted, and zero active leases at the final observation
  point.
- The selected `/v1/usage` payload carried the Aira authority identity, while
  `/v1/usage/all` returned four account entries with zero collection errors.
- Every Relay account config points `CODEX_HOME` at its Relay-owned home. The
  native `%USERPROFILE%\\.codex\\config.toml` remains unchanged and still
  points at the official Codex home.
- Process inspection found both the independent Relay `ChatGPT.exe` processes
  and the pre-existing Microsoft Store `ChatGPT.exe` processes. The installer
  process scope only matched the Relay root; no official process was closed or
  modified.

The installed E2E output contained only upstream/plugin startup warnings from
fake credentials (featured-plugin HTTP 401, PowerShell shell snapshot support,
and Windows long plugin paths). They did not fail the Relay test or alter the
official app.

## Authorized real-account smoke — PASS (normal turn only)

Four short, harmless turns were sent through the installed Relay app-server and
Gateway, one per currently configured isolated source home. Each run returned
`turnAccepted=true`, `turn/completed`, no terminal error, and exit code `0`.
The temporary homes were removed after the run. These are real provider turns,
so they consumed a small amount of the corresponding subscriptions' quota.
They prove credential validity and the normal Relay path for all four sources;
they do not prove failover because no source returned a live 429/usage-limit
rejection.

## Read-only live-account evidence — PASS for visibility; failover LIVE PENDING

The permission-gated read-only quota probe reached all four currently
configured Relay source homes and exited `0`; every source returned a valid
quota response with `rateLimitReachedType: null`. The final observed primary
used percentages were `36%`, `0%`, `0%`, `0%`; secondary used percentages were
`6%`, `16%`, `16%`, `2%`. This proves quota visibility and source isolation,
not a quota rejection transition.

No real-account A→B, B→C or C→D quota rejection was induced in this run. No
live long-session chat, existing-chat resume, archived-chat continuation or
Goal continuation is marked `PASS`. The next authorized run must use a
genuinely depleted source first, then record each structured rejection,
pool/lease revision, canonical hash/size, same thread/session/Goal identity,
duplicate/lost-output assertions and the final exit code.

## Streaming quota regression — PASS

The working-tree patch reproduces the provider shape that caused the reported
"quota còn 200% nhưng request lỗi" symptom: HTTP 200 followed by
`response.failed` whose only error text is `You've hit your usage limit`.
The gateway now classifies that event before output, retries the identical
request in the same unified pool, marks only the rejected source depleted and
clears the transient error after the fallback completes. Terminal errors are
persisted as bounded `pool.lastError` data and are rendered inside the native
Usage & billing content column.

Validation completed after reinstalling the independent Windows Relay:

- `go test ./internal/state ./internal/gateway ./internal/mux -count=1` — PASS.
- Native `-32600`/retry-limit error projection unit test — PASS; the recent
  bounded pool cause is included in the Relay event.
- `node --test ui/test-windows-router-menu.cjs` — 29/29 PASS; full JavaScript
  suite — 32/32 PASS.
- Installed `codex.real.exe` unified-pool E2E — PASS, one task authority,
  message-only A→B failover plus B→C→D quota fixture, 20 logical turns and
  identical pre-output request bodies.
- Read-only quota probe — PASS for all four configured source homes; no source
  reported `rateLimitReachedType`.
- Final live Relay status — `healthy`, 4 connected, 400% maximum, 330%
  confirmed remaining, 4 known, 0 unknown, 4 available, 0 depleted and 0
  active leases.

Read-only probe (does not print credentials or raw provider JSON):

```powershell
python scripts/probe_live_rate_limits.py --executable '<installed codex.real.exe>' `
  --source-dir '<source A codex-home>' --source-dir '<source B codex-home>' `
  --confirm-read-only-quota
```

## Protocol limitation — NOT PROVEN

Seamless continuation after a stream has already emitted visible output or a
side effect is not claimed. Relay intentionally marks that lease
`recovery-required` and never replays it. A future release may change this only
after the upstream provides a continuation primitive and a dedicated E2E proves
no duplicate output or side effect.

## Release/publish status

Source commit `fce9fce` is pushed to `origin/main`. A GitHub release
publication is not implied by this evidence file; the release archive must be
built from the pushed commit and its manifest/hash must be regenerated.
