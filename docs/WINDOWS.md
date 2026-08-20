# Windows preview

The Windows port keeps the official Microsoft Store package untouched. Its
patcher copies the package's `app` directory to a user-writable location,
renames `resources\codex.exe` to `resources\codex.real.exe`, and puts the
compiled `codex-mux.exe` in its place. The copied Electron app therefore starts
the same per-account Codex app-server multiplexer used by the macOS build.

It injects a small DOM bridge into the copied renderer for the profile menu
and signs-in, then applies narrowly version-pinned patches to the reviewed
Windows renderer build for Profile statistics, Plugins account scope, the
native rate-limit reset sheet, and the native Settings Usage query. The patcher
checks every renderer anchor exactly once; an unfamiliar Microsoft Store update
stops instead of applying a rewrite to an unknown bundle.

## Install

For a normal user who already has the prerequisites, run this one line in a
normal PowerShell window after reviewing the official release:

```powershell
irm https://github.com/LightHaru/codex-relay/releases/latest/download/install-codex-relay.ps1 | iex
```

The bootstrap downloads the latest GitHub Release manifest, validates the
expected source asset URL and SHA-256, rejects unsafe archive paths, retains
the verified source under `%LOCALAPPDATA%\Codex Relay Bootstrap\...`, and then
runs the same local installer below. It does not require `git clone` and does
not modify the Microsoft Store app.

For a local checkout instead, double-click:

`Install Codex Relay.cmd`

It uses `Get-AppxPackage` first to locate the installed Microsoft Store Codex
package, builds and verifies a staged independent copy, upgrades only the
previous Router copy, repairs the direct Desktop shortcut, and launches the
Router when successful. It does **not** close, modify, or replace the Microsoft
Store app.

This is a local-source installer, not a standalone redistributable `.exe`.
Windows x64, the Microsoft Store ChatGPT/Codex package, Python 3, Go 1.26+, and
Node.js 22.12+/npm are still required. The bootstrap checks these prerequisites
before making any Router change. If the checked-out `node_modules` directory is
missing, the installer runs lockfile-resolved `npm ci --ignore-scripts` before
patching.

It supports every Store build whose exact `app.asar` profile is recorded in
`scripts/patch_windows.py`. The current profiles include the older
`26.810.7004.0` package and the newer `26.818.2441.0` package; the older
profile remains available so an existing checkout can be rebuilt against an
older installed app. An unrecorded official update stops by default. Review
its renderer anchors and add a deliberate profile before using it; do not use
`--allow-untested-source` as a permanent compatibility fix.

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
| `%LOCALAPPDATA%\Codex Relay\app` | Independent copied app and `routerctl.exe` |
| `%LOCALAPPDATA%\Codex Relay\Codex Relay.cmd` | Launcher with a dedicated Electron profile |
| `%USERPROFILE%\Desktop\Codex Relay.lnk` | Direct shortcut to the independent copy, with its dedicated profile |
| `%APPDATA%\Codex Relay` | Dedicated Electron profile for a fresh Relay installation |
| `%USERPROFILE%\.codex-mux` | Router state, isolated account homes, and local-control token |
| `%LOCALAPPDATA%\Codex Relay Updater\router-updater.exe` | External updater used by the in-app Update button; it is outside the replaceable app tree |

On an upgrade the staged copy is validated before any current Router copy is
stopped. The installer stops only processes whose executable path is below
`%LOCALAPPDATA%\Codex Relay`; it never matches the Store
app by process name. The prior stable Router app is moved to
`%USERPROFILE%\.codex-mux\backups\...`, while the Electron profile and all
Router/account state remain in place.

## In-app updates

The Windows build is distributed as source-only releases because the copied
OpenAI Store application and its `app.asar` are not redistributable. A user
therefore needs the normal local prerequisites for the first installation
(Python 3, Go, Node.js, and the official Store app), but does not need to type
a command for a later Router release:

1. The Router checks the project's HTTPS GitHub Releases manifest in the
   background. A missing manifest (for example, before the first published
   release) produces no banner and does not affect chat.
2. When a newer semantic version is available, a small **Update now** notice
   appears in the Router window.
3. Clicking it starts the external `router-updater.exe`. The helper validates
   the GitHub host, downloads the source archive, verifies its SHA-256, rejects
   unsafe archive paths, waits for the Router to exit, and runs the bundled
   installer with the current official Store source.
