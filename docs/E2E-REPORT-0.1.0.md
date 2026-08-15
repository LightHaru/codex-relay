# v0.1.0 signed-app E2E report

Tested on 15 August 2026 against source commit `ab51ae6`.

| Item | Tested value |
| --- | --- |
| macOS | 27.0 (`26A5406e`) |
| Architecture | Apple silicon (`arm64`) |
| Official ChatGPT version | `26.803.61601` |
| Official bundle build | `6396` |
| Official `app.asar` SHA-256 | `d5a44ed9e2f1db5f81dbbe85408aed256f3203c5b16f00817bb9d7cd941343cf` |
| Signing team | `X522N436T7` |

## Passed

- The signed app launched normally after the final rebuild and remained running.
- Three subscriptions loaded with photos, plan labels, masked emails, pooled
  usage, and an explicit loading state.
- The combined Profile view loaded, used 20 px avatar overlap, hid combined
  identity text, and toggled to one subscription's identity and statistics.
- The Plugins account picker switched between the primary and secondary
  subscription and updated the displayed connection state.
- A depleted primary subscription automatically routed a live chat to a
  subscription with quota; the thread displayed the selected subscription.
- An all-depleted test response produced the combined depletion alert.
- The rate-limit reset sheet displayed per-account reset balances, selected a
  secondary subscription, and reached its native confirmation state.
- A live Computer Use request opened Calculator through the native controller.
  The turn completed without an `osascript` fallback.
- Appshots permissions were recognized, the attachment-menu action opened the
  native window picker, and the picker displayed live desktop windows.
- The app, helper, every nested Computer Use app, and every nested executable
  passed strict code-signature verification under the same team. The helper's
  upstream Sparkle update keys were absent.
- Go tests and vet, JavaScript and Python syntax checks, release metadata checks,
  staged-tree checks, and the high-severity dependency audit passed. The audit
  reported zero vulnerabilities.

## Harness limitation

The automated controller opened and visually verified the native Appshots
window picker, which exercises the signed Screen Recording/TCC path. The
external desktop-control harness then stopped responding before it could click
the final window tile, so attachment insertion was not machine-driven in this
run. Perform that final click once manually before publishing the tag.
