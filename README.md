# codex-manage

`codex-manage` is a small terminal UI for switching between multiple Codex auth profiles on the fly.

[![codex-manage demo](docs/assets/demo.gif)](docs/assets/demo.mp4)

[Watch the demo video](docs/assets/demo.mp4)

It keeps saved profiles next to your local Codex config (`~/.codex/auth.json` on Linux/macOS, `%USERPROFILE%\.codex\auth.json` on Windows) and lets you quickly:

- save ChatGPT auth automatically under its account identity
- save API-key auth with an optional label or automatic fingerprint
- activate another saved profile
- re-authenticate an existing ChatGPT profile in its own browser session
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

In the TUI, select a ChatGPT profile and press `a`. The status area remains in an authentication state until the browser flow finishes; press `Esc` to cancel. API-key profiles cannot use this workflow.

## Browser-based re-authentication

Re-authentication runs `codex app-server` with a private temporary `CODEX_HOME`, then opens the returned OAuth URL in a dedicated Chromium user-data directory. The temporary login must return the same ChatGPT `account_id` as the selected saved profile. Only after that check passes does `codex-manage` replace and activate the saved credentials. A cancelled, failed, malformed, or mismatched login leaves the saved profile and active `auth.json` unchanged.

Brave, Chromium, Chrome, and Edge are supported. Detection prefers Brave, then Chromium, Chrome, and Edge. Override detection or storage when needed:

| Environment variable | Purpose |
| --- | --- |
| `CODEX_MANAGE_BROWSER_EXECUTABLE` | Browser executable path or command name |
| `CODEX_MANAGE_BROWSER_PROFILES_DIR` | Root for per-account browser data |
| `CODEX_BRAVE_EXE` | Legacy Brave executable override |
| `CODEX_BROWSER_PROFILES_DIR` | Legacy browser-profile root |

Existing `codex-browser` data under `~/.codex-browser-profiles` (or the Windows user profile equivalent) is reused automatically. Within a root, the stable auth-manager profile key is preferred; an existing legacy directory derived from the account label/email is reused when present. Browser data is intentionally not deleted when an auth profile is deleted.

Without an override or existing legacy root, new browser data is stored under:

- Linux: `${XDG_DATA_HOME:-~/.local/share}/codex-manage/browser-profiles/<browser>`
- macOS: `~/Library/Application Support/codex-manage/browser-profiles/<browser>`
- Windows: `%LOCALAPPDATA%\codex-manage\browser-profiles\<browser>`
- WSL with a Windows browser: the Windows Local AppData location above

These directories contain browser cookies and other session data, can grow substantially, and should be protected like any signed-in browser profile. Normal `codex login` and the Codex TUI `/login` remain untouched: `codex-manage` does not set or export `$BROWSER`.

If you previously sourced `codex-login.zsh` from `codex-browser`, you can stop sourcing it after moving to `codex-manage --login`; no browser data migration is required.

ChatGPT labels come from the email claim in `auth.json`; if it is unavailable, the UI shows a shortened account ID. API-key profiles use an optional custom label and otherwise show a non-secret SHA-256 fingerprint. Profile filenames are opaque internal keys and are not accepted by `--select`.

Saved profiles are restrictive copies of Codex's `auth.json`. This means API keys remain stored in plaintext with `0600` permissions, matching Codex's own representation.
