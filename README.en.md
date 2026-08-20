# Codex Relay

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

> Vietnamese documentation: [README.md](README.md)

Run eligible ChatGPT/Codex subscriptions through one independently patched
desktop copy. The account found in the original Codex profile is selected as
**Primary** on first launch, but Router stores that choice independently. You
can select a different Primary in Router without changing the account selected
by the original Codex app. Additional subscriptions are isolated and used only
when Primary cannot accept more work.

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
| **Primary-first routing** | New chats use the controller/Primary account while it has usable capacity. |
| **Isolated secondary subscriptions** | Every additional account has its own Codex home and credentials. |
| **Sticky thread ownership** | Follow-up turns stay with the assigned account, preserving conversation context. |
| **Quota-aware failover** | A depleted or unavailable owner can continue a thread through an eligible secondary account. |
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
    ├── Primary / controller → default Codex home
    ├── Subscription 2       → isolated Codex home
    └── Subscription N       → isolated Codex home
                              │
                              └── persistent thread ID → account owner
~~~

### Primary-first and failover behavior

1. **New chat:** Primary is selected whenever both its short and longer usage
   windows have capacity.
2. **Fallback:** only after Primary is unavailable or depleted does the Router
   rank eligible secondary accounts using quota pressure, reset availability,
   pinned-thread count, and stable ordering.
3. **Follow-up:** the thread returns to its persisted owner; it is not moved
   merely to balance load.
4. **Failover:** the Router copies the local rollout history into the eligible
   account's isolated Codex home, resumes the same thread there, persists the
   new owner, and forwards the turn there. The source history remains intact.
5. **All subscriptions depleted:** the app returns one combined quota alert
   with the next known reset instead of repeatedly retrying an exhausted
   account.

Primary is initialized from the default Codex profile only once, during Router
state bootstrap. Account settings can then select any connected ChatGPT
subscription as Primary; adding or changing an account in the separate Codex
app does not overwrite Router's stored choice.

### Select Primary and manage subscriptions

Open the Router profile menu and choose **Account settings**. **Set as Primary**
changes the Router controller, restarts only Router-owned Codex app-server
sessions, and refreshes the panel before it reports success; it does not log
the account in or out of the native Codex app. **Remove** is available
for connected secondary accounts after an explicit confirmation. Router requires
you to choose another Primary first, and warns when the account owns existing
chats. Removing an account clears Router's assignment metadata but never deletes
the native `~/.codex` home or the source history file.

The **Usage remaining** summary adds the known remaining percentages across
connected subscriptions. If one account has not returned quota data yet, the
known total remains visible and the affected row says **Updating quota…** or
**Quota unavailable**; missing data is never presented as a fabricated zero or a
bare dash.

Each account row shows the ChatGPT profile display name, then username or email
as a fallback, alongside its Router label and plan. Its own quota row displays
the countdown for every reset window returned for that subscription (for
example, `Reset 5h: 1h 20m`); hover the row for the full local timestamp. If
ChatGPT does not report a reset time, the Router says so rather than guessing.

### Continuing an old chat

You can continue a pre-existing local Codex chat through the Router; it is not
limited to newly created chats. Open the old chat from the Router's sidebar and
send the next message there. If its former owner is depleted, the Router copies
that chat's local rollout file into the selected fallback account's isolated
history store before continuing it. This preserves the original local history
and makes subsequent turns use the fallback account.

An already-sent turn in the separate Microsoft Store app cannot be intercepted:
that app does not communicate with the Router. Keep it open if you wish, but
open the same old chat in **Codex Relay** before sending the next
message that should be quota-routed.

For protocol-level detail, read [Architecture](docs/ARCHITECTURE.md).

## Platform status and compatibility

| Platform | Current installation path | Verified upstream input |
| --- | --- | --- |
| **macOS, Apple silicon** | Existing signed independent-app workflow | ChatGPT `26.803.61601`, bundle build `6396` |
| **Windows x64 (preview)** | Local-checkout, double-click installer | Store packages `26.810.7004.0` and `26.818.2441.0` (both have reviewed renderer profiles) |

The patchers are deliberately fail-closed. They verify the official bundle
version/hash and exact renderer and binary anchors before activation. An
unreviewed upstream update stops the build rather than applying a partial
rewrite. See [Compatibility](docs/COMPATIBILITY.md) for exact hashes and
reviewed versions.

