# Changelog

All notable changes follow [Keep a Changelog](https://keepachangelog.com/) and
this project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

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

[Unreleased]: https://github.com/LightHaru/codex-subscription-router/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/LightHaru/codex-subscription-router/releases/tag/v0.1.0
