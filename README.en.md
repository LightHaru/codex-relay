# Codex Subscription Router

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

> Vietnamese documentation: [README.md](README.md)

Run eligible ChatGPT/Codex subscriptions through one independently patched
desktop copy. The controller account remains the **Primary** identity and the
first routing choice; additional subscriptions are isolated and used only when
Primary cannot accept more work.

This repository is maintained at
[LightHaru/codex-subscription-router](https://github.com/LightHaru/codex-subscription-router).
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
| **Windows account management** | The profile menu can add, inspect, and cancel pending secondary sign-ins. |
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
4. **Failover:** the Router reads and resumes the thread on an eligible
   account, persists the new owner, and forwards the turn there.
5. **All subscriptions depleted:** the app returns one combined quota alert
   with the next known reset instead of repeatedly retrying an exhausted
   account.

Primary is initialized from the default Codex profile. It remains the visible
application identity and the default routing choice; adding a secondary
subscription does not make that subscription the controller.

For protocol-level detail, read [Architecture](docs/ARCHITECTURE.md).

## Platform status and compatibility

| Platform | Current installation path | Verified upstream input |
| --- | --- | --- |
| **macOS, Apple silicon** | Existing signed independent-app workflow | ChatGPT `26.803.61601`, bundle build `6396` |
| **Windows x64 (preview)** | Local-checkout, double-click installer | Store package `OpenAI.Codex_26.810.7004.0_x64__2p2nqsd0c76g0` |

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

`Install Codex Subscription Router.cmd`

The installer:

1. Locates the installed Microsoft Store package through
   `powershell.exe Get-AppxPackage`. The Store app does not need to be closed.
2. Builds and verifies a staged independent copy before touching the active
   Router.
3. Stops only executables beneath
   `%LOCALAPPDATA%\Codex Subscription Router`. It never selects processes by
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
git clone https://github.com/LightHaru/codex-subscription-router.git
cd codex-subscription-router
~~~

Open the checkout in Explorer and double-click
`Install Codex Subscription Router.cmd`. No terminal command is needed for the
install itself once the checkout exists.

For development or automation, use:

~~~powershell
py -3 scripts/patch_windows.py --force --launch
~~~

| Path | Purpose |
| --- | --- |
| `%LOCALAPPDATA%\Codex Subscription Router\app` | Independent copied application and `routerctl.exe` |
| `%LOCALAPPDATA%\Codex Subscription Router\Codex Subscription Router.cmd` | Launcher with a dedicated Electron profile |
| Windows Desktop known folder | Direct `Codex Subscription Router.lnk` shortcut |
| `%APPDATA%\Codex Subscription Router` | Dedicated Windows Electron profile |
| `%USERPROFILE%\.codex-mux` | Router state, account homes, local token, and recoverable backups |

See [Windows installation](docs/WINDOWS.md) for implementation and
troubleshooting detail.

## macOS Apple silicon

The existing macOS workflow produces independently signed applications at:

- `~/Applications/Codex Subscription Router.app`
- `~/Applications/Codex Subscription Router Computer Use.app`

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
git clone https://github.com/LightHaru/codex-subscription-router.git
cd codex-subscription-router
./install.sh
~~~

The script installs locked build dependencies, stops only a prior independent
Router bundle, creates a recoverable backup, builds/signs the app, and launches
it. For manual control:

~~~sh
npm ci --ignore-scripts
python3 scripts/patch_app.py
open "$HOME/Applications/Codex Subscription Router.app"
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
| Accessibility | Codex Subscription Router |
| Screen & System Audio Recording | Codex Subscription Router Computer Use |

See [SMOKE-TEST.md](docs/SMOKE-TEST.md) for the signed-app verification flow.

## Add a subscription

### Windows official browser sign-in

1. Open the profile menu at the bottom of the Router sidebar.
2. Select **Add another subscription**.
3. The Router asks the official Codex child app-server to begin the supported
   ChatGPT browser sign-in. It can open the verified page automatically; use
   **Continue to ChatGPT** if the browser was blocked.
4. Complete sign-in in the browser, then return to the Router. The dialog
   closes automatically and a success toast confirms the connection.

The Windows Router displays **no device code**, does not collect a password,
and accepts only HTTPS `chatgpt.com` or `auth.openai.com` authorization URLs.
The official child owns the callback and credential storage.

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
| `~/Library/Application Support/Codex Subscription Router` | Independent macOS desktop profile |
| `%APPDATA%\Codex Subscription Router` | Independent Windows desktop profile |

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

When the official app updates, do **not** overwrite the independent Router.
First check [Compatibility](docs/COMPATIBILITY.md), then rebuild from reviewed
source:

| Platform | Rebuild action |
| --- | --- |
| Windows | Double-click `Install Codex Subscription Router.cmd` again, or run `py -3 scripts/patch_windows.py --force --launch` |
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
  anchors.
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
