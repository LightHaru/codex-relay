# Architecture

The independently built desktop uses bundle identifier `app.cdxmux.multi`; its
Computer Use helper uses `com.cdxmux.sky.CUAService`. Neither identifier is used
by the official ChatGPT installation. These identifiers and the `.codex-mux`
state directory remain stable across the product rename so existing macOS
privacy grants, connected accounts, and sticky thread ownership continue to
work.

Codex Relay replaces the copied app's bundled `codex` executable
with a small Go multiplexer and keeps the original binary beside it as
`codex.real`.

## Request routing

The desktop app opens one JSON-RPC app-server connection to the multiplexer.
The multiplexer starts one real app-server child for every enabled account,
each with its own `CODEX_HOME` and `CODEX_SQLITE_HOME`.

New threads use a strict round-robin selector across every enabled, connected
ChatGPT subscription with capacity in every reported quota window. Quota
percentage only decides whether an account may enter the pool; it is not a
tie-breaker that pins every chat to the lowest-usage account. An in-memory
dispatch counter gives each capacity-bearing account the next turn, while
accounts with temporarily unknown quota data remain last-resort candidates.
The controller/Primary owns shared Relay configuration but is not a routing
lock for new chats. On Windows, it is never used as a bridge to the Store app's
native home. Once a thread ID is known, `state.json` persists its owner.
Requests, responses, approvals, and notifications are rewritten only as needed
to preserve one coherent desktop session. The initial `initialize` handshake is
sent to all children concurrently so one disconnected account cannot block the
whole desktop connection.

If the owner is depleted, the multiplexer resumes the rollout on an account
with capacity and updates ownership. Threads do not migrate for ordinary load
balancing. A legacy/unassigned thread that is already in a Relay-owned home is
located from that home before the same failover path is applied. If a known
thread still points at the former native Store `sessions` directory, Relay may
read and copy that single rollout into the selected Relay home before resume;
it never reads native credentials/configuration, starts a child there, or edits
the source file. Store-only history is not scanned or imported in bulk.

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
