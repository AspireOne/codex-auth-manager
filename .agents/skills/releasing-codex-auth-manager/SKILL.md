---
name: releasing-codex-auth-manager
description: "Releases codex-auth-manager through its GitHub tag workflow and verifies Homebrew/Scoop propagation. Use for release, publish, tag-and-release, or update/watch brew and scoop requests. Do not use for ordinary pushes, CI diagnosis, or release-infrastructure edits."
---

# Release

Canonical sources: `AGENTS.md`, `README.md`, `scripts/release.go`,
`.github/workflows/release.yml`, `.github/workflows/homebrew-tap.yml`, and
`.github/workflows/scoop-bucket.yml`.

1. Resolve `VERSION`: use the requested version; otherwise derive the next
   stable SemVer from the latest tag and Conventional Commits only when
   unambiguous; ask if ambiguous. Never overwrite an existing tag or infer a
   prerelease.
2. Read repository instructions. Require a clean tree, fetch tags, verify
   `origin` access, and confirm the target SHA. Before any requested push,
   run a push dry-run. Never change remotes, credential helpers, or tokens;
   stop and report authentication failures.
3. If pushing commits was requested, push the target branch and verify its
   remote SHA. Otherwise require the release target to already be remote.
4. Run `go run scripts/release.go "$VERSION"`; never use `-skip-tests`
   without explicit approval. Do not manually create, delete, or retag
   releases.
5. Find the tag-triggered `Release` workflow for `VERSION`; poll until
   terminal. On failure, inspect failed logs and stop. Verify a successful,
   published, non-draft release with six platform archives and
   `SHA256SUMS.txt`.
6. Find matching `Update Homebrew Tap` and `Update Scoop Bucket` runs; poll
   until terminal. If release publication succeeded but a workflow was not
   dispatched, dispatch it once with `version="$VERSION"`. Treat “already up
   to date” as success only after verifying the repositories.
7. Verify `Formula/codex-manage.rb` references the tagged source archive and
   its SHA-256; verify `bucket/codex-manage.json` has the version, Windows
   asset URLs, and matching hashes. Never edit tap repositories directly.
8. Report the release/tag, target SHA, tests, workflow URLs, tap commit URLs,
   and every no-op, failure, or unresolved state. Do not claim propagation
   from dispatch success alone.
