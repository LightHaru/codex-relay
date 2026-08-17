# Contributing

## Development setup

Use the platform relevant to the patch you are changing:

- **macOS:** Apple silicon, an official ChatGPT app, Go 1.26+, Node.js
  22.12+/npm, Xcode Command Line Tools, and a suitable signing identity for
  a complete app build.
- **Windows:** Windows x64, an installed Microsoft Store ChatGPT/Codex package,
  Python 3, Go 1.26+, and Node.js 22.12+/npm.

```sh
npm ci --ignore-scripts
npm run check
npm run release:check
```

Do not commit an app bundle, Store package, ASAR archive, credentials, signing
certificates, provisioning profiles, account state, or captures containing
unmasked email addresses, device codes, authorization URLs, or account IDs.

## Patch changes

Renderer and main-process patches depend on exact upstream anchors. A change
must:

1. Keep the official app immutable.
2. Fail closed when an expected anchor or binary constant is absent.
3. Preserve account isolation and sticky thread ownership.
4. Keep control services on loopback with token authentication.
5. Add focused tests for backend behavior and a curated screenshot for a new
   user-visible state when appropriate.
6. Preserve the Primary-first policy, safe pending-login cancellation, and the
   rule that the official installed app is never modified.

Test against the upstream build recorded in `docs/COMPATIBILITY.md`. If a new
official build requires anchor changes, update that file in the same pull
request.

## Pull requests

Keep changes focused and explain security-sensitive behavior explicitly. The
CI checks Go tests and vetting, JavaScript syntax, Python compilation, native C
syntax, and release metadata consistency.
