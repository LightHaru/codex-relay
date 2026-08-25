# Windows preview

The Windows port keeps the official Microsoft Store package untouched. Its
patcher copies the package's `app` directory to a user-writable location,
renames `resources\codex.exe` to `resources\codex.real.exe`, and puts the
compiled `codex-mux.exe` in its place. The copied Electron app therefore starts
the same per-account Codex app-server multiplexer used by the macOS build.

It injects a small DOM bridge into the copied renderer for the profile menu,
sign-in, and the in-flow multi-subscription panel inside Settings → Usage &
billing, then applies narrowly version-pinned patches to the reviewed Windows
renderer build for Profile statistics, Plugins account scope, the native
rate-limit reset sheet, and the native Settings Usage query. The patcher checks
every renderer anchor exactly once; an unfamiliar Microsoft Store update stops
instead of applying a rewrite to an unknown bundle.

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
`scripts/patch_windows.py`. The reviewed profiles include
`26.810.7004.0`, `26.818.2441.0`, `26.818.3698.0`, and the current
`26.818.4152.0`, `26.818.5229.0`, and `26.818.5345.0` packages;
older profiles remain available so an existing checkout can be rebuilt against
an older installed app. An unrecorded official update stops by default. For a
deliberate compatibility check, `--allow-untested-source` asks the patcher to
discover the same Profile, Plugins, reset, and login anchors structurally after
extracting the ASAR. It still rejects a missing or duplicated anchor and leaves
the existing Relay installation untouched. If discovery fails, wait for a
reviewed release instead of treating the flag as a permanent bypass.

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
| `%APPDATA%\Codex Relay\codex-home` | Relay-only primary credentials, sessions, and configuration |
| `%USERPROFILE%\.codex-mux` | Router state, isolated account homes, and local-control token; never the Store app's credential home |
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

When an older state file contains a secondary subscription whose home is still
`%USERPROFILE%\.codex`, the isolated startup moves that metadata entry to
`%USERPROFILE%\.codex-mux\accounts\<id>\codex-home` and leaves it disconnected
until it is signed in again. Router removes only that subscription's stale
thread-owner mappings; it never copies, deletes, or changes the official
Codex credential or history files.

## Quota routing and model-capacity retry

Primary is the stored Router controller and the source of shared Relay
configuration; it is **not** a "new chats only use Primary" lock. On Windows
it lives in Relay's dedicated `codex-home`, never in the Store app's
`%USERPROFILE%\\.codex`. For a new `thread/start`,
Relay reads fresh short and longer usage windows from every enabled, connected
subscription, excludes depleted/open-circuit accounts, protects a low-water
reserve and selects through persistent weighted-deficit round robin. Deficits,
cursor and active reservations survive restart.

Task routing follows Sticky, Balanced (default), or Rotate at completed-turn
boundaries. A worker change journals a canonical Relay Memory checkpoint,
incrementally materializes the verified rollout into the target account's
isolated `sessions` directory, resumes the same thread, and only then commits a
new owner generation. A chat that existed before Relay but is
already present in a Relay-owned home follows the same failover path. If its
old Router metadata still points at the native Store history, opening that
known chat in Relay first copies only the matching rollout into a Relay-owned
home; credentials/configuration and the native source remain untouched. A
Store-only chat is not scanned or imported automatically.

The profile menu shows **Relay Controller** separately from **Current Task
Route**. The normal task view also gets a small in-flow route badge near its
composer with the committed worker, generation, effective policy, next
candidate, latest handoff phase, and recovery state. SSE refreshes that badge
after a route or handoff event. Neither surface uses `position: fixed`, adds a
Settings navigation item, or replaces the native Settings shell.

In 0.4.1, **Routing details** expands that badge without leaving the task. The
compact row distinguishes the active worker, last completed worker and
policy-aware next-candidate preview. The native `<details>` panel adds current
owner, previous/last-quota workers, requested/effective policy, reservation,
quota freshness, fixed selected/skipped reasons, handoff generations, a
bounded timeline and pool summary. It remains in the composer flow at narrow
window sizes and normal zoom; it never enters the Settings sidebar or uses a
fixed overlay.

SSE terminal events refresh the panel. Quota failover, failed handoff,
recovery-required, all-depleted and safe policy downgrade may show one small
toast; the event ID is retained in session storage so a duplicate/replayed
event cannot show the same toast twice. Balanced/Rotate maintenance handoffs
remain visible in the timeline without default toast noise.

