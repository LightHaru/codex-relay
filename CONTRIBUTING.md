# Contributing

## Development setup

Use macOS on Apple silicon with Go 1.26+, Node.js 22.12+, npm, Xcode Command Line
Tools, and an official ChatGPT installation.

```sh
npm ci --ignore-scripts
npm run check
npm run release:check
```

Do not commit an app bundle, credentials, signing certificates, provisioning
profiles, account state, or captures containing unmasked email addresses or
device codes.

## Patch changes

Renderer and main-process patches depend on exact upstream anchors. A change
must:

1. Keep the official app immutable.
2. Fail closed when an expected anchor or binary constant is absent.
3. Preserve account isolation and sticky thread ownership.
4. Keep control services on loopback with token authentication.
5. Add focused tests for backend behavior and a curated screenshot for a new
   user-visible state when appropriate.

Test against the upstream build recorded in `docs/COMPATIBILITY.md`. If a new
official build requires anchor changes, update that file in the same pull
request.

## Pull requests

Keep changes focused and explain security-sensitive behavior explicitly. The
CI checks Go tests and vetting, JavaScript syntax, Python compilation, native C
syntax, and release metadata consistency.
