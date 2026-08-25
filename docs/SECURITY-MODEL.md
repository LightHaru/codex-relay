# Security model

## Trust boundaries

- The official ChatGPT app is trusted build input and remains unchanged.
- The patcher has local filesystem and code-signing access by design.
- Each real Codex child is trusted with only its assigned account home.
- The injected renderer is trusted with the loopback control token.
- Other local users and remote origins are outside the control API boundary.
- Processes running as the same macOS user are not considered isolated from
  one another; they can already read that user's app data subject to macOS
  permissions.

## Credentials

OAuth material stays in `auth.json` under each account's Codex home. The
multiplexer reads an account token only to call the same authenticated ChatGPT
profile and rate-limit-reset endpoints used by the desktop experience. It does
not log or return tokens. Windows browser sign-in is initiated by the official
Codex child app-server; the Router receives only the official authorization URL
and login identifier needed to cancel an unfinished flow. It never collects a
password or performs its own OAuth token exchange. State persisted by the mux
contains account paths/labels, enabled state, thread routes and generations,
canonical rollout paths and hashes, scheduler deficits/reservations, account
health, capability observations, turn-attempt and handoff journals, and a
bounded routing-decision ledger. It never contains an OAuth token. State v1 is
backed up before migration; later writes keep a last valid backup and replace
the primary state atomically.

Canonical Relay Memory contains conversation rollout JSONL and is therefore
sensitive user data. It stays under the private Router state root. Materializers
accept only regular `.jsonl` files beneath managed `sessions` or
`archived_sessions` roots, reject path traversal/symlink escapes, cap size,
verify prefix/hash, fsync a sibling temporary file, and replace atomically.

Automatic retry fails closed once a command, approval, hook, file mutation, or
tool side effect has begun. Unknown capability profiles never opt into
incomplete-turn resume; the task is marked `recovery required` instead.

Canonical turn diagnostics store `SHA-256(method || NUL || params)` as the
request fingerprint; raw request parameters, prompts, and workspace paths are
not copied into `turn-ledger.jsonl`. Persisted failure messages are mapped to a
small fixed classification such as `quota_exhausted`, `history_not_found`, or
`resume_failed`. The thread-route API redacts the checkpoint's local rollout
path, and SSE route reasons are fixed Router strings rather than arbitrary
upstream payloads.

Goal objective/status/token-budget state is read from the source app-server and
restored directly on the target during a handoff. The objective remains an
in-memory transfer value: it is not written to Router state, decisions,
handoff journals, SSE events, or control API responses. For a budgeted Goal,
Relay subtracts source `tokensUsed` before setting the target budget so moving
workers cannot increase the remaining token allowance.

The state root is mode `0700`; state, config, and control-token files are mode
`0600`. Existing control tokens are validated as 256-bit hexadecimal values and
their permissions are repaired on startup.

Plugin and MCP configuration is deliberately synchronized from the Primary
account so installed definitions remain consistent. Inline environment values
inside those definitions are therefore copied into every isolated account home
with mode `0600`; account isolation is not a separate secret boundary for
shared plugin configuration.

## Network

The control server binds to `127.0.0.1`. Private endpoints require the token
embedded into the independently built local renderer. Profile images must use
HTTPS. Response sizes and JSON request bodies are bounded.

The Windows Router checks one documented GitHub Releases manifest only when a
patched copy is running. The manifest is restricted to the project's GitHub
hosts, the downloaded source archive is checked against its SHA-256 value, and
the external updater validates the archive paths before invoking the local
installer. No credentials or control token are sent with the check. There is
no telemetry endpoint; traffic beyond loopback is otherwise performed by the
official Codex children or by the documented ChatGPT profile and rate-limit
APIs. Releases contain source only, never an OpenAI ASAR or executable.

## Signing and native access

The source app is copied into a temporary staging directory. Native modules,
the Computer Use helper, Node runtime, mux, and final app are signed under one
selected Apple team and verified before replacement. Official OpenAI
application-group and keychain entitlements are removed from modified callers.

The native helper's caller allowlist is patched to the selected team and the
independent desktop bundle ID. This is required for the helper's peer checks;
it does not bypass macOS Accessibility or Screen Recording consent.

## Diagnostics

`CODEX_MUX_UI_TESTS=1` enables deterministic preview and screenshot endpoints.
They are unavailable during a normal launch, bind only to loopback, and require
the same control token. Release workflows never set this variable.

The routing status and SSE endpoints are diagnostics, not credential APIs.
They may expose local account IDs, labels, masked identity data, quota
percentages, thread IDs, generations, health, and sanitized handoff state to
the trusted local renderer. They never expose access/refresh tokens, cookies,
passwords, `auth.json` contents, control-token values, prompt/file contents, or
full canonical rollout paths.

The explainability contract additionally clears full email/username fields
from `/v1/router/status`, blanks absolute checkpoint paths, and reclassifies
stored account-health, handoff and decision text before serialization. This
fail-closed projection also protects upgrades from an older or corrupt state
entry containing an arbitrary error string. It never returns a Goal objective,
workspace path, conversation/file contents, tool arguments or raw upstream
error. Quota attribution persists only account ID, effective percentage,
observation timestamp and a fixed status; it does not persist the upstream
Usage payload or any billing credential.

## Distribution

Releases contain source only. Publishing the patched `.app`, the official ASAR,
or any extracted OpenAI binary is outside this project's release process.
