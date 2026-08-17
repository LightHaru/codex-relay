# Compatibility

The patcher is intentionally tied to known ChatGPT desktop bundle structures.
It verifies every modified renderer, main-process, and native binary anchor and
stops instead of applying a partial patch.

## Release 0.1.0

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

## Windows preview build

| Component | Tested value |
| --- | --- |
| Platform | Windows x64 |
| Microsoft Store package | `OpenAI.Codex_26.810.7004.0_x64__2p2nqsd0c76g0` |
| Electron app version | `26.810.52044` |
| Electron build number | `6662` |
| `app.asar` SHA-256 | `c7ac6d76cf5f30aa5cb92e1e46561933c06e94e3fe2d6582a04dac18c76f3ed1` |

The Windows patcher checks the recorded ASAR hash before copying the Store app.
It injects a local renderer asset, expands the renderer CSP only with the
loopback control origin `http://127.0.0.1:48123`, and applies exact-one anchors
for Profile statistics, Plugins account-scoped RPCs, and the native reset
sheet. The official Store package is never modified. Any missing, duplicated,
or changed Windows renderer anchor is a hard build failure.