An exact reviewed installer manifest is required for these cross-account
handoffs. If the manifest is missing/unknown, the requested policy remains
visible but the effective policy is Sticky and no history copy/resume occurs.
No reviewed profile currently enables incomplete-turn resume; a post-side-
effect quota failure is shown as recovery-required instead of replayed.

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
4. The Router opens the official ChatGPT authorization URL in the default
   browser. This is the documented OAuth hand-off; the isolated Codex
   app-server child still owns the localhost callback for this subscription.
5. Complete sign-in in that browser. If it is already signed in to another
   ChatGPT account, choose **Use another account** there.
6. If the browser page is closed before completion, choose **Open secure
   sign-in** to open it again, or choose **Cancel sign-in** to discard the
   unfinished secondary account.
7. The confirmation polls only the local Router. When the account connects,
   the confirmation closes automatically and one non-blocking success
   notification appears. A web OAuth error leaves the row available for retry.
8. If you select **Cancel sign-in**, the unfinished secondary account is
   stopped and removed. The Primary account and any connected subscription are
   never removed by this action. A previously abandoned `Waiting for sign-in`
   row is restored after a Relay restart only when Relay has a persisted,
   intentionally started login flow; a disconnected stale row is shown as
   **Not connected** and can be removed without pretending a browser flow is
   active.

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
- Each connected account also shows a native-style **Usage limit resets** card.
  An available **Use reset** button calls only that subscription's reset
  endpoint, refreshes the card, and never consumes another account's credit.

Do not provide a password to the Router UI or `routerctl`. Credentials are
entered only on the official HTTPS `chatgpt.com` / `auth.openai.com` page in
the default browser; the Router does not collect, display, or exchange OAuth
tokens. The default browser may reuse its existing SSO, passkeys, or identity
state; use the provider's **Use another account** control when needed.

`routerctl.exe` remains available for local diagnostics (`routerctl list`), but
is not required to add or sign in to an account.

## Continuing existing chats through the Router

The Router includes existing local chats owned by Relay's dedicated Primary
home and each connected secondary in its sidebar. Select the old chat in the
**Codex Relay** window and send the next message there. If the current owner
has exhausted quota, the Router copies that chat's local rollout history into
the fallback account's isolated `sessions` (or `archived_sessions`) directory,
resumes the same thread ID there, and then forwards the turn. The source
account's history file is left unchanged. This works with both absolute rollout
paths and the `CODEX_HOME`-relative paths emitted by different app-server
versions.

Chats created in the Store Codex app before data separation remain native to
that app unless the user explicitly opens the same known chat in Relay. For
that requested chat only, Relay can read the old rollout and copy one file into
the selected Relay account's isolated history store. It never reads native
credentials/configuration, starts a child on the Store home, or edits/deletes
the source rollout. Store-only history is not scanned or imported in bulk.

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
| **Settings → Usage & billing** | Keeps the native page shell, sidebar, and billing controls. Adds an in-flow **Shared quota pool** panel inside the page's content column; it is not a sidebar item or overlay. The additive pool is primary and isolated account billing/reset details are collapsed under Worker diagnostics. |

The menu's **Shared quota pool** number is additive: five full subscriptions
show `500% / 500%`, while 155 confirmed percentage points show `155% / 500%`.
If one account's quota endpoint is temporarily unavailable,
the known total remains visible and the affected account is marked as updating;
Router never invents a zero/100% value from missing data.

The Settings Usage proxy still returns the normal single-account Usage JSON
that the native page expects, while the in-flow panel calls the separate,
token-protected `/v1/usage/all` route. That route fetches one payload per
enabled subscription concurrently, keeps partial failures visible, and never
merges account-scoped billing actions into a fabricated total. It reads
isolated `auth.json` files only in the Router process; no OAuth access token or
local control token is exposed to renderer JavaScript. If one account's Usage
request fails, its card says **Unavailable** while the other accounts remain
visible. If the local bridge is unavailable, the patched native request fails
closed instead of falling through to the official Codex account.

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
been ported. The multiplexer, state-v2 task routes, canonical Relay Memory, and
per-account Codex homes are shared core code and are covered by the Go test
suite. Keep each
connected subscription compliant with its governing terms.
