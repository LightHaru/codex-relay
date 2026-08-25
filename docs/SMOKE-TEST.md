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

Before any live session, run `go test ./internal/state ./internal/mux
./internal/control`. The deterministic fixtures must cover v1→v2 backup and
migration, persistent weighted deficits/reservations, low-water reserve, one
active turn per thread, all three policies, incremental prefix/mismatch copy,
handoff commit/rollback, crash recovery and side-effect fail-closed behavior.
These tests use temporary homes and fake app-server children; they must not
consume a real subscription.

- Connect at least two subscriptions and confirm photos, plans, masked emails,
  pooled usage, and loading states.
- Exercise Sticky, Balanced and Rotate; confirm worker changes happen only
  after a completed turn and Current Task Route shows the committed generation.
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
- Send one new chat and continue one existing chat; confirm the selected policy,
  canonical history handoff and quota failover operate in the Router window.
- Confirm a new task's first turn remains on the worker that created it, then
  verify Balanced/Rotate can change workers only after that turn completes and
  the canonical rollout checkpoint exists.
- Set an active Goal on a depleted worker, continue it through Relay, and verify
  the target returns the same objective with active status before the next
  turn completes. Never record the objective in routing diagnostics.
- Confirm the profile panel shows Relay Controller separately from Current Task
  Route and that a recovery-required task cannot start another turn until it is
  reviewed and acknowledged.
- Open **Routing details** in an idle task and during a disposable active turn.
  Verify current owner, active worker, last completed worker and next candidate
  remain distinct; the next row says preview and opening/refreshing the panel
  does not change scheduler cursor, dispatch count, deficits or reservations.
- Verify **Why this account** shows fixed selected/skipped reasons and quota
  freshness, and the recent timeline shows one stable row per logical
  reservation, selection, completion, rollback and handoff phase. Restart Relay
  only at an idle boundary and confirm the timeline and scheduler survive.
- Confirm quota attribution says waiting/unavailable until a newer snapshot is
  observed and reports a consuming worker/delta only for a measurable decrease.
  Do not infer consumption from dispatch count and do not use a reset credit.
- Keep the official Codex window open while checking that the inspector stays
  in the task composer flow, does not cover the sidebar/task, and remains usable
  with keyboard focus, a narrow window and common zoom levels.
- Open Usage & billing and at least two other Settings child pages. Confirm the
  pool panel exists only inside the Usage content column after its heading, the
  sidebar remains usable, and the inspector never replaces a Settings page.
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
