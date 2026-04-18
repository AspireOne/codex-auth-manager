# codex-manage

`codex-manage` is a small terminal UI for switching between multiple Codex auth profiles on the fly.

[![codex-manage demo](docs/assets/demo.gif)](docs/assets/demo.mp4)

[Watch the demo video](docs/assets/demo.mp4)

It keeps saved profiles next to your local Codex config (`~/.codex/auth.json` on Linux/macOS, `%USERPROFILE%\.codex\auth.json` on Windows) and lets you quickly:

- save the current auth file as a named profile
- activate another saved profile
- add a short note to a profile for quick context
- rename or delete saved profiles
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
