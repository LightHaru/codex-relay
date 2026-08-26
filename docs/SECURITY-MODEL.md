# Security model

## Trust boundaries

- The official Codex/ChatGPT installation is build input and remains untouched.
- Codex Relay owns only its copied app, its loopback gateway and its isolated
  state root.
- The one task authority receives the public task protocol.
- Each credential-source child receives only its own isolated `codex-home` and
  is used for login, account management and quota observation.
- The control and model APIs bind to loopback; remote origins and other users
  are outside the trust boundary.

## Credential isolation

Every source keeps its own `auth.json`, profile and SQLite home. The transport
reads only the selected source file long enough to construct the upstream
request; it strips incoming credential headers and never forwards cookies,
refresh tokens or the local control token upstream. The task authority home has
no source credentials. Login is performed by the official source app-server;
Relay never asks for a password or performs a browser OAuth exchange itself.

The state store contains account paths/labels, membership state, quota evidence,
pool leases, task generations, hashes, health and bounded fixed-reason ledgers.
It does not contain OAuth tokens. State, config, token and canonical-history
files use private Windows ACLs/Unix modes where supported.

## Public versus private data

Contract-v2 `/v1/router/status` and `/v1/thread-route` are intentionally
sanitized to one identity (`relay`, `Codex Relay Pool`) plus aggregate pool and
recovery state. They do not return account IDs, email, worker lists, candidate
choices, handoff source/target, raw error text or full rollout paths. Detailed
source metadata is available only to token-protected account-management calls
used by the local renderer.

Routing diagnostics store fixed reason codes and request SHA-256 fingerprints,
not prompts, model output, file contents, tool arguments, Goal objectives,
workspace paths or arbitrary upstream payloads. SSE notifications are pool-level
and are safe to display in the task UI.

## History and replay safety

Canonical Relay Memory is sensitive conversation data and stays under the Relay
state root. Materialization accepts only regular JSONL under managed
`sessions`/`archived_sessions`, rejects traversal and symlink/reparse escapes,
caps size, verifies stable bytes and SHA-256, fsyncs a sibling temporary file,
and installs atomically. On Windows access denied, an immutable sibling
generation is used; a locked old rollout is never overwritten.

Only pre-output, pre-side-effect quota rejection can be retried. After visible
output or any command, approval, hook, file mutation or tool side effect, the
lease becomes `recovery-required`; source depletion is recorded for future
turns and the request is never replayed. A generic timeout/network error is not
treated as quota evidence. Partial-stream seamless continuation is not claimed
without a real upstream continuation primitive and proof.

## Pool state integrity

`PoolState.Revision` and `CredentialSourceState.Revision` are CAS-protected.
One source transition is committed for concurrent duplicate quota events.
`PoolLease` heartbeats prevent an interactive turn from expiring silently;
startup converts expired uncertain leases to recovery-required. A continuous
v2 rollback projection has no active reservations and marks active tasks for
review. Its manifest records the projection hash and source pool revision.

The only public pool capacity is normalized routing headroom. It is not an
OpenAI billing balance, and upstream accounting remains per real credential.

## Network and update security

Control routes require the per-install random token and use loopback CORS/PNA
allowlists. The model gateway has its own random bearer token. Request and
response sizes are bounded; errors are sanitized. The updater accepts only the
documented GitHub release host, validates archive paths and SHA-256, and runs
outside the app directory so it can replace Relay safely. It does not send
credentials, patch the official app, or terminate arbitrary processes.

Releases contain source and public manifests only; never attach an official
ASAR, OpenAI executable, certificate, token, `auth.json`, or user history.
