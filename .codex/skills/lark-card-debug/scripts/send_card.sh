#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

bin_dir="${LARK_CARD_DEBUG_BIN_DIR:-/mnt/RapidPool/tmp/lark-card-debug}"
mkdir -p "$bin_dir"
bin_path="$bin_dir/lark-card-debug"

export GOCACHE="${GOCACHE:-/mnt/RapidPool/tmp/gocache}"
export GOMODCACHE="${GOMODCACHE:-/mnt/RapidPool/tmp/gomodcache}"

go build -o "$bin_path" ./cmd/lark-card-debug
exec "$bin_path" "$@"
