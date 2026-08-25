# Codex Relay

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

> Vietnamese documentation: [README.md](README.md)

Run eligible ChatGPT/Codex subscriptions through one independently patched
desktop copy. On Windows, Relay starts with its own `codex-home` and
file-backed credential store; accounts signed in to the Store Codex app are
**not imported or shared**. Sign in to the accounts you want to use inside
Relay. You can select a different Primary in Relay without changing the
account selected by the original Codex app. **Primary is a
configuration/controller identity, not a routing lock:** new chats are shared
fairly across every eligible account with capacity, while existing Relay chats
retain their assigned owner until failover is necessary.

This repository is maintained at
[LightHaru/codex-relay](https://github.com/LightHaru/codex-relay).
It contains source and build tooling only. It does not distribute OpenAI/ChatGPT
binaries or modify the official installed package.

> [!WARNING]
> This is an unofficial, version-sensitive project. It is not affiliated with
> or supported by OpenAI. Review the source, keep every connected subscription
> compliant with its governing terms, and do not use it to bypass access
> controls or rate limits.

![Multi-subscription account menu](screenshots/account-menu.png)

## Highlights

| Capability | Behavior |
| --- | --- |
| **Persistent quota scheduler** | Weighted-deficit round robin uses fresh short/long quota, reservations, low-water reserve, and a restart-safe cursor. |
| **Isolated secondary subscriptions** | Every additional account has its own Codex home and credentials. |
| **Shared Relay Memory** | One canonical task history is incrementally materialized to exactly one account worker per completed-turn boundary. |
| **Quota-aware failover** | A depleted or unavailable owner can continue a thread through an eligible secondary account. |
| **Exact-model capacity retry** | A transient selected-model capacity error retries the same turn and model, never silently changes the model. |
| **Windows account management** | The profile menu can add, inspect, cancel, remove, and change the Primary subscription. |
| **Account-aware settings** | Profile statistics, supported Plugin surfaces, and rate-limit resets can be selected per subscription. |

## Routing model

The Router replaces the copied app's bundled Codex executable with a small Go
multiplexer. The original executable remains beside it as `codex.real`. One
desktop/app-server connection is routed to one official Codex child per
connected subscription.

~~~text
Independent Router copy
        │
        │ one desktop/app-server connection
        ▼
    codex-mux
    ├── Primary / controller → Relay-only codex-home (Windows)
    ├── Subscription 2       → isolated Codex home
    └── Subscription N       → isolated Codex home
                              │
                              └── persistent thread ID → account owner
~~~

### Persistent scheduling, shared task memory, and safe failover

1. **New chat:** Relay reads every connected account's fresh short and longer
   quota windows. Depleted/open-circuit accounts are removed, a low-water
   reserve is retained when possible, and persistent weighted-deficit round
   robin selects the worker. Deficits, cursor, and active reservations survive
   restart. The first turn stays on the worker that accepted `thread/start`,
   because no authoritative rollout exists before that turn completes.
   Each isolated account is cross-checked through both app-server
   `account/rateLimits/read` and native Usage. Explicit `allowed` and
   `limit_reached` flags cannot override a 100%-used window, while a real
   `usageLimitExceeded` turn rejection invalidates cached quota immediately.
   An open circuit rejoins the pool after a newer reset epoch confirms capacity
   or after a successful probation turn on older Codex builds.
2. **Follow-up:** the default **Balanced** policy may choose another worker only
   after the preceding turn is terminal. **Sticky** retains an eligible worker;
   **Rotate** changes worker after every completed turn. A task never has two
   active worker generations.
3. **Old/unassigned chat:** Relay finds the rollout in a Relay-owned account
   home before applying the normal failover path. If a known thread still
   points at the former Store `sessions` directory, opening that thread in
   Relay copies only its one rollout into the selected Relay home; credentials
   and the native source are never imported or modified.
4. **Failover:** Relay journals `PREPARED → COPIED → RESUMED → COMMITTED`,
   checkpoints the stable rollout in account-neutral Relay Memory, verifies its
   SHA-256/prefix, incrementally materializes it into the target home, resumes
   the same thread, then atomically advances ownership and generation. Startup
   rolls interrupted handoffs back to their source generation.
   Active Goal state is also read through `thread/goal/get`, restored and
   verified on the target before commit. A quota-limited goal resumes as
   active on a capacity-bearing worker, while any token budget is reduced by
   tokens already consumed so its remaining safety bound is preserved. If
   app-server creates a Goal continuation without a renderer `turn/start`, a
   terminal quota error still hands off at that durable boundary without
   replaying the previous command or tool work.
5. **Selected model at capacity:** Relay retries the exact original `turn/start`
   payload — including the selected model — up to three times with short
   exponential backoff on the same account. It never changes the model or
   consumes another subscription merely because that model is busy.
6. **Side-effect boundary:** automatic quota retry is allowed only before a
   command, file change, hook, approval, or tool side effect starts. Afterwards
   the task becomes `recovery required` and waits for explicit review, avoiding
   duplicate execution.
7. **All subscriptions depleted:** the app returns one combined quota alert
   with the next known reset instead of repeatedly retrying an exhausted
   account.

The Relay profile menu distinguishes **Relay Controller** from **Current Task
Route** and exposes Sticky, Balanced, and Rotate policies. The authenticated
loopback observability surface provides `/v1/router/status`,
`/v1/thread-route`, `/v1/routing/decisions`, `/v1/routing/policy`, and routing
events on `/v1/events`.

#### Routing Inspector: verify the account that actually ran a task

Relay adds a compact **Running via** row above the composer in a task. It shows
the actual worker for the current logical turn, not the Primary/Relay
Controller. Open **Routing details** for an in-flow, keyboard-accessible panel;
it does not cover task content, enter the Settings sidebar, or replace the
native Settings shell.

The roles are deliberately distinct:

- **Current owner** owns the task's authoritative generation.
- **Active worker** is processing the current logical turn; when idle, Relay
  explicitly says that no turn is running.
- **Last completed via** identifies the worker that completed the previous
  turn.
- **Last quota attributed to** is confirmed only after a newer upstream
  snapshot shows a measurable quota decrease.
- **Next Candidate (preview)** is what the current policy would choose if a new
  turn started now. Reading or refreshing it never increments dispatches,
  changes cursor/deficits, creates a reservation, changes ownership, opens a
  circuit, or alters the later real selection.

**Why this account** shows the normalized score, confirmed quota and a fixed
reason code. `selected_highest_score` means the best currently eligible score;
`eligible_lower_score` remains usable but ranked lower; `skipped_depleted`,
`skipped_cooldown`, `skipped_open_circuit`, `skipped_disconnected`,
`skipped_disabled`, `skipped_unknown_quota`, and `skipped_stale_quota` explain
why a worker was excluded. Balanced may use one account for several turns when
its quota, deficit and reservations produce the better score; Balanced is
quota-weighted scheduling, not strict alternation.

The **Routing timeline** records stable event IDs, timestamps, generation,
worker, reservations, completion, quota attribution and each handoff phase. A
successful handoff shows source → target, its fixed reason and `COMMITTED`.
After a command, tool, hook, approval or file mutation starts, a quota failure
becomes `recovery required`; Relay does not blind-replay the turn. Diagnostics
store a request hash and sanitized categories—not prompts, Goal objectives,
file contents, tool arguments, credentials, control tokens, or absolute paths.

> `500% / 500%` means five isolated subscriptions provide up to 500 percentage
> points of **routing capacity**. It is not one 500% OpenAI subscription, does
> not merge billing balances, and each request still runs on exactly one
> subscription.

On Windows, Relay's Primary starts in its own `codex-home` and is not
initialized from the Store app's default Codex profile. Account settings can
then select any connected ChatGPT subscription as Primary; adding or changing
an account in the separate Codex app does not overwrite Relay's stored choice.
If an older Router state still points any subscription at `%USERPROFILE%\.codex`,
the isolated startup moves that entry to a Relay-owned home under
`%USERPROFILE%\.codex-mux\accounts\...` and shows it as needing sign-in again.
Only the Router's thread-owner metadata is removed; the official Codex
credential and history files are kept intact.

### Select Primary and manage subscriptions

Open the Router profile menu and choose **Account settings**. **Set as Primary**
changes the Router controller, restarts only Router-owned Codex app-server
sessions, and refreshes the panel before it reports success; it does not log
the account in or out of the native Codex app. **Remove** is available
for connected secondary accounts after an explicit confirmation. Router requires
you to choose another Primary first, and warns when the account owns existing
chats. Removing an account clears Router's assignment metadata but never deletes
the native `~/.codex` home or the source history file.

The **Shared quota pool** adds known remaining percentages across connected
subscriptions: five full subscriptions render as `500% / 500%`, while a pool
with 155 points remaining renders as `155% / 500%` instead of averaging to
31%. If one account has not returned quota data yet, the
known total remains visible and the affected row says **Updating quota…** or
**Quota unavailable**; missing data is never presented as a fabricated zero or a
bare dash.

Each account row shows the ChatGPT profile display name, then username or email
as a fallback, alongside its Router label and plan. Its own quota row displays
the countdown for every reset window returned for that subscription (for
example, `Reset 5h: 1h 20m`); hover the row for the full local timestamp. If
ChatGPT does not report a reset time, the Router says so rather than guessing.

### Continuing an old chat

You can continue pre-existing chats already owned by Relay; it is not limited
to newly created chats. Open the old chat from the Relay sidebar and send the
next message there. If its former owner is depleted, the Router copies that
chat's local rollout file into the selected fallback account's isolated
`sessions` or `archived_sessions` history store before continuing it. Relay
accepts both absolute rollout paths and `CODEX_HOME`-relative paths emitted by
different app-server versions. This preserves the original local history and
makes subsequent turns use the fallback account.

If an older Router state points to a rollout that still lives in the former
native Store `sessions` directory, opening that known chat in Relay performs
the same one-file, read-only migration into the selected Relay account. It
does not copy credentials or configuration and leaves the native rollout
unchanged.

Chats created in the separate Store Codex app remain native to that app unless
the user explicitly opens the same known chat in Relay. In that case Relay
performs a one-rollout, read-only migration into the selected Relay account;
it never reads `auth.json`/`config.toml`, starts a child on the Store home, or
edits/deletes the native source. Store-only history is not scanned or imported
in bulk, so deleting an account in one desktop cannot remove the other
desktop's credentials or source history.

An already-sent turn in the separate Microsoft Store app cannot be intercepted:
that app does not communicate with the Router. Keep it open if you wish, but
open the same old chat in **Codex Relay** before sending the next
message that should be quota-routed.

For protocol-level detail, read [Architecture](docs/ARCHITECTURE.md) and the
authoritative [Shared-Memory Router v2](docs/SHARED-MEMORY-ROUTER.md).

## Platform status and compatibility

| Platform | Current installation path | Verified upstream input |
| --- | --- | --- |
| **macOS, Apple silicon** | Existing signed independent-app workflow | ChatGPT `26.803.61601`, bundle build `6396` |
| **Windows x64 (preview)** | One-command bootstrap or local checkout | Store packages `26.810.7004.0`, `26.818.2441.0`, `26.818.3698.0`, `26.818.4152.0`, `26.818.5229.0`, `26.818.5345.0`, and `26.818.8289.0` (all have reviewed renderer profiles) |

The patchers are deliberately fail-closed. They verify the official bundle
version/hash and exact renderer and binary anchors before activation. An
unreviewed upstream update stops the build rather than applying a partial
rewrite. See [Compatibility](docs/COMPATIBILITY.md) for exact hashes and
reviewed versions.

## Windows x64: one-command installer

### Scope

The Windows installer has a **one-command bootstrap** for users who already
have the prerequisites. It downloads the latest GitHub Release manifest,
validates the exact expected source-asset URL and SHA-256, then runs the same
reviewed local installer from that source. It is **not** a standalone
`Setup.exe`, does not bundle Node/Go/Python, and does not redistribute the
official Store application.

Run this in a normal PowerShell window after reviewing the linked release:

~~~powershell
irm https://github.com/LightHaru/codex-relay/releases/latest/download/install-codex-relay.ps1 | iex
~~~

No `git clone` is required. The verified source is retained under
`%LOCALAPPDATA%\Codex Relay Bootstrap\...` for inspection, the Desktop
shortcut is repaired, and Relay opens when installation completes. Only run
this line from the official
[LightHaru/codex-relay release](https://github.com/LightHaru/codex-relay/releases).

The checkout/double-click path remains available for contributors or users who
prefer to inspect and run the source locally:

Once the checkout is available, double-click:

`Install Codex Relay.cmd`

The installer:

1. Locates the installed Microsoft Store package through
   `powershell.exe Get-AppxPackage`. The Store app does not need to be closed.
2. Builds and verifies a staged independent copy before touching the active
   Router.
3. Stops only executables beneath
   `%LOCALAPPDATA%\Codex Relay` (and, during migration, the specifically
   allow-listed legacy Router root). It never selects processes by
   the `ChatGPT.exe` name alone, so the Microsoft Store app is not targeted.
4. Moves the prior stable Router copy to a recoverable
   `%USERPROFILE%\.codex-mux\backups\...` directory.
5. Creates or repairs a direct Desktop shortcut and launches the independent
   Router.

### Requirements

- Windows 11 x64;
- the Microsoft Store ChatGPT/Codex package already installed;
- Python 3;
- Go 1.26 or newer;
- Node.js 22.12+ and npm;
- Internet access to download the locked ASAR tool and, for the bootstrap, the
  verified release source.

If `node_modules` is absent, the installer obtains the locked ASAR build tool
using `npm ci --ignore-scripts`. It does not automatically install Python, Go,
or Node, and it never silently bypasses an unreviewed Store hash.

### Install from a checkout instead

~~~powershell
git clone https://github.com/LightHaru/codex-relay.git
cd codex-relay
~~~

Open the checkout in Explorer and double-click
`Install Codex Relay.cmd`. No terminal command is needed for the
install itself once the checkout exists.

For development or automation, use:

~~~powershell
py -3 scripts/patch_windows.py --force --launch
~~~

| Path | Purpose |
| --- | --- |
| `%LOCALAPPDATA%\Codex Relay\app` | Independent copied application and `routerctl.exe` |
| `%LOCALAPPDATA%\Codex Relay\Codex Relay.cmd` | Launcher with a dedicated Electron profile |
| Windows Desktop known folder | Direct `Codex Relay.lnk` shortcut |
| `%APPDATA%\Codex Relay` | Dedicated Electron profile for a new Relay install |
| `%APPDATA%\Codex Relay\codex-home` | Relay-only primary credentials, sessions, and configuration; never shared with the Store app |
| `%USERPROFILE%\.codex-mux` | Router state, secondary account homes, local token, and recoverable backups; not the Store app's credential home |
| `%LOCALAPPDATA%\Codex Relay Updater\router-updater.exe` | External helper used by the in-app update button |

### Migration from an earlier 0.2.x installation

No account needs to be added again. A Relay install stages the new copy first,
stops only the former managed Router directory, and moves that old app copy to
`%USERPROFILE%\.codex-mux\backups\...` before starting
`%LOCALAPPDATA%\Codex Relay\app`. Account state, assigned-thread metadata,
secondary homes, and chat history remain in place. To avoid moving an Electron
profile while it is closing, Relay keeps using the old profile for the first
migrated install when a new profile does not exist. After verifying Relay,
remove any obsolete old shortcut or backup manually if desired.

### One-click Router updates after the first install

Published Router releases include a small HTTPS GitHub manifest and a
source-only archive. The running Windows copy checks that manifest quietly.
When a newer Router version is available, it shows **Update now** in the app.
The button downloads the archive, checks its SHA-256, stages it with the
current official Store source, closes only the Router processes, repairs the
shortcut, and reopens the new version. Router account state, chat ownership,
and the separate Electron profile stay in place.

The updater is intentionally outside the managed app directory, so it can
replace a running copy safely. It never modifies the Microsoft Store app and
never receives passwords, OAuth tokens, or the local control token. If a
release manifest is absent, no banner is shown.

This is a **source-release updater**, not a standalone `Setup.exe`: the first
install still requires Python 3, Go, Node.js/npm, and the official Store app.
See [Windows installation](docs/WINDOWS.md#in-app-updates) for the full flow,
failure recovery, and the security boundaries.

See [Windows installation](docs/WINDOWS.md) for implementation and
troubleshooting detail.

### If you see “Unable to send message — Update Agent sandbox”

This is a Windows sandbox setup failure, not a quota failure. Some Codex
Windows builds get stuck when `[windows] sandbox = "elevated"` is enabled; the
same setup error can then block both new chats and existing chats.

The Windows Router forces `unelevated` only for Router-owned processes and
secondary account homes. It does not edit, delete, or log out the native
`%USERPROFILE%\.codex` home, so the Microsoft Store Codex app keeps its own
configuration. After upgrading, close and reopen the **Codex Relay** shortcut
once. If an old dialog remains, run the one-command bootstrap or checkout installer
again so it replaces the Router wrapper, then launch the Router again.

## macOS Apple silicon

The existing macOS workflow produces independently signed applications at:

- `~/Applications/Codex Relay.app`
- `~/Applications/Codex Relay Computer Use.app`

Requirements:

- the official ChatGPT app at `/Applications/ChatGPT.app`;
- Xcode Command Line Tools;
- Python 3;
- Go 1.26 or newer;
- Node.js 22.12+ and npm;
- an Apple Development or Developer ID Application signing identity.

Use the current LightHaru checkout so the installer builds the source you
reviewed:

~~~sh
git clone https://github.com/LightHaru/codex-relay.git
cd codex-relay
./install.sh
~~~

The script installs locked build dependencies, stops only a prior independent
Router bundle, creates a recoverable backup, builds/signs the app, and launches
it. For manual control:

~~~sh
npm ci --ignore-scripts
python3 scripts/patch_app.py
open "$HOME/Applications/Codex Relay.app"
~~~

Reuse the same Apple signing team for every rebuild. Changing teams can
invalidate existing privacy grants; the patcher rejects an unexpected change
unless an explicit diagnostic override is supplied. Ad-hoc signing is for
diagnostics only, and Appshots/Computer Use can be unavailable under it.

### macOS permissions

When needed, grant the **independent Router**, not the official ChatGPT app,
these permissions in **System Settings → Privacy & Security**:

| Permission | Independent application |
| --- | --- |
| Accessibility | Codex Relay |
| Screen & System Audio Recording | Codex Relay Computer Use |

See [SMOKE-TEST.md](docs/SMOKE-TEST.md) for the signed-app verification flow.

## Add a subscription

### Windows browser sign-in

1. Open the profile menu at the bottom of the Router sidebar.
2. Select **Add another subscription**.
3. The Router asks the official Codex child app-server to begin the supported
   ChatGPT sign-in and opens the returned HTTPS authorization link in your
   default browser. This is the documented OAuth hand-off; the child process
   still owns the localhost callback for this subscription.
4. Complete sign-in on the official page. If the browser is already signed in
   to another account, choose **Use another account** there.
5. If the browser page was closed, choose **Open secure sign-in** to open it
   again, or choose **Cancel sign-in** to discard the unfinished subscription.
   Relay persists an intentionally started pending flow, so reopening Relay
   restores its **Waiting for sign-in** row. A disconnected account that was
   never in a pending flow is shown as **Not connected**, not as a fake active
   browser sign-in.
6. When the child reports a connected account, the confirmation closes and the
   Router shows one success notification. A web OAuth error leaves the pending
   row available for a retry instead of deleting it immediately.

The Windows Router displays **no device code**, does not collect a password,
and forwards only HTTPS `chatgpt.com` or `auth.openai.com` authorization URLs.
The official child owns the localhost callback and credential storage. Relay
never reads a password, callback code, or OAuth token.

> Note: the default browser may reuse its existing SSO, passkeys, or identity
> state. Relay cannot—and should not—erase that OS/browser state. If the
> official page preselects an account, choose **Use another account** there.

### Cancel a pending sign-in

Choose **Cancel sign-in** in the dialog or from a pending subscription row.
The Router cancels the official child flow, removes only the unconnected
**secondary** account and its isolated local home, and refreshes the menu.

If browser completion wins the cancellation race, the connected account is
preserved rather than deleted. Primary cannot be cancelled through this flow.

The current macOS UI retains its existing device-code sign-in experience until
its UI is migrated separately.

## Profiles, Plugins, and resets

![Account-scoped plugin connections](screenshots/plugin-account-picker-secondary-final.png)

### Profile statistics

Profile statistics begin in a combined view with overlapping account photos.
Select a photo to inspect only that subscription's identity and statistics;
select it again to return to the combined view.

### Settings → Plugins

The Plugins page provides a subscription picker. Plugin definitions and managed
MCP configuration are shared, while Apps, connection status, and OAuth login
operations are scoped to the selected subscription.

### Account settings → Usage limit resets

Account settings loads a native-style **Usage limit resets** card for every
connected subscription. It shows available/applicable counts, each reset's
title, status, and expiry, and provides a **Use reset** button for available
credits. The action calls only the selected subscription, refreshes its balance,
and cannot consume another account's credit. Reset data is fetched through the
isolated Relay account; credentials remain scoped to that account. Renderer
profiles that provide the native reset picker keep their native behavior as
well.

### Settings → Usage & billing

The native Settings → Usage & billing page remains the page shell: its sidebar,
navigation, layout, and native billing actions are unchanged. Relay inserts a
**Shared quota pool** panel into that page's content column only.
It is normal in-flow content (never a fixed overlay and never a new sidebar
item). The additive pool is primary; per-worker plans, credits, quota windows,
reset times, reset credits, and errors are collapsed under **Worker
diagnostics**. This does not merge upstream credentials, billing or reset
credits: Relay combines scheduling capacity while workers remain isolated.

The native **Use reset** action and the Relay reset cards both stay scoped to
the subscription shown on the card. Account settings remains the place to
change Primary or remove a subscription. Billing, plan, credit purchase, and
cancel-plan actions continue to belong to the native page.

## Local data and safety

| Location | Purpose |
| --- | --- |
| `%APPDATA%\Codex Relay\codex-home` | Relay-only primary credentials, conversations, and configuration on Windows |
| `~/.codex` | Native Store Codex data; Relay may read one explicitly requested legacy rollout for migration, but never reads credentials/configuration or modifies/deletes this directory |
| `~/.codex-mux/state.json` | Account metadata and persisted thread ownership |
| `~/.codex-mux/accounts/<id>/codex-home` | Isolated secondary account homes |
| `~/.codex-mux/control-token` | Random token for the loopback-only control service |
| `~/.codex-mux/backups` | Recoverable Router application backups |
| `%APPDATA%\Codex Relay` | Independent Windows desktop profile for a new Relay install |

- The control service binds only to `127.0.0.1` and uses a random 256-bit
  token for private routes.
- OAuth material stays in the relevant account home. It is not returned by the
  control API or intentionally logged by the Router.
- The project has no Router telemetry endpoint and does not distribute patched
  OpenAI application binaries.
- Plugin/MCP definitions are synchronized from Primary. Inline secrets inside
  shared MCP configuration can therefore be copied to isolated account homes;
  account isolation is **not** a separate secret boundary for those shared
  definitions.

On Windows, signing in, signing out, or removing an account in the Store Codex
app does not change Codex Relay's account list. Relay uses its own `codex-home`
and file-backed credential store; sign in to that account separately inside
Relay when you want to use it there.

Read [SECURITY.md](SECURITY.md) and
[Security model](docs/SECURITY-MODEL.md) before reporting a credential,
signing, or local control-service issue.

## Update or rebuild

For a Router release, use the in-app **Update now** notice on Windows. It is
the normal path for end users and performs the download, verification,
restart, and relaunch without a command. When the official Store app itself
updates, do **not** overwrite the independent Router. First check
[Compatibility](docs/COMPATIBILITY.md), then rebuild from a reviewed source
checkout:

| Platform | Rebuild action |
| --- | --- |
| Windows | Use the in-app **Update now** banner when present; after an official Store update, rerun the one-command bootstrap or double-click `Install Codex Relay.cmd` |
| macOS | Run `./install.sh` from the checkout, or `python3 scripts/patch_app.py --force` |

Unknown official bundles and changed renderer anchors fail closed. Preserve the
backup until the rebuilt Router has passed a smoke test.

## Development and verification

~~~sh
npm ci --ignore-scripts
npm run check
npm run release:check
~~~

Run the focused Windows renderer/installer checks directly with:

~~~powershell
python scripts/test_patch_windows.py
~~~

The verification suite includes Go tests, vetting, JavaScript syntax/UI tests,
Python patcher tests, exact renderer-anchor checks, and release metadata
checks. The optional current-renderer fixture runs only when
`CODEX_MUX_WINDOWS_RENDERER_DIR` points to a reviewed unpacked Store renderer.

## Limits and non-goals

- This is source-only tooling. There is currently **no standalone Windows
  `Setup.exe`**, no bundled Node/Go/Python runtime, and no redistributed
  ChatGPT/Codex binary.
- Upstream app updates can require a deliberate compatibility review and new
  anchors. The Windows patcher currently retains reviewed profiles for
  `26.810.7004.0`, `26.818.2441.0`, `26.818.3698.0`, `26.818.4152.0`, and
  `26.818.5229.0`, `26.818.5345.0`, and `26.818.8289.0`.
- Windows is a version-pinned preview port; macOS Computer Use/Appshots remain
  macOS-specific.
- The initial merged history fetch is limited to 500 threads per account.
- Combined “skills explored” totals can count a skill once per account because
  upstream profile responses expose counts rather than global skill IDs.
- An account must be valid, enabled, connected, and have capacity before the
  Router can select it.
- Cross-account task handoff is enabled only when the installed Relay manifest
  identifies an exact reviewed `app.asar` profile. A missing or unknown profile
  keeps the requested policy visible but makes its effective policy Sticky; it
  never attempts a speculative history copy/resume.
- No reviewed profile currently proves safe incomplete-turn resume. Relay can
  change workers between completed turns and can retry a quota rejection before
  visible output or side effects; after a command, tool, hook, approval, or file
  mutation it requires recovery review instead of replaying the turn.
- Automated validation uses fake app-server workers and temporary homes. Live
  Windows E2E requires explicit operator approval because it restarts/rebuilds
  the installed Relay and may otherwise consume real quota.

## Attribution

Thanks to **Bennett Blackham (b-nnett)** for the original project and its
copyright notice. This LightHaru-maintained repository retains the original MIT
license and notices; contributions should preserve applicable attribution and
license text.

## Contributing, security, and license

- Read [CONTRIBUTING.md](CONTRIBUTING.md) before submitting changes.
- Follow [SECURITY.md](SECURITY.md) for credential, signing, or local-service
  reports.
- Releases follow the source-only process in
  [RELEASING.md](docs/RELEASING.md).
- Source is available under the [MIT License](LICENSE). ChatGPT, Codex, and
  the official desktop applications are OpenAI products and are not licensed
  by this repository.
