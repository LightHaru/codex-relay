# Windows preview

The Windows port keeps the official Microsoft Store package untouched. Its
patcher copies the package's `app` directory to a user-writable location,
renames `resources\codex.exe` to `resources\codex.real.exe`, and puts the
compiled `codex-mux.exe` in its place. The copied Electron app therefore starts
the same per-account Codex app-server multiplexer used by the macOS build.

It injects a small DOM bridge into the copied renderer for the profile menu
and signs-in, then applies narrowly version-pinned patches to the reviewed
Windows renderer build for Profile statistics, Plugins account scope, and the
native rate-limit reset sheet. The patcher checks every renderer anchor exactly
once; an unfamiliar Microsoft Store update stops instead of applying a rewrite
to an unknown bundle.

## Install

For the local checkout, double-click:

`Install Codex Subscription Router.cmd`

It uses `Get-AppxPackage` first to locate the installed Microsoft Store Codex
package, builds and verifies a staged independent copy, upgrades only the
previous Router copy, repairs the direct Desktop shortcut, and launches the
Router when successful. It does **not** close, modify, or replace the Microsoft
Store app.

This is a local-source installer, not a standalone redistributable `.exe`.
Windows x64, the Microsoft Store ChatGPT/Codex package, Python 3, Go, and Node
are still required. If the checked-out `node_modules` directory is missing, the
installer runs lockfile-resolved `npm ci --ignore-scripts` before patching.

It supports the installed Microsoft Store app whose `app.asar` SHA-256 is
recorded in `scripts/patch_windows.py`. An unrecorded official update stops by
default. Review it before explicitly passing `--allow-untested-source`.

For a scripted upgrade, the equivalent command is:

```powershell
python scripts/patch_windows.py --force --launch
```

The patcher creates or repairs the Desktop shortcut by default. Use
`--no-desktop-shortcut` only for an unattended developer build. If Store
registration cannot be read, open the official app once and retry, or pass the
official app directory using `--source`.

The patcher creates:

| Path | Purpose |
| --- | --- |
| `%LOCALAPPDATA%\Codex Subscription Router\app` | Independent copied app and `routerctl.exe` |
| `%LOCALAPPDATA%\Codex Subscription Router\Codex Subscription Router.cmd` | Launcher with a dedicated Electron profile |
| `%USERPROFILE%\Desktop\Codex Subscription Router.lnk` | Direct shortcut to the independent copy, with its dedicated profile |
| `%APPDATA%\Codex Subscription Router` | Dedicated Electron profile |
| `%USERPROFILE%\.codex-mux` | Router state, isolated account homes, and local-control token |

On an upgrade the staged copy is validated before any current Router copy is
stopped. The installer stops only processes whose executable path is below
`%LOCALAPPDATA%\Codex Subscription Router`; it never matches the Store
app by process name. The prior stable Router app is moved to
`%USERPROFILE%\.codex-mux\backups\...`, while the Electron profile and all
Router/account state remain in place.

## Account management in the app

Start the copied app with the Desktop shortcut (or the generated `.cmd`
launcher). It brings up the loopback-only control API along with the
multiplexer.

1. Open the profile menu at the bottom of the sidebar.
2. Review the Router usage section and connected subscription rows.
3. Select **Add another subscription**.
4. The Router opens a small in-app confirmation and starts the official
   ChatGPT browser sign-in. If a browser window does not open automatically,
   select **Continue to ChatGPT** in that confirmation.
5. Complete sign-in in the browser, then return to the app. The dialog polls
   only the local router. When the account connects, it closes automatically
   and shows one non-blocking success notification.
6. If you select **Cancel sign-in**, the unfinished secondary account is
   stopped and removed. The Primary account and any connected subscription are
   never removed by this action. A previously abandoned `Waiting for sign-in`
   row also has a **Cancel sign-in** action.

Do not provide a password to the Router UI or `routerctl`. Credentials are
entered only on the official HTTPS `chatgpt.com` / `auth.openai.com` browser
page; the Router does not collect, display, or exchange OAuth tokens.

`routerctl.exe` remains available for local diagnostics (`routerctl list`), but
is not required to add or sign in to an account.

## Profile, Plugins, and resets

The Windows copy provides the same account-selection behavior as the supported
macOS build for the following surfaces:

| Surface | Windows behavior |
| --- | --- |
| **Settings → Profile** | Starts with combined statistics and overlapping connected-account photos. Select a photo to reload that subscription's identity/statistics; select it again to return to the combined view. |
| **Settings → Plugins** | Shows a subscription picker. Plugin definitions and managed MCP configuration remain shared, while Apps, connection status, and OAuth RPCs are sent with the selected account scope. |
| **Usage / rate-limit resets** | Adds a subscription picker to the native sheet. It changes the displayed usage windows and fetches/consumes reset credits only for that account. |

The Profile selector controls the displayed identity and statistics. Native
profile-edit actions remain controller-account actions, so do not use the
selected statistics view as an indication that profile editing is scoped.

## Scope and limitations

The profile-menu bridge targets the current English and Vietnamese visible
labels. If a future upstream update removes or renames its visible `Settings`
or `Log out` rows, the bridge does not inject. Separately, renderer patches are
locked to the recorded `app.asar` hash; review and update their anchors before
building for a new Store version. Windows Computer Use integrations have not
been ported. The multiplexer, sticky thread ownership, and per-account Codex
homes are shared core code and are covered by the Go test suite. Keep each
connected subscription compliant with its governing terms.
