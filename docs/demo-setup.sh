#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

export GOCACHE="$repo_root/.vhs-go-build"
export GOPATH="$repo_root/.vhs-go"
export GOMODCACHE="$GOPATH/pkg/mod"
umask 077

rm -rf .vhs-demo-home .vhs-go .vhs-go-build
mkdir -p .vhs-demo-home/.codex/auth_manager/profiles docs/assets dist

go build -o dist/codex-manage ./cmd/codex-manage

printf '%s\n' '{"auth_mode":"account","tokens":{"account_id":"acct-personal-demo"}}' > .vhs-demo-home/.codex/auth_manager/profiles/personal
printf '%s\n' '{"auth_mode":"account","tokens":{"account_id":"acct-work-demo"}}' > .vhs-demo-home/.codex/auth_manager/profiles/work
printf '%s\n' '{"auth_mode":"account","tokens":{"account_id":"acct-unsaved-demo"}}' > .vhs-demo-home/.codex/auth.json
