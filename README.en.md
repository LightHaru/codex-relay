# Codex Relay

> Vietnamese: [README.md](README.md)

Codex Relay is an independent Windows router for Codex. In **Unified Pool Gateway** mode, Codex sees one Relay API, one Relay identity, one task authority, and one quota pool. Additional ChatGPT/Codex accounts are hidden credential sources; they are never exposed as public chats, threads, or workers.

> [!IMPORTANT]
> Relay is not an OpenAI product and does not merge subscriptions into a new billing plan. `500%` means normalized routing headroom across five 100% sources; upstream usage is still charged to the real credential in use.

> [!WARNING]
> Connect only accounts you are authorized to use and follow their terms. Do not use Relay to bypass access controls, limits, or safety systems. Never put tokens, `auth.json`, or conversation data in issues, logs, or pull requests.

## Model

```text
Codex desktop
     │  one API / one identity / one session
     ▼
Relay Unified Pool Gateway
     │  one logical task authority
      ▼
PoolQuotaLedger + QuotaAwarePoolScheduler
     ├── credential source A (hidden)
     ├── credential source B (hidden)
     ├── credential source C (hidden)
     └── credential source D (hidden)
```

Every request enters the same Relay API and the same task authority. The scheduler
reads both quota windows (5-hour and weekly), removes depleted sources, and
fair-shares requests across eligible sources with a persistent cursor. If upstream
still rejects one source, Relay marks it `DEPLETED` and retries the exact body,
session, thread, turn, and connection through the next source inside the **same
logical request**. It never creates a worker, task, thread, or “move chat” event
when changing a hidden credential.

### Guarantees and safety boundary

- One `RelayGatewayWorker` owns the thread, session, Goal, tools/approvals, canonical history, and output stream.
- Every logical turn has one `PoolLease`; pool revisions and source transitions are atomic, idempotent, and heartbeat-protected.
- A→B→C→D retry is allowed only before visible output or a side effect. Body, model, thread, and Goal remain unchanged.
- Network failures, HTTP 502/503/504, and SSE streams that end before `response.completed` are transport failures, not quota evidence. Before output the Gateway rotates sources; the failed source only enters a temporary `suspect/cooldown` state and keeps its credentials/quota intact.
- On app or machine restart, an old lease that emitted no output/tool event is released before the Gateway accepts requests. The same request ID can continue without a false `409`; concurrent duplicates join one upstream flight instead of running twice.
- If quota is rejected after output, a command, file change, or tool side effect, Relay does not replay. The turn becomes `recovery-required`; the source is excluded from later turns and the result must be reviewed.
- If an upstream pauses or closes after `response.output_item.done` without a terminal, Relay gives `response.completed` a very short grace window; if it is still absent, Relay emits a standards-shaped recovery terminal and the mux shows an actionable Relay reason instead of `stream closed before response.completed`.
- When every source is depleted, Relay returns one pool-level error. Network, timeout, and model errors are not guessed to be quota exhaustion.

## Quick Windows install

After an official release publishes its manifest, PowerShell can install it with:

```powershell
irm https://github.com/LightHaru/codex-relay/releases/latest/download/install-codex-relay.ps1 | iex
```

The bootstrap validates the host, manifest, and SHA-256; it does not silently install Python/Go/Node or modify the official Codex app.

From source:

```powershell
git clone https://github.com/LightHaru/codex-relay.git
cd codex-relay
```

Open the checkout in Explorer and double-click `Install Codex Relay.cmd`. The installer creates a **Codex Relay** shortcut, an independent Electron profile, and an isolated `codex-home`.

Requirements: Windows x64, the Microsoft Store Codex/ChatGPT app, Python 3, Go 1.26+, Node.js 22.12+, and npm. Relay does not import the account in `%USERPROFILE%\\.codex`.

| Path | Purpose |
| --- | --- |
| `%LOCALAPPDATA%\\Codex Relay\\app` | Independent Relay app and `codex.real.exe` |
| `%APPDATA%\\Codex Relay\\codex-home` | Private Relay host home (task authority only when selected) |
| `%USERPROFILE%\\.codex-mux` | Pool ledger, source homes, canonical history, backups |
| `%LOCALAPPDATA%\\Codex Relay Updater` | Updater outside the app directory |