## Windows x64: local one-click installer

### Scope

The Windows installer is a **double-click installer from this source
checkout**. It is **not** a standalone `Setup.exe`, does not bundle
Node/Go/Python, and does not redistribute the official Store application.

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
- this source checkout.

If `node_modules` is absent, the installer obtains the locked ASAR build tool
using `npm ci --ignore-scripts`. It does not automatically install Python, Go,
or Node, and it never silently bypasses an unreviewed Store hash.

### Install from a checkout

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
| `%USERPROFILE%\.codex-mux` | Router state, account homes, local token, and recoverable backups |
| `%LOCALAPPDATA%\Codex Relay Updater\router-updater.exe` | External helper used by the in-app update button |

### Migration from Codex Subscription Router 0.2.x

No account needs to be added again. A `v0.3.0` install stages Relay first,
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
configuration. After upgrading, close and reopen the **Codex Subscription
Router** shortcut once. If an old dialog remains, run the checkout installer
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

### Windows private in-app sign-in

1. Open the profile menu at the bottom of the Router sidebar.
2. Select **Add another subscription**.
3. The Router asks the official Codex child app-server to begin the supported
   ChatGPT sign-in and opens it in a private child window owned by the Router.
   It does not launch the user's default browser.
4. Complete sign-in on the official page in that window. Every launch uses a
   new temporary, non-persistent Electron session, so it does not reuse a
   prior login's cookies or web storage.
5. If the child window is closed, choose **Open secure sign-in** to create a
   new private sign-in window, or choose **Cancel sign-in** to discard the
   unfinished secondary subscription.
6. When the official child reports a connected account, both windows close and
   the Router shows one success notification.

The Windows Router displays **no device code**, does not collect a password,
and accepts only HTTPS `chatgpt.com` or `auth.openai.com` authorization URLs.
The official child owns the callback and credential storage. The private
window has no Router preload or Node access, uses no main-app cookies, and
clears its temporary session data when it closes.

> Note: the Router guarantees fresh cookies, local storage, and cache for this
> window. It cannot—and should not—erase OS-level SSO, passkeys, or identity
> state managed by Windows, Google, Microsoft, or Apple. If an official page
> preselects an account through SSO, choose **Use another account** on that
> page.

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

### Native rate-limit resets

The native rate-limit sheet includes an account picker. Selecting a
subscription changes the displayed usage/reset balance and ensures a consumed
reset applies only to that account.

## Local data and safety

| Location | Purpose |
| --- | --- |
| `~/.codex` | Primary Codex credentials, conversations, and cache |
| `~/.codex-mux/state.json` | Account metadata and persisted thread ownership |
| `~/.codex-mux/accounts/<id>/codex-home` | Isolated secondary account homes |
| `~/.codex-mux/control-token` | Random token for the loopback-only control service |
| `~/.codex-mux/backups` | Recoverable Router application backups |
| `~/Library/Application Support/Codex Subscription Router` | Legacy-compatible macOS Electron profile retained across the product rename |
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
| Windows | Double-click `Install Codex Relay.cmd` again, or run `py -3 scripts/patch_windows.py --force --launch` |
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
  anchors. The Windows patcher currently retains profiles for both the older
  `26.810.7004.0` package and the newer `26.818.2441.0` package.
- Windows is a version-pinned preview port; macOS Computer Use/Appshots remain
  macOS-specific.
- The initial merged history fetch is limited to 500 threads per account.
- Combined “skills explored” totals can count a skill once per account because
  upstream profile responses expose counts rather than global skill IDs.
- An account must be valid, enabled, connected, and have capacity before the
  Router can select it.

## Attribution

The original project and its copyright notice are credited to **Bennett
Blackham (b-nnett)**. This LightHaru-maintained repository retains the original
MIT license and notices; contributions should preserve applicable attribution
and license text.

## Contributing, security, and license

- Read [CONTRIBUTING.md](CONTRIBUTING.md) before submitting changes.
- Follow [SECURITY.md](SECURITY.md) for credential, signing, or local-service
  reports.
- Releases follow the source-only process in
  [RELEASING.md](docs/RELEASING.md).
- Source is available under the [MIT License](LICENSE). ChatGPT, Codex, and
  the official desktop applications are OpenAI products and are not licensed
  by this repository.
