# Compatibility

The patcher is intentionally tied to known ChatGPT desktop bundle structures.
It verifies every modified renderer, main-process, and native binary anchor and
stops instead of applying a partial patch.

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
