# Windows installation and operations

## Scope

The Windows build is an independently patched copy of the installed Codex app.
The official Store app is never replaced and can remain open beside Relay. Relay
uses a private Electron profile, a Relay-owned task-authority home and isolated
credential-source homes.

## First install

Prerequisites are Windows x64, Microsoft Store Codex/ChatGPT, Python 3, Go
1.26+, Node.js 22.12+ and npm. From a published release:

```powershell
irm https://github.com/LightHaru/codex-relay/releases/latest/download/install-codex-relay.ps1 | iex
```

For a checkout, run `git clone https://github.com/LightHaru/codex-relay.git`,
open it in Explorer and double-click `Install Codex Relay.cmd`. The script
validates the selected official bundle and stages a copy before enabling the
Relay wrapper. It does not silently install missing toolchains.

Important locations:

| Location | Role |
| --- | --- |
| `%LOCALAPPDATA%\\Codex Relay\\app` | Relay copy, `codex.real.exe`, mux |
| `%APPDATA%\\Codex Relay\\codex-home` | Private Relay host home; it is the authority only when that account is selected |
| `%USERPROFILE%\\.codex-mux` | State-v3 ledger, source homes, Relay Memory, backups |
| `%LOCALAPPDATA%\\Codex Relay Updater` | Replaceable updater outside app |
| Desktop `Codex Relay.lnk` | User-facing launch shortcut |

Relay never uses `%USERPROFILE%\\.codex` as a writable authority home. Existing
official files are not copied, deleted or logged in the migration.

## Add a source

Open the Relay account menu → **Add another subscription**. The normal browser
login is opened by the isolated source app-server. When callback/polling reports
success, the code dialog closes and the source appears as connected. A cancelled
or incomplete flow remains `LOGIN_PENDING` and can be discarded without touching
the official account.

The source home and `auth.json` are private to that source. The source is not a
public task worker. Removing a source is rejected while it is the authority or
owns an unresolved recovery task.

## Unified Pool behavior

The public task authority is the connected account selected as **Relay
authority** in Account settings. The private Relay host account remains a
separate source unless it is selected. Management children may exist for
login, account settings and quota observation, but ordinary thread, turn,
Goal, tool and approval messages go only to the one selected authority.
Changing the authority is allowed only while no turn is active and restarts
Relay-owned children; it never creates a second public worker or moves a chat.

The Gateway keeps one public API and task authority while its hidden credential
scheduler fair-shares requests across every confirmed eligible source. It reads
the short (5-hour) and long (weekly) windows, skips depleted sources and uses a
persistent cursor so one account cannot monopolise the pool. A pre-output A→B
retry reuses the exact request body, model, session, thread and logical turn. It
does not create a task or move a chat. If a stream already produced output or a
side effect, Relay marks recovery-required and never replays it. If all sources
are depleted, the error is one pool-level message.

## Usage & billing placement

The native Settings shell remains in charge. The Relay surface is inserted only
after the native Usage & billing heading, inside its content column. It is not a
sidebar item, a second Settings shell or a fixed overlay. General, Profile,
Plugins, Browser and every other child page remain reachable.

The page displays one **Codex Relay Pool** summary and one detailed card for each
connected source. Each card reads its isolated account's plan, credits, quota
windows, reset information and any bounded read error. Login/remove controls
remain in account management. Public task status contains no source ID, email,
worker name, candidate or handoff target.

## Build and test

```powershell
npm ci --ignore-scripts
npm run check
npm run release:check
git diff --check
```

Probe a staged authority with:

```powershell
python scripts/probe_unified_provider.py --executable <path-to-codex.real.exe>
```

The local app-server E2E uses a fake upstream and temporary fake credentials;
it proves one authority and same-request A→B→C→D mechanics. It does not prove
live account quota. Use [TEST-MATRIX.md](TEST-MATRIX.md) for the separately
authorized live-account procedure.

## In-app update

After a reviewed release publishes `windows-update.json`, the banner **Update
now** downloads only the source archive, checks its allowed host, archive paths
and SHA-256, and invokes the updater outside the app directory. The updater:

1. records the current Relay installation and state root;
2. waits for Relay-owned processes only;
3. stages and verifies the new copy;
4. repairs the `Codex Relay` shortcut;
5. starts the new Relay process and leaves pool state/history untouched.

It never kills a process merely because it is named `ChatGPT.exe`, and it never
changes the official Store app. A failed update leaves the previous copy and
state available for rollback.

## Troubleshooting

**“Unable to send message — Update Agent sandbox”**: this is the official
Windows sandbox setup, not proof of quota exhaustion. Restart the Relay
shortcut after its isolated wrapper is installed; do not edit `%USERPROFILE%\\.codex`.

**“Relay Pool has no usable quota source”**: inspect the pool summary and source
management dialog. A source must have a valid isolated login and fresh quota
evidence; unknown quota is probation-only. Do not infer capacity from an old UI
percentage.

**“Relay request failed”**: read the error toast or the affected account card in
Usage & billing. Relay includes a safe reason and HTTP/upstream code when one is
available; it never displays raw provider bodies, tokens or local paths.

**“Relay Pool already has an active request for this logical turn”**: v0.5.4+
recovers pre-commit leases before opening the local Gateway and coalesces
concurrent duplicate request IDs. After an upgrade, fully restart only the
managed Codex Relay copy. Do not delete `state.json`: a committed lease is kept
as recovery-required to prevent duplicated commands or file changes.

**“stream disconnected before completion” after output**: v0.5.4+ recognizes
the `response.output_item.done` boundary without treating its nested item
status as a terminal response. It waits only a short, bounded grace period for
`response.completed`; if the provider remains silent, Relay sends a recovery
terminal and the mux shows an actionable Relay message instead of the generic
`stream closed before response.completed` text. Continue with a new turn after
reviewing the affected result; no side effect is replayed.

**“retry_budget_exhausted”**: every eligible source failed before a complete
terminal event. The message includes a safe error class, attempt count and
`RP-...` correlation reference. Transport cooldown does not mean quota was
consumed or the account was disconnected.

**“recovery-required”**: a quota/network failure happened after output or a side
effect, so automatic replay is unsafe. Review the task result and acknowledge
recovery before continuing. This state is intentional data-loss protection.

**History/path error**: stop trying to copy a native rollout manually. Relay
requires a managed `sessions`/`archived_sessions` path, verifies hash/size and
uses an immutable sibling generation if Windows denies replacement. Preserve
the original file and attach only sanitized diagnostics to an issue.

## Safety checklist

Keep the official Codex app open during verification, use short harmless prompts,
do not click reset-credit actions, and never publish `auth.json`, tokens,
full paths, prompts, model output or user history. Record the exact command and
final exit code for every claimed result.
