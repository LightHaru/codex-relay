# Compatibility

The patcher is intentionally tied to known ChatGPT desktop bundle structures.
It verifies every modified renderer, main-process, and native binary anchor and
stops instead of applying a partial patch.

## Release 0.3.5

This hotfix keeps the reviewed Windows `26.818.2441.0` renderer profile but
corrects the React namespace used by the account-scoped reset picker. It also
fixes the sandboxed updater preload so the in-app update bridge is available to
the renderer. The full Settings → Usage flow was exercised against the live
Relay app with the existing three-subscription profile; all local bridge
requests returned 200 and the native Usage page rendered without its generic
error screen.

## Release 0.3.6

This release repairs Windows **Add another subscription** sign-in for current
and older supported Store bundles. The renderer now validates the official
ChatGPT authorization URL and hands it to the user's normal browser through
Electron's `shell.openExternal`. The isolated Codex app-server remains the
only component that owns the localhost callback, exchanges the authorization
code, and stores the subscription credentials. No embedded Electron login
window is created, so Cloudflare, passkeys, and existing browser SSO can use
their supported flow. The Relay keeps polling the scoped account and closes
the confirmation with a success toast only after the account is connected.

## Release 0.3.4

This hotfix completes the Windows Settings → Usage loopback fix by returning
`Access-Control-Allow-Private-Network: true` for the explicit packaged
renderer allowlist. Chromium's Private Network Access check can otherwise
reject a secure `app://-` renderer before the normal CORS response is read.

## Release 0.3.3

This hotfix keeps the reviewed Windows renderer profiles unchanged and fixes
the local Settings → Usage bridge for the packaged `file://` renderer. The
loopback service now answers the browser's `Origin: null` CORS preflight (and
the small compatibility set used by older Electron builds) without opening
the API to arbitrary web origins. The token requirement remains unchanged.

## Release 0.3.1

This release keeps both reviewed Windows Store `app.asar` profiles and adds one
exact-one renderer anchor per profile for the native Settings → Usage query.
The patch calls a local, token-protected Relay bridge first and falls back to
the original renderer request only when that bridge cannot return a payload.
The Release test suite exercises the exact current Store renderer in a temporary
extraction before publication.

It also changes new-thread routing from Primary-first to quota fair-share,
preserves sticky/failover behavior for old and assigned chats, and recognizes
the selected-model capacity response for an exact-model same-account retry.
These mux changes are independent of the upstream renderer profile and are
covered by deterministic local JSON-RPC integration tests.

## Release 0.3.2

This is a Windows updater hotfix. The installer accepts the checkout-shaped
`-Source` argument emitted by the 0.3.1 updater for backward compatibility,
while new bootstrap and updater code omit that ambiguous argument. No
renderer/profile compatibility changes are included.

## Release 0.3.0

This release changes the public product name to **Codex Relay**. It retains the
`codex-mux` state directory and the legacy update-manifest product identifier
so a verified `0.2.x` Windows installation can update in place. Windows tests
the same two exact Store `app.asar` profiles listed below; this rename does not
claim compatibility with an unreviewed upstream Codex build.

## Release 0.2.0

| Component | Tested value |
| --- | --- |
| Official ChatGPT version | `26.803.61601` |
| Official bundle build | `6396` |
| `app.asar` SHA-256 | `d5a44ed9e2f1db5f81dbbe85408aed256f3203c5b16f00817bb9d7cd941343cf` |
| Architecture | Apple silicon (`arm64`) |

A different official version may work when all anchors remain identical, but
it is unverified. The patcher rejects a version, build, or ASAR hash mismatch by
default; `--allow-untested-source` is an explicit diagnostic override. Never
weaken an anchor-count or binary-constant check merely to make a new build
complete. Review the upstream change and update the patch deliberately.

## Windows preview builds

The Windows patcher keeps a separate renderer profile for each reviewed Store
`app.asar`. Both profiles remain in `scripts/patch_windows.py`, so the same
checkout can be rebuilt after rolling the official app back to the older
package. An unknown hash still fails closed.

### Older Store profile

| Component | Tested value |
| --- | --- |
| Platform | Windows x64 |
| Microsoft Store package | `OpenAI.Codex_26.810.7004.0_x64__2p2nqsd0c76g0` |
| Electron app version | `26.810.52044` |
| Electron build number | `6662` |
| `app.asar` SHA-256 | `c7ac6d76cf5f30aa5cb92e1e46561933c06e94e3fe2d6582a04dac18c76f3ed1` |

### Current Store profile

| Component | Tested value |
| --- | --- |
| Platform | Windows x64 |
| Microsoft Store package | `OpenAI.Codex_26.818.2441.0_x64__2p2nqsd0c76g0` |
| ChatGPT executable file version | `151.0.7922.170` |
| `app.asar` SHA-256 | `71c60b36a782e5597f1ca90abf70dba6a9a6aa4e61f3be69e422be43666a7d70` |

The Windows patcher checks the recorded ASAR hash before copying the Store app.
It injects a local renderer asset, expands the renderer CSP only with the
loopback control origin `http://127.0.0.1:48123`, and applies exact-one anchors
for Profile statistics, Plugins account-scoped RPCs, and the native reset
sheet. The official Store package is never modified. Any missing, duplicated,
or changed Windows renderer anchor is a hard build failure.

The in-app updater is compatible with both profiles because it downloads the
source release and asks the installer to discover whichever reviewed Store
package is currently installed. It does not silently apply a patch to an
unreviewed upstream bundle.
