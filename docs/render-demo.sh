#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

cleanup() {
  rm -rf .vhs-demo-home .vhs-go .vhs-go-build
}

cleanup
trap cleanup EXIT

bash docs/demo-setup.sh
vhs docs/demo.tape "$@"
