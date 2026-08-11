#!/bin/bash
set -euo pipefail

readonly PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly PACKAGE_DIR="${PROJECT_ROOT}/packaging/vaultwarden"
readonly DIST_DIR="${PROJECT_ROOT}/dist"
readonly LOCAL_FNPACK="${PROJECT_ROOT}/.tools/fnpack"

if command -v fnpack >/dev/null 2>&1; then
  FNPACK="$(command -v fnpack)"
elif [ -x "${LOCAL_FNPACK}" ]; then
  FNPACK="${LOCAL_FNPACK}"
else
  echo "未找到 fnpack。请安装 fnpack 1.2.1 后重新执行 make build。" >&2
  exit 1
fi

mkdir -p "${DIST_DIR}"
(
  cd "${PACKAGE_DIR}"
  "${FNPACK}" build
)

fpk_file="$(find "${PACKAGE_DIR}" -maxdepth 1 -type f -name '*.fpk' -print -quit)"
if [ -z "${fpk_file}" ]; then
  echo "fnpack 未生成 FPK 文件。" >&2
  exit 1
fi
mv "${fpk_file}" "${DIST_DIR}/vaultwarden.fpk"
echo "构建完成: ${DIST_DIR}/vaultwarden.fpk"
