# Windows smoke test

Run this checklist on a disposable Relay profile and the exact Store bundle
selected by `docs/COMPATIBILITY.md`. Keep the official Codex window open. Do not
use reset credits or send prompts that can mutate user files.

## Build gates

- `npm ci --ignore-scripts`, `npm run check`, `npm run release:check` and
  `git diff --check` exit zero.
- `scripts/probe_unified_provider.py` reports one authority app-server, one
  Responses request, SSE enabled and no source credential read by the authority.
- The patcher verifies the exact `app.asar` hash and all anchors exactly once.
- The official Store app/profile/home hash is unchanged after staging Relay.

## Process and identity

- Launch the **Codex Relay** shortcut beside the official Codex app.
- Verify Relay uses `%APPDATA%\\Codex Relay\\codex-home` and
  `%USERPROFILE%\\.codex-mux`; it does not use `%USERPROFILE%\\.codex` as its
  writable home.
- Verify only Relay-owned children receive source homes; the official process
  remains open and untouched.
- Initialize the desktop client once and confirm the task authority is the only
  child receiving ordinary thread/turn/Goal/tool traffic.

## Pool behavior

- Add four disposable/authorized sources through the UI; confirm each has an
  isolated home and no shared credential file.
- In Usage & billing, confirm there is one **Codex Relay Pool** card in the
  content column. The sidebar and every other Settings child remain usable.
- Send a short task and verify repeated turns stay on the first source while it
  is usable; no round-robin or pre-emptive balancing occurs.
- With a deterministic fixture or authorized live rejection, verify A→B→C→D
  uses the same session/thread/logical turn and request body before output.
- Verify no public “Move chat”, worker name, source ID or account owner appears.
- Mark all sources depleted in a fixture and confirm one sanitized pool error,
  no fake completed turn and no active lease left behind.
- Induce/observe a late quota event only in a safe fixture: output is not
  replayed, source is excluded for later turns and the task is recovery-required.

## Chat, Goal and history

- Start a new chat, continue it for multiple turns and restart Relay at an idle
  boundary; the same task and canonical generation resume.
- Resume an existing and an archived chat from a canonical checkpoint. Verify
  containment, SHA-256 and size; no source path is outside managed sessions.
- Run the Goal fixture with no tool/command/file side effects. Confirm objective,
  status and remaining budget remain on the same task after pre-output failover.
- Exercise tool/approval bindings. Any post-side-effect quota failure must stop
  automatic replay and require recovery review.

## UI and account management

- Open Profile, Plugins and reset-credit surfaces; source selection is available
  only in management/settings flows, not in the task route.
- Complete Add another subscription in the normal browser, close the code
  notice after success, and verify the connected row updates without callback
  errors. Never paste a password into Relay.
- Check narrow window, keyboard focus, zoom and long labels. No fixed overlay may
  cover the sidebar or native Settings shell.

## Update rehearsal

- In a disposable release fixture, reject unapproved hosts, unsafe archive paths
  and SHA-256 mismatches.
- With a published, reviewed manifest, click **Update now**. Verify the updater
  is outside the app directory, waits only for Relay-owned processes, preserves
  pool state/history/shortcut and restarts Relay. Do not target `ChatGPT.exe` by
  name and do not close the official app.

Record the commit, Store version/hash, compatibility profile, sanitized pool and
lease revisions, transition order, final command and exit code. If live quota
transition or partial-stream continuation was not observed, record `LIVE PENDING`
or `NOT PROVEN`; never infer it from a UI percentage.
