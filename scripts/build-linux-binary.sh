#!/usr/bin/env bash
set -euo pipefail

arch=${GOARCH:-arm64}
out=${1:-.cache/lfl-linux-$arch}
mkdir -p "$(dirname "$out")"
GOOS=linux GOARCH="$arch" go build -o "$out" ./cmd/lfl
echo "$out"