4. The installer stages and validates the new renderer profile before stopping
   only Router processes. It preserves `%APPDATA%` and `.codex-mux` state,
   repairs the Desktop shortcut, and launches the new Router automatically.

The update mechanism does not download or replace the Microsoft Store app,
does not send passwords, tokens, or account data, and never runs a URL or
executable path supplied by the renderer. If an update fails, the helper tries
to reopen the existing Router and writes diagnostic output to
`%LOCALAPPDATA%\Codex Relay Updater\update.log`.

This is an in-app **source-release updater**, not a standalone `Setup.exe`.
The first install still requires the documented toolchain; a future fully
redistributable installer would need a separately licensed, prebuilt runtime
and a code-signing/distribution decision.

## Windows Agent sandbox compatibility

Some Codex Windows releases fail their elevated Agent sandbox setup and show
`Unable to send message — Update Agent sandbox` (or a similar setup banner) on
both new and existing chats. The Router handles this without changing the
official Store installation:

- every Router-owned `app-server` and sandbox invocation receives the runtime
  override `windows.sandbox="unelevated"`;
- secondary homes under `%USERPROFILE%\.codex-mux\accounts` receive the same
  setting when their managed configuration is synchronized;
- the native `%USERPROFILE%\.codex\config.toml` is never rewritten, so the
  separate Microsoft Store app is not changed;
- the override is applied before the app-server starts, so existing chat
  ownership and quota failover continue to work as usual.

After upgrading an existing checkout, restart only the Router window. The
installer stops and relaunches processes below
`%LOCALAPPDATA%\Codex Relay`; it does not stop the Store app.

### Rename migration from 0.2.x

`Codex Relay` is the new public name for the project formerly displayed as
`Codex Subscription Router`. A `--force` installation stages the new Relay
copy before stopping either managed copy. It then stops only executables under
the exact old managed root, moves that root into
`%USERPROFILE%\.codex-mux\backups\...`, creates the new Relay shortcut, and
starts Relay. It does not target the Microsoft Store application.

The `~/.codex-mux` state root, account homes, control token, persistent thread
ownership, and any legacy Electron profile are retained. If
`%APPDATA%\Codex Subscription Router` exists but the new profile does not,
Relay deliberately uses the existing profile rather than moving it during an
update. This preserves browser/session history. A user may remove an old
desktop shortcut or backup only after confirming Relay works normally.

## Quota routing and model-capacity retry

Primary is the stored Router controller and the source of shared configuration;
it is **not** a "new chats only use Primary" lock. For a new `thread/start`,
Relay reads the short and longer usage windows from every enabled, connected
subscription, excludes every depleted account, and selects the least-used
eligible account. A small per-account dispatch counter breaks ties so accounts
with comparable quota alternate instead of permanently favoring the first one.

Once a chat has an owner, its follow-up turns remain sticky for context. If the
owner is depleted, Relay copies only that local rollout history into a target
account's isolated `sessions` directory, resumes the same thread, persists the
new owner, and forwards the turn. A chat that existed before Relay has no
stored owner; it starts at Primary so its history can be read, but follows this
same failover path when Primary has no quota.

`Selected model is at capacity. Please try a different model.` is treated as a
transient model-capacity condition, not a quota failure. Relay retries the
exact original `turn/start` payload — including its selected model — up to
three times with short exponential backoff on the same account. It never
silently changes the model or shifts the request to another subscription for
that error. A true upstream quota/rate-limit response still uses the normal
thread failover path.

## Account management in the app

Start the copied app with the Desktop shortcut (or the generated `.cmd`
launcher). It brings up the loopback-only control API along with the
multiplexer.

1. Open the profile menu at the bottom of the sidebar.
2. Review the Router usage section and connected subscription rows. The total
   shows the quota that is known; a row with missing data says **Updating
   quota…** or **Quota unavailable** instead of showing a misleading dash.
3. Select **Add another subscription**.
4. The Router opens the official ChatGPT sign-in in a private child window of
   the Router itself. It does not launch the default browser.
5. Every sign-in launch receives a newly generated, non-persistent Electron
   session. It therefore starts without cookies, local storage, or cache from
   another Router sign-in.
6. If that child window is closed before completion, choose **Open secure
   sign-in** to create a fresh one, or choose **Cancel sign-in** to discard the
   unfinished secondary account.