The official Codex app keeps its own profile, credentials, history, and processes.

## Sign-in and daily use

Open **Codex Relay**, open the account menu, and choose **Add another subscription**. Browser login is performed by the selected official app-server; Relay stores only pending-login state and observes completion. Each source has its own home and `auth.json`; never copy it between apps.

In Settings → **Usage & billing**, Relay keeps the native child page and sidebar, then inserts exactly one in-flow panel in the content column. It contains the **Codex Relay Pool** summary plus one card per connected account: short/long quota, reset time, plan, credits, reset credits, and a concrete error when that source cannot be read. This is billing information inside Settings; task routes and public status still hide source identities.

The name/photo shown in the menu is the **Relay authority** (for example, Agent Aira), the one public identity of the single logical worker; it does not prove that every request is pinned to that credential. When the authority is depleted, the Gateway marks that source exhausted and retries the same request through the next pool source while thread, session, Goal, and UI remain one continuous flow. The actual source is used only for hidden routing and is visible in the Usage & billing panel.

Task notifications are pool-level, such as “Relay Pool continued the session” or “Relay Pool has no usable quota”. They never say “Move chat to Subscription 2”, name a worker, or expose an account owner.

## Local API

The control API binds to `127.0.0.1` and requires the installation token. The Gateway model API is `POST /v1/responses`, also bearer-protected on loopback; credential headers are rebuilt from the selected source and source cookies/tokens are not forwarded.

In contract v2, `GET /v1/router/status` and `GET /v1/thread-route` expose only the `relay`/`Codex Relay Pool` identity, aggregate pool state, and necessary recovery state. Detailed source metadata belongs to token-protected account-management endpoints.

## Compatibility and updates

The patcher is anchor-based for reviewed Store bundles and fails closed for an unknown bundle. Router core uses `wire_api = responses` with a local custom provider. Run compatibility gates after an official Codex update; never weaken anchor checks to force a build. If an app-server profile is not reviewed, Relay keeps one public API/identity but enters safe mode and disables credential failover until that profile is tested.

The updater is a separate executable outside the app directory. When a valid release publishes `windows-update.json`, **Update now** downloads the source archive, verifies SHA-256 and archive paths, waits for the correct Relay process to exit, installs, and restarts. Pool state, canonical history, shortcut, and credential homes remain in place. It cannot close or modify the official Codex app.

## Testing

```powershell
npm ci --ignore-scripts
npm run check
npm run release:check
git diff --check
```

The suite covers state-v3 migration/rollback, quota-aware fair-share pool ledger,
CAS, heartbeat/crash recovery, same-body retry, early-stream failover,
late-stream no-replay, sanitization, in-flow Usage & billing UI, and a real
installed `codex.real.exe` against a fake upstream. That E2E proves one task
authority and A→B→C→D source rotation inside one session; it is not live quota
evidence.

Real-account E2E must be run separately with short prompts, authorized accounts, and a sanitized report. Record observed transitions, lease/pool revision, canonical hash/size, Goal continuity, duplicate/lost-output checks, the final exit code, and that the official Codex remained open. If live evidence does not exist, record `LIVE PENDING`, never `PASS`.

See [docs/TEST-MATRIX.md](docs/TEST-MATRIX.md), [docs/SMOKE-TEST.md](docs/SMOKE-TEST.md), [docs/COMPATIBILITY.md](docs/COMPATIBILITY.md), and [docs/SECURITY-MODEL.md](docs/SECURITY-MODEL.md).

## Contributing and license

This is a community project. Open issues with token/PII-scrubbed logs, the Store build, reproduction steps, and the final exit code. Do not attach `app.asar`, executables, `auth.json`, or user history.

MIT License — see [LICENSE](LICENSE). Codex Relay builds on the original ideas and source of [LightHaru/codex-subscription-router](https://github.com/LightHaru/codex-subscription-router); thank you to the original author and earlier contributors. This project is not affiliated with or endorsed by OpenAI and does not distribute OpenAI binaries.
