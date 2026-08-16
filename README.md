# codex-manage

`codex-manage` is a small terminal UI for switching between multiple Codex auth profiles on the fly.

[![codex-manage demo](docs/assets/demo.gif)](docs/assets/demo.mp4)

[Watch the demo video](docs/assets/demo.mp4)

It keeps saved profiles next to your local Codex config (`~/.codex/auth.json` on Linux/macOS, `%USERPROFILE%\.codex\auth.json` on Windows) and lets you quickly:

- save ChatGPT auth automatically under its account identity
- save API-key auth with an optional label or automatic fingerprint
- activate another saved profile
- re-authenticate an existing ChatGPT profile in its own browser session
- see cached ChatGPT quota usage, reset time, and authentication status
- assign Free, Plus, or Pro plans with paid plans grouped first
- add a short note to a profile for quick context
- edit API-key labels or delete saved profiles
- log out by removing the active `auth.json`

This is useful if you regularly work with multiple Codex accounts and want a faster way to swap between them without logging out/in constantly or manually copying auth files around.

## Install

On macOS or Linux, install with Homebrew:

```sh
brew install AspireOne/tap/codex-manage
```

On Windows, install with Scoop:

```powershell
scoop bucket add AspireOne https://github.com/AspireOne/scoop-bucket
scoop install codex-manage
```

Or download the archive for your platform from the [latest GitHub release](https://github.com/AspireOne/codex-auth-manager/releases/latest), extract it, and put the `codex-manage` binary somewhere on your `PATH`.

To update an existing package-manager install:

```sh
brew update
brew upgrade codex-manage
```

```powershell
scoop update
scoop update codex-manage
```

## Build

```sh
make build
```

This produces a binary named `codex-manage` (or `codex-manage.exe` on Windows) in the `dist/` directory.

You can also build directly with Go:

```sh
go build -o dist/ ./cmd/codex-manage
```

## Test

Run the complete automated suite:

```sh
go test ./...
```

The suite includes file-backed, black-box login scenarios with fake Codex and browser executables. To additionally exercise the installed Codex app-server handshake and login-start API without opening a browser or changing credentials:

```sh
CODEX_MANAGE_TEST_INSTALLED_CODEX=1 \
  go test ./internal/reauth -run TestInstalledCodexAppServerLoginStartAndCancel -count=1 -v
```

See [Authentication testing](docs/authentication-testing.md) for the coverage matrix and optional live OAuth checklist.

## Development checks

Install [Lefthook](https://github.com/evilmartians/lefthook) once, then install this repository's Git hooks:

```sh
make hooks
```

The pre-commit hook formats and re-stages Go files, verifies both Go module graphs, runs golangci-lint and NilAway, runs the complete test suite with the race detector and cache disabled, and cross-builds the six release targets. The commit-message hook enforces Conventional Commits.

Run the same checks without committing with:

```sh
make check
```

## Release

Create and push a release tag with:

```sh
go run scripts/release.go v0.1.0
```

This script ensures your working tree is clean, runs tests, creates an annotated git tag, and pushes it to `origin`. GitHub Actions then builds release archives for Linux, macOS, and Windows, publishes a GitHub release, and includes the commits since the previous tag in the release notes.

## Homebrew

This repo can also update a separate Homebrew tap repository whenever a GitHub release is published.

One-time setup:

1. Create a tap repo on GitHub, for example `AspireOne/homebrew-tap`.
2. In this repo, add a repository variable named `HOMEBREW_TAP_REPO` with that value.
3. Add a repository secret named `HOMEBREW_TAP_TOKEN` containing a GitHub token that can push to the tap repo.

After that, each published release updates `Formula/codex-manage.rb` in the tap automatically.

Users can then install with:

```sh
brew install AspireOne/tap/codex-manage
```

## Scoop

This repo can also update a separate Scoop bucket repository whenever a GitHub release is published.

One-time setup:

1. Create a bucket repo on GitHub, for example `AspireOne/scoop-bucket`.
2. In this repo, add a repository variable named `SCOOP_BUCKET_REPO` with that value.
3. Add a repository secret named `SCOOP_BUCKET_TOKEN` containing a GitHub token that can push to the bucket repo.

After that, each published release updates `bucket/codex-manage.json` in the bucket automatically.

Users can then install with:

```powershell
scoop bucket add AspireOne https://github.com/AspireOne/scoop-bucket
scoop install codex-manage
```

## Run

```sh
./dist/codex-manage
```

(Or `./dist/codex-manage.exe` on Windows)

List saved profiles without opening the TUI:

```sh
codex-manage --list
codex-manage -l
```

Activate a saved profile by its displayed label:

```sh
codex-manage --select matej@example.com
codex-manage -s "Personal API project"
```

Re-authenticate and activate an existing ChatGPT profile:

```sh
codex-manage --login matej@example.com
```

Or, in the TUI, select a ChatGPT profile and press `a`; press `Esc` to cancel. API-key profiles cannot use this workflow.

## Browser-based re-authentication

Select a ChatGPT profile and sign in again without leaving the manager. It opens a dedicated browser session, then updates and activates the profile after a successful sign-in.

Brave, Chromium, Chrome, and Edge are supported. If needed, choose the browser or browser-profile location with:

| Environment variable | Purpose |
| --- | --- |
| `CODEX_MANAGE_BROWSER_EXECUTABLE` | Browser executable path or command name |
| `CODEX_MANAGE_BROWSER_PROFILES_DIR` | Browser-profile location |

The profile list also shows each ChatGPT account's current quota usage, reset time, and sign-in status at a glance. Usage details are refreshed automatically and reused briefly, keeping profile switching fast.
