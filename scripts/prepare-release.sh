#!/bin/bash
set -euo pipefail

if [ "$#" -ne 3 ]; then
  echo "用法: $0 <vaultwarden-version> <packaging-revision> <owner/repository>" >&2
  exit 1
fi

version="$1"
revision="$2"
update_repository="$3"
readonly PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly MANIFEST="${PROJECT_ROOT}/packaging/vaultwarden/manifest"
readonly UPDATE_REPOSITORY_FILE="${PROJECT_ROOT}/packaging/vaultwarden/app/server/UPDATE_REPOSITORY"
readonly PACKAGE_VERSION_FILE="${PROJECT_ROOT}/packaging/vaultwarden/app/server/PACKAGE_VERSION"

case "${version}" in
  ''|*[!0-9.]*|.*|*.)
    echo "无效的 Vaultwarden 版本: ${version}" >&2
    exit 1
    ;;
esac
case "${revision}" in
  ''|*[!0-9]*)
    echo "无效的打包修订号: ${revision}" >&2
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

package_version="${version}-rc${revision}"
sed -i.bak -E "s/^version([[:space:]]*)=.*/version\\1= ${package_version}/" "${MANIFEST}"
sed -i.bak -E "s/^changelog([[:space:]]*)=.*/changelog\\1= Vaultwarden ${version}；fnOS 打包修订 rc${revision}。/" "${MANIFEST}"
find "${PROJECT_ROOT}/packaging/vaultwarden" -name '*.bak' -delete
printf '%s\n' "${update_repository}" > "${UPDATE_REPOSITORY_FILE}"
printf '%s\n' "${package_version}" > "${PACKAGE_VERSION_FILE}"

echo "已准备 Vaultwarden ${package_version}，更新源为 ${update_repository}。"
