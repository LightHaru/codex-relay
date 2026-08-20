# Signed-app smoke test

Complete this checklist on the exact official build recorded in
`docs/COMPATIBILITY.md` before publishing a release draft. Use a team-backed
signature and reuse the same Apple team as the previous installed build.

## Build and identity

- Confirm the patcher reports the expected version, build, and ASAR SHA-256.
- Verify the official `/Applications/ChatGPT.app` is unchanged.
- Verify the app and every nested Computer Use application with
  `codesign --verify --deep --strict`.
- Confirm the installed app and helper report the intended bundle IDs and the
  same `TeamIdentifier`.

## Accounts and routing

- Connect at least two subscriptions and confirm photos, plans, masked emails,
  pooled usage, and loading states.
- Start chats until each account has received one; confirm every follow-up stays
  on its original account.
- Spoof one depleted account and confirm the thread continues on an account with
  quota. Spoof all accounts depleted and confirm the combined alert.
- Open a quota-triggered reset sheet, switch subscriptions, consume a reset, and
  confirm only the selected account changes.

## Settings and plugins

- Confirm Profile opens in the combined state, uses 20 px avatar overlap, and
  toggles between combined and per-account statistics.
- In Settings → Plugins, select each subscription and verify Apps, MCP status,
  and MCP OAuth login reflect that account while installed definitions remain
  shared.

## Appshots and Computer Use

- In System Settings, grant Accessibility to Codex Relay and Screen & System
  Audio Recording to Codex Relay Computer Use.
  Quit and reopen when macOS asks.
- Capture an Appshot from the attachment menu and with the Command-key shortcut.
- Run a Computer Use task and confirm the native helper performs the action
  without falling back to `osascript`.
- Rebuild once with the same signing team and confirm existing permissions still
  work without adding duplicate permission rows.

Record the tested commit, macOS version, signing team ID, and any deviations in
the release draft before publishing it.

## Windows Router smoke test

Run this additional checklist on each recorded Windows `app.asar` profile:

- Confirm the official Store `app.asar` hash matches the selected profile and
  remains unchanged after patching.
- Launch both the official Store app and the independent Router; verify the
  Router uses its own Desktop shortcut, `%APPDATA%` profile, and
  `%LOCALAPPDATA%\Codex Relay` path.
- Confirm `http://127.0.0.1:48123/v1/health` is healthy and that the account
  menu shows the connected subscriptions without exposing tokens.
- Send one new chat and continue one existing chat; confirm quota failover and
  sticky ownership operate in the Router window.
- Check Profile, Plugins account selection, the rate-limit reset picker, and
  the private official browser sign-in flow.
- Verify `codex.real.exe` receives `windows.sandbox="unelevated"` while the
  official Store process remains untouched.
- Build `router-updater.exe`, confirm it is outside the managed app, and test a
  staged source update in a disposable release fixture. Verify hash mismatch,
  unsafe ZIP paths, and an unapproved host are rejected.
- After a published release contains `windows-update.json`, click **Update
  now** and confirm the Router closes, stages the release, repairs the
  shortcut, and reopens without losing `.codex-mux` state.
