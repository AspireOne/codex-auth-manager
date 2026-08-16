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

printf '%s\n' '{"auth_mode":"chatgpt","tokens":{"account_id":"acct-personal-demo","id_token":"header.eyJlbWFpbCI6InBlcnNvbmFsQGV4YW1wbGUuY29tIn0.signature"}}' > .vhs-demo-home/.codex/auth_manager/profiles/personal
printf '%s\n' '{"auth_mode":"chatgpt","tokens":{"account_id":"acct-work-demo","id_token":"header.eyJlbWFpbCI6IndvcmtAZXhhbXBsZS5jb20ifQ.signature"}}' > .vhs-demo-home/.codex/auth_manager/profiles/work
printf '%s\n' '{"auth_mode":"apikey","OPENAI_API_KEY":"sk-demo-unsaved"}' > .vhs-demo-home/.codex/auth.json
