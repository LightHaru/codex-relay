# Releasing Codex Relay

Releases are source-only. Never attach a patched app, ASAR, extracted official
file, signing certificate, provisioning profile, credential, or account data.

Release `0.5.0` is the Unified Pool Gateway line: one public Relay API and task
authority over a hidden sticky quota pool. Do not describe deterministic fake
upstream tests as real-account quota evidence, and do not publish while any
required live transition is still `LIVE PENDING` unless the release explicitly
states that limitation.

1. Update `VERSION`, `package.json`, and both version fields in
   `package-lock.json`.
2. Move changelog entries from Unreleased into `## [x.y.z] - YYYY-MM-DD`.
3. Record the tested official app version, build, architecture, and ASAR hash in
   `docs/COMPATIBILITY.md`.
4. Run `npm ci --ignore-scripts`, `npm run check`, `npm run release:check`, and
   `git diff --check` on the target Windows build (and CI on other platforms).
5. Complete `docs/SMOKE-TEST.md` against the exact Windows Store profile and
   record the commit, app version/hash, pool schema, sanitized evidence and
   final exit codes in the release draft.
6. Review `git diff --check` and confirm no ignored credentials or app bundles
   are staged.
7. Configure the protected `release` environment, tag the reviewed commit as
   `vX.Y.Z`, and push the tag.

The canonical repository is now
`https://github.com/LightHaru/codex-relay`. Keep the manifest's
`product: "codex-subscription-router"` compatibility identifier unchanged:
installed 0.2.x Windows builds validate that value before they can update to a
Relay release. The visible product name and release URLs use Codex Relay.

The release workflow verifies that the tag matches `VERSION`, repeats all
checks, creates a source archive, calculates its SHA-256, writes
`windows-update.json`, and attaches those files plus the reviewed
`install-codex-relay.ps1` bootstrap to a draft GitHub release. The Windows
Router only checks this manifest after the draft is published. The bootstrap
also validates this manifest's exact release URL and SHA-256 before it invokes
the source installer for a first-time user.

Review the archive name, hash, bootstrap asset, draft, and smoke-test record
before publishing manually; never attach the official Store app or a patched
ASAR.
