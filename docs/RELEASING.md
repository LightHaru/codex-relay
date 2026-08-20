# Releasing Codex Relay

Releases are source-only. Never attach a patched app, ASAR, extracted official
file, signing certificate, provisioning profile, or account data.

1. Update `VERSION`, `package.json`, and both version fields in
   `package-lock.json`.
2. Move changelog entries from Unreleased into `## [x.y.z] - YYYY-MM-DD`.
3. Record the tested official app version, build, architecture, and ASAR hash in
   `docs/COMPATIBILITY.md`.
4. Run `npm ci --ignore-scripts`, `npm run check`, and
   `npm run release:check` on macOS.
5. Complete `docs/SMOKE-TEST.md` with a team-backed signature and record the
   exact commit, macOS version, and signing team in the release draft.
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
`windows-update.json`, and attaches both files to a draft GitHub release. The
Windows Router only checks this manifest after the draft is published. Review
the archive name, hash, draft, and smoke-test record before publishing it
manually; never attach the official Store app or a patched ASAR.