7. The confirmation polls only the local router. When the account connects,
   both windows close automatically and one non-blocking success notification
   appears.
8. If you select **Cancel sign-in**, the unfinished secondary account is
   stopped and removed. The Primary account and any connected subscription are
   never removed by this action. A previously abandoned `Waiting for sign-in`
   row also has a **Cancel sign-in** action.

### Choose Primary or remove a subscription

Open the same profile menu and select **Account settings**. This panel belongs
to Router, not to the Microsoft Store app:

- **Set as Primary** selects any connected ChatGPT subscription as the Router
  controller. The choice is persisted in `%USERPROFILE%\.codex-mux\state.json` and
  is not changed when the user switches accounts in the separate Codex app. A
  successful change also restarts only Router-owned Codex app-server sessions,
  then refreshes the panel; the Microsoft Store app is neither stopped nor
  modified.
- **Remove** is available for a connected secondary account after an explicit
  confirmation. Router requires another account to be selected as Primary first.
  If the account owns chats, the confirmation states how many assignments will
  be cleared; local source history is retained and the native `%USERPROFILE%\.codex`
  directory is never deleted.
- A pending sign-in keeps its **Cancel sign-in** action, so abandoned rows can be
  cleaned up without touching a connected identity.

Do not provide a password to the Router UI or `routerctl`. Credentials are
entered only on the official HTTPS `chatgpt.com` / `auth.openai.com` page in
the private child window; the Router does not collect, display, or exchange
OAuth tokens. That window has no Router preload or Node access, blocks
downloads and permission requests, and clears its temporary session data on
close.

The fresh-session guarantee applies to the Router child window's cookies and
web storage. It does not erase SSO, passkeys, or identity state managed by the
operating system or an external identity provider. Use the provider's **Use
another account** control when needed.

`routerctl.exe` remains available for local diagnostics (`routerctl list`), but
is not required to add or sign in to an account.

## Continuing existing chats through the Router

The Router includes existing local chats from the Primary account and each
connected secondary in its sidebar. Select the old chat in the **Codex Relay**
window and send the next message there. If the current owner has exhausted
quota, the Router copies that chat's local rollout history into the fallback
account's isolated `sessions` directory, resumes the same thread ID there, and
then forwards the turn. The source account's history file is left unchanged.
This also covers a legacy chat with no stored Router owner: Relay begins at
Primary to read history, then fails over instead of showing Primary's depleted
quota error.

This does not intercept messages sent in the separate Microsoft Store app. It
is safe to keep the Store app open, but the message that should be quota-routed
must be sent from the independent Router window.

## Profile, Plugins, and resets

The Windows copy provides the same account-selection behavior as the supported
macOS build for the following surfaces:

| Surface | Windows behavior |
| --- | --- |
| **Settings → Profile** | Starts with combined statistics and overlapping connected-account photos. Select a photo to reload that subscription's identity/statistics; select it again to return to the combined view. |
| **Settings → Plugins** | Shows a subscription picker. Plugin definitions and managed MCP configuration remain shared, while Apps, connection status, and OAuth RPCs are sent with the selected account scope. |
| **Usage / rate-limit resets** | Adds a subscription picker to the native reset sheet. It changes the displayed reset windows and fetches/consumes credits only for that account. |
| **Settings → Usage** | Reads the normal native Usage payload through Relay's token-protected local proxy for the controller account, with a connected-account fallback if that credential is unavailable. This avoids an unrelated Store browser session producing the generic “Oops” page. |

The menu's **Usage remaining** number is the sum of valid windows returned by
connected accounts. If one account's quota endpoint is temporarily unavailable,
the known total remains visible and the affected account is marked as updating;
Router never invents a zero/100% value from missing data.

The Settings Usage proxy returns only the normal Usage JSON that the native
page expects. It reads an isolated `auth.json` only in the Router process; no
OAuth access token or local control token is exposed to renderer JavaScript.
If the local request cannot complete, the version-pinned patch falls back to
the native request instead of replacing the entire page with an error.

Every Router account row uses the ChatGPT profile display name (then username
or email as a fallback), the Router label, and the plan label so subscriptions
are distinguishable. Its quota line shows the reset countdown for each
reported window from that same account, such as `Reset 5h: 1h 20m`. Hovering a
row shows the complete local reset timestamp. Missing reset metadata is shown
as unavailable; Router never guesses a reset time.

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
