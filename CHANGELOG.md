# Changelog

All notable changes follow [Keep a Changelog](https://keepachangelog.com/) and
this project uses [Semantic Versioning](https://semver.org/).

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

[Unreleased]: https://github.com/LightHaru/codex-relay/compare/v0.3.6...HEAD
[0.3.6]: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.6
[0.3.5]: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.5
[0.3.4]: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.4
[0.3.3]: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.3
[0.3.2]: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.2
[0.3.1]: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.1
[0.3.0]: https://github.com/LightHaru/codex-relay/releases/tag/v0.3.0
[0.2.0]: https://github.com/LightHaru/codex-relay/releases/tag/v0.2.0
[0.1.0]: https://github.com/LightHaru/codex-relay/releases/tag/v0.1.0
