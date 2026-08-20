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

New threads use a fair-share selector across every enabled, connected ChatGPT
subscription with capacity in every reported quota window. The primary score is
the higher used percentage among the reported short/long windows, so the least
pressured account is chosen first. A small in-memory dispatch penalty breaks
comparable scores and alternates equal quota instead of permanently selecting
the first account in state order. The controller/Primary still owns the shared
configuration and is the safe initial source for an old unassigned chat; it is
not a routing lock for new chats. Once a thread ID is known, `state.json`
persists its owner. Requests, responses, approvals, and notifications are
rewritten only as needed to preserve one coherent desktop session.

If the owner is depleted, the multiplexer resumes the rollout on an account
with capacity and updates ownership. Threads do not migrate for ordinary load
balancing. A legacy/unassigned thread starts from the controller so its history
can be read, then takes that same failover path if the controller is depleted.

For the distinct upstream error `Selected model is at capacity`, the mux keeps
the exact original `turn/start` request (including `model`) and retries it on
the same account at most three times with short exponential backoff. This is
intentionally separate from quota failover: a busy model must not silently
change the selected model or consume a different subscription.

## Account isolation

The Primary account uses `~/.codex`. Added accounts use
`~/.codex-mux/accounts/<id>/codex-home`. Managed configuration is copied from
the Primary account, excluding credential-store settings and project trust.
Each isolated account forces file-backed CLI and MCP OAuth credentials.

## Desktop integration

The patcher extracts `app.asar`, verifies exact upstream anchors, inserts the
account UI, disables the copied app's native self-update, and repacks the
archive with an updated integrity hash. On Windows, its version-pinned renderer
rewrite sends the native single-account Settings → Usage request through
`CodexMuxWindows.usageStatus()` and injects a version-neutral **All connected
subscriptions** panel. The panel calls the token-protected `/v1/usage/all`
route, which fetches one native Usage payload with each isolated account
credential and keeps partial failures per account; native billing actions remain
account scoped. OAuth tokens stay outside the renderer. Windows also injects a
small, version-neutral update bridge. It checks the Router's source-only
GitHub release manifest, then hands a hash-verified archive to an updater
executable stored outside the managed app so the app can quit and restart
safely. The official Store package is never replaced. The app receives a
separate Chromium profile and URL scheme.

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
login/logout actions, a narrow pending-login cancellation action, and an
authenticated SSE event stream; it never returns OAuth tokens. Browser sign-in
is initiated by the official child app-server, which owns the localhost
callback and writes credentials only in that subscription's isolated Codex
home.
