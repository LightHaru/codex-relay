# Changelog

All notable changes follow [Keep a Changelog](https://keepachangelog.com/) and
this project uses [Semantic Versioning](https://semver.org/).

## [0.3.12] - 2026-08-22

### Added

- Windows Settings → Usage & billing now keeps the official native page shell
  and mounts an in-flow **All connected subscriptions** panel inside its
  content column. It does not add a sidebar item, replace the Settings shell,
  or use a fixed overlay.
- Every account card shows its own plan, credits, quota windows, reset times,
  reset-credit list, and bounded billing payload details. A failed account is
  marked **Unavailable** without hiding healthy subscriptions.

### Fixed

- The current Windows Store renderer routes Usage, reset-credit queries, reset
  mutations, selected usage windows, and the native reset picker through the
  isolated Relay account bridge.
- A Relay Usage bridge failure now fails closed instead of falling through to
  the official Codex browser session, preventing native/Relay account mixing.

Release: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.12

## [0.3.11] - 2026-08-22

### Fixed

- Isolated-mode startup now migrates every legacy subscription entry that
  points at the official `%USERPROFILE%\.codex` home, not only the old
  `Primary` row. The official credential and history files remain untouched;
  the migrated Relay row requires its own sign-in and stale Router ownership
  metadata is cleared.

Release: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.11

## [0.3.10] - 2026-08-22

### Fixed

- Account settings now render native-style **Usage limit resets** cards with a
  scoped **Use reset** action, refresh the selected account after redemption,
  and never consume another subscription's credit.
- Pending browser sign-in intent is persisted per secondary subscription and
  restored after a Relay restart. Disconnected stale accounts no longer show a
  false **Waiting for sign-in** row or cancellation action.
- Unrecorded Windows Store bundles can opt into structural renderer-anchor
  discovery; minifier aliases are inferred only when every feature anchor is
  unique, otherwise the installer fails closed without touching the old copy.

Release: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.10

## [0.3.9] - 2026-08-21

### Fixed

- Windows Relay now uses its own primary `CODEX_HOME` and file-backed
  credential store under `%APPDATA%\Codex Relay\codex-home`; it no longer
  inherits the official Store app's `%USERPROFILE%\.codex` account. Existing
  Relay state is migrated without copying or deleting native credentials, and
  old native-chat owner mappings are dropped from Relay only.
- Quota failover now migrates rollouts returned as absolute or
  `CODEX_HOME`-relative paths by current and older Codex app-server builds.
- A turn that is accepted and then rejected asynchronously by an exhausted
  subscription is now retried on the next available account without leaking
  the native quota error to the desktop client.
- The native Usage bridge now prefers a connected subscription with remaining
  capacity, so an exhausted Relay Primary no longer shows a false
  out-of-messages banner while another subscription can continue the chat.
- Opening an older chat now imports its single legacy rollout into the selected
  Relay account before `thread/resume`, so the chat no longer depends on the
  official Store app's history path after the first successful open.
- Archived chats under `archived_sessions` can now move to another connected
  subscription instead of failing with an "outside the source sessions"
  error.
- Windows extended-length rollout paths (`\\?\C:\...` and extended UNC paths)
  are normalized before the source-history boundary check, so valid chats from
  the current Codex desktop build can be migrated safely.
- Legacy chats without a Router owner mapping are resolved by scanning managed
  rollout history, and startup restores the configured Relay `Primary` account
  metadata so Relay-owned chats can resume after routing is enabled.
- History migration remains restricted to Codex-managed history directories,
  validates symlink resolution, and never modifies the source rollout.
- New chats now use strict round-robin dispatch across connected subscriptions
  with known remaining capacity; quota percentage no longer pins every chat to
  the account with the lowest usage.
- Relay account initialization is concurrent, so a disconnected subscription
  cannot block the desktop handshake for the rest of the pool.
- The native Windows **Usage & billing** page is left unchanged. Account
  settings now show **Usage limit resets** and the full reset-credit details for
  each connected subscription instead.
- Account settings now present each available reset as a native-style card with
  a scoped **Use reset** action; the result refreshes only that subscription's
  balance and never consumes another account's credit.
- Browser sign-in intent is persisted per secondary subscription. Relay restores
  a real unfinished flow after restart, while disconnected stale accounts no
  longer masquerade as **Waiting for sign-in** rows.
- Windows upgrades can structurally discover minifier aliases for an unrecorded
  Store bundle when explicitly requested with `--allow-untested-source`; every
  renderer anchor is still required exactly once and ambiguous updates fail
  closed.

Release: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.9

## [0.3.8] - 2026-08-21

### Fixed

- Windows Usage & billing aggregation is now mounted only inside the native
  Usage subpage, directly after its title and description and before **Your
  plan**. It no longer occupies the Settings shell, sidebar, or neighboring
  navigation content.
- Existing Relay Usage cards are relocated to the correct title anchor when a
  renderer rerenders the Settings page, preventing duplicate or full-page
  overlays.

Release: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.8

## [0.3.7] - 2026-08-21

### Added

- Windows Settings → Usage & billing now shows an **All connected
  subscriptions** panel. Every account is fetched through its isolated Relay
  credential and displayed with its own plan, credits, rate-limit windows,
  reset metadata, spend controls, reset-credit counts, Code Review limits, and
  future Usage fields returned by ChatGPT.
- Added the token-protected `/v1/usage/all` control route. It fetches accounts
  independently, keeps partial failures visible, and never fabricates a
  combined billing balance or redirects account-specific billing actions.
- Added Go and Windows renderer tests for multi-account Usage rendering,
  partial credential failures, unconnected subscriptions, and preservation of
  new upstream Usage fields.
- The Usage panel now selects the smallest content-column ancestor, keeping it
  inside the native Codex settings page instead of inserting it beside the
  sidebar.

### Documentation

- Updated the English and Vietnamese Usage & billing instructions, Windows
  deployment notes, and architecture documentation for the multi-account
  dashboard and its account-scoped billing safety rules.

Release: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.7

## [0.3.6] - 2026-08-20

### Fixed

- Windows **Add another subscription** now hands the official ChatGPT OAuth
  link to the user's default browser. This avoids embedded Electron/Cloudflare
  callback failures while the isolated Codex app-server continues to own the
  localhost callback and credential storage.
- Login flow identifiers now use a strict opaque format so older Electron/Node
  builds cannot reject a valid callback session before polling the Relay.
- The sign-in dialog accepts both `authUrl` and `auth_url` response spellings
  for compatibility with older app-server builds, and keeps the unfinished
  account available for a retry when the browser reports an OAuth error.

## [0.3.5] - 2026-08-20

### Fixed

- Windows Settings → Usage no longer crashes when the reset-account picker
  subscribes to quota state. The renderer patch now calls the React namespace
  belonging to the reviewed `26.818.2441.0` bundle instead of an unrelated
  MCP schema symbol.
- The Windows updater preload now imports Electron's `contextBridge` and
  `ipcRenderer` explicitly, so the in-app update bridge loads in Electron's
  sandboxed preload environment.

## [0.3.4] - 2026-08-20

### Fixed

- Windows Settings → Usage now authorizes Chromium's Private Network Access
  preflight for the trusted packaged renderer. This completes the loopback
  bridge fix for secure `app://-` builds, which could still show the generic
  **“Oops, an error has occurred”** page after the 0.3.3 CORS fix.

## [0.3.3] - 2026-08-20

### Fixed

- Windows Settings → Usage now works from the packaged renderer. The local
  Usage bridge accepts the opaque `null`/`file://` Origin emitted by Electron's
  `file://` page, while retaining an explicit allowlist and token protection.
  This removes the generic **“Oops, an error has occurred”** screen caused by
  the browser rejecting the local request during CORS preflight.

## [0.3.2] - 2026-08-20

### Fixed

- Windows source-release updates now pass the extracted checkout correctly to
  the local installer. The installer recognizes the old 0.3.1 updater's
  checkout-shaped `-Source` argument instead of treating the repository as the
  official ChatGPT app, so **Update now** can complete without a manual repair.
- The one-command bootstrap and future updater helpers no longer pass an
  extracted source checkout as an official Store app path.

## [0.3.1] - 2026-08-20

### Added

- Fair-share selection for new chats across all enabled, connected
  subscriptions with capacity. The selector favors lower current quota use and
  alternates comparable accounts instead of locking new work to Primary.
- Sticky quota failover coverage for chats created before Relay: an unassigned
  old chat starts at Primary to read history, then migrates to an account with
  capacity instead of returning Primary's depleted-quota error.
- Bounded same-account retries for `Selected model is at capacity` that retain
  the exact original selected model and request payload.
- A token-protected Settings → Usage proxy that reads the normal native usage
  payload with isolated account credentials, avoiding a mismatched Store browser
  session; the renderer falls back safely to its native request if unavailable.
- A reviewed one-command Windows bootstrap asset. It validates the published
  source archive URL, SHA-256, and archive paths before running the existing
  staged local installer.

### Changed

- The Windows renderer patcher now requires one additional exact Usage anchor
  for each supported Store `app.asar` profile and fails closed if it changes.
- Updated Vietnamese and English user documentation, Windows deployment notes,
  release instructions, and contributor policy for fair-share routing, old
  chat failover, exact-model retry, Usage recovery, and the one-command first
  install.

## [0.3.0] - 2026-08-20

### Changed

- Renamed the user-facing product and canonical repository to **Codex Relay**.
  The internal `codex-mux` state and the update manifest product identifier are
  intentionally retained so installed 0.2.x copies can update without losing
  account state, thread ownership, or Electron profile data.
- Windows upgrades stage the new Relay copy first, stop only the old managed
  Router root, and move that legacy copy into `~/.codex-mux/backups` before
  launching `%LOCALAPPDATA%\Codex Relay\app`.
- Reworked the Vietnamese and English installation/update guides around the
  direct `Codex Relay` shortcut, automatic in-app source updates, migration,
  compatibility checks, and recovery steps.

## [0.2.0] - 2026-08-20

### Added

- One-command installer with safe source updates, prerequisite checks, signed
  rebuilds, recoverable upgrades, and automatic launch.
- Reset-aware routing that prioritizes weekly quota at risk of expiring and
  gives a bounded boost to subscriptions with banked usage resets.
- Windows x64 preview patcher that creates an independent copy of the Microsoft
  Store app, routes it through `codex-mux.exe`, and leaves the official package
  unchanged.
- Windows profile-menu bridge with pooled usage, connected subscription rows,
  an in-app official browser **Add another subscription** flow, and a
  double-click local-source installer that creates a direct Desktop shortcut.
- Windows browser-login completion closes its dialog automatically and shows
  one non-blocking connected notification, with stale-poll protection.
- Pending Windows sign-ins can be cancelled safely: the Router cancels the
  official child flow, preserves an account that completed in the cancellation
  race, and removes only an unconnected secondary account and its isolated
  home.
- Primary-first routing for new threads, plus short-window capacity checks so
  an available secondary is selected before an avoidable quota failure.
- Version-pinned Windows renderer ports for combined/selected Profile
  statistics, account-scoped Plugins Apps/MCP/OAuth RPCs, and per-account
  rate-limit reset selection.
- Focused Windows bridge and renderer-anchor regression checks.
- Windows compatibility profile for the newer Microsoft Store `26.818.2441.0`
  bundle while retaining the previously reviewed `26.810.7004.0` profile.
- Hash-verified in-app source-release updates with an external Windows helper,
  safe staging, Router-only restart, and automatic relaunch.

## [0.1.0] - 2026-08-15

### Added

- Multi-subscription routing with quota-aware balancing and sticky threads.
- Account isolation, device-code sign-in, pooled usage, and quota failover.
- Native account menu, masked emails, plan labels, and profile photos.
- Combined Profile statistics with per-account selection.
- Account-scoped Apps and MCP connection state in Settings → Plugins.
- Per-account rate-limit reset selection and pooled depletion handling.
- Independently signed Appshots and Computer Use support.
- Fail-closed upstream compatibility checks and deepest-first nested helper signing.
- Loopback-only, token-authenticated diagnostic UI states.
- Source-only CI, draft release automation, security documentation, and smoke tests.

[Unreleased]: https://github.com/LightHaru/codex-relay/compare/v0.3.12...HEAD
[0.3.12]: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.12
[0.3.11]: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.11
[0.3.10]: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.10
[0.3.9]: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.9
[0.3.8]: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.8
[0.3.7]: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.7
[0.3.6]: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.6
[0.3.5]: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.5
[0.3.4]: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.4
[0.3.3]: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.3
[0.3.2]: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.2
[0.3.1]: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.1
[0.3.0]: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.0
[0.2.0]: https://github.com/LightHaru/codex-relay/releases/tag/v0.2.0
[0.1.0]: https://github.com/LightHaru/codex-relay/releases/tag/v0.1.0
