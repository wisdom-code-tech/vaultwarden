#!/bin/bash
set -euo pipefail

readonly PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly OUTPUT="${PROJECT_ROOT}/packaging/vaultwarden/app/server/gateway-proxy"

if ! command -v go >/dev/null 2>&1; then
  echo "缺少 Go 1.22+，无法构建统一网关代理。" >&2
  exit 1
fi

(
  cd "${PROJECT_ROOT}/gateway"
  go test ./...
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -trimpath -buildvcs=false -ldflags='-s -w -buildid=' \
    -o "${OUTPUT}" .
)
chmod 0755 "${OUTPUT}"
file "${OUTPUT}" | grep -q 'ELF 64-bit.*x86-64.*statically linked'
echo "统一网关代理构建完成。"
