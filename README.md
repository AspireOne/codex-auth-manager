# codex-manage

`codex-manage` is a small terminal UI for switching between multiple Codex auth profiles on the fly.

It keeps saved profiles next to your local Codex config and lets you quickly:

- save the current `~/.codex/auth.json` as a named profile
- activate another saved profile
- rename or delete saved profiles
- log out by removing the active `auth.json`

This is useful if you regularly work with multiple Codex accounts and want a faster way to swap between them without logging out/in constantly or manually copying auth files around.

## Build

```sh
make build
```

This produces a Linux `amd64` binary named `codex-manage`.

## Run

```sh
./codex-manage
```
