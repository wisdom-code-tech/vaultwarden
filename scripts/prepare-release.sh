#!/bin/bash
set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo "用法: $0 <vaultwarden-version> <owner/repository>" >&2
  exit 1
fi

version="$1"
update_repository="$2"
readonly PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly MANIFEST="${PROJECT_ROOT}/packaging/vaultwarden/manifest"
readonly UPDATE_REPOSITORY_FILE="${PROJECT_ROOT}/packaging/vaultwarden/app/server/UPDATE_REPOSITORY"

case "${version}" in
  ''|*[!0-9.]*|.*|*.)
    echo "无效的 Vaultwarden 版本: ${version}" >&2
    exit 1
    ;;
esac
case "${update_repository}" in
  */*) ;;
  *)
    echo "无效的 GitHub 仓库: ${update_repository}" >&2
    exit 1
    ;;
esac

sed -i.bak -E "s/^version([[:space:]]*)=.*/version\\1= ${version}/" "${MANIFEST}"
sed -i.bak -E "s/^changelog([[:space:]]*)=.*/changelog\\1= Vaultwarden ${version}；由 GitHub Actions 基于固定上游镜像摘要构建。/" "${MANIFEST}"
find "${PROJECT_ROOT}/packaging/vaultwarden" -name '*.bak' -delete
printf '%s\n' "${update_repository}" > "${UPDATE_REPOSITORY_FILE}"

echo "已准备 Vaultwarden ${version}，更新源为 ${update_repository}。"
