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

if command -v ffmpeg >/dev/null 2>&1; then
  palette="$(mktemp "${TMPDIR:-/tmp}/codex-manage-demo-palette.XXXXXX.png")"
  trap 'rm -f "$palette"; cleanup' EXIT

  ffmpeg -y -i docs/assets/demo.mp4 \
    -vf "fps=15,scale=1440:-1:flags=lanczos,palettegen=stats_mode=full" \
    "$palette"
  ffmpeg -y -i docs/assets/demo.mp4 -i "$palette" \
    -lavfi "fps=15,scale=1440:-1:flags=lanczos[x];[x][1:v]paletteuse=dither=sierra2_4a:diff_mode=rectangle" \
    docs/assets/demo.gif
else
  printf '%s\n' "warning: ffmpeg not found; docs/assets/demo.gif was not regenerated" >&2
fi
