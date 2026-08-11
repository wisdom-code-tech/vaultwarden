#!/bin/bash
set -euo pipefail

readonly APP_VERSION="${VAULTWARDEN_VERSION:-1.37.1}"
readonly IMAGE_TAG="${APP_VERSION}-alpine"
readonly EXPECTED_AMD64_MANIFEST="${VAULTWARDEN_AMD64_MANIFEST-sha256:8e40ec37553bdfdf608d3f66ae17474ff6e53db44f1d8a7fa0685862f3db52ed}"
readonly REPOSITORY="vaultwarden/server"
readonly PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly SERVER_DIR="${PROJECT_ROOT}/packaging/vaultwarden/app/server"
readonly PACKAGE_DIR="${PROJECT_ROOT}/packaging/vaultwarden"

install_metadata_and_icons() {
  icon_source="${SERVER_DIR}/web-vault/images/android-chrome-512x512.png"
  if [ ! -f "${icon_source}" ]; then
    echo "Web Vault 中缺少应用图标。" >&2
    exit 1
  fi
  curl -fsSL "https://raw.githubusercontent.com/dani-garcia/vaultwarden/${APP_VERSION}/LICENSE.txt" -o "${PACKAGE_DIR}/LICENSE"
  mkdir -p "${PACKAGE_DIR}/app/ui/images"
  if command -v sips >/dev/null 2>&1; then
    sips -z 256 256 "${icon_source}" --out "${PACKAGE_DIR}/ICON_256.PNG" >/dev/null
    sips -z 64 64 "${icon_source}" --out "${PACKAGE_DIR}/ICON.PNG" >/dev/null
    cp "${PACKAGE_DIR}/ICON_256.PNG" "${PACKAGE_DIR}/app/ui/images/icon_256.png"
    cp "${PACKAGE_DIR}/ICON.PNG" "${PACKAGE_DIR}/app/ui/images/icon_64.png"
  elif command -v magick >/dev/null 2>&1; then
    magick "${icon_source}" -resize 256x256! "${PACKAGE_DIR}/ICON_256.PNG"
    magick "${icon_source}" -resize 64x64! "${PACKAGE_DIR}/ICON.PNG"
    cp "${PACKAGE_DIR}/ICON_256.PNG" "${PACKAGE_DIR}/app/ui/images/icon_256.png"
    cp "${PACKAGE_DIR}/ICON.PNG" "${PACKAGE_DIR}/app/ui/images/icon_64.png"
  elif command -v convert >/dev/null 2>&1; then
    convert "${icon_source}" -resize 256x256! "${PACKAGE_DIR}/ICON_256.PNG"
    convert "${icon_source}" -resize 64x64! "${PACKAGE_DIR}/ICON.PNG"
    cp "${PACKAGE_DIR}/ICON_256.PNG" "${PACKAGE_DIR}/app/ui/images/icon_256.png"
    cp "${PACKAGE_DIR}/ICON.PNG" "${PACKAGE_DIR}/app/ui/images/icon_64.png"
  else
    echo "缺少 PNG 缩放工具（macOS sips 或 ImageMagick magick）。" >&2
    exit 1
  fi
}

for command_name in curl jq tar file; do
  if ! command -v "${command_name}" >/dev/null 2>&1; then
    echo "缺少构建依赖: ${command_name}" >&2
    exit 1
  fi
done

if [ -x "${SERVER_DIR}/vaultwarden" ] && [ -d "${SERVER_DIR}/web-vault" ] && [ -f "${SERVER_DIR}/runtime/etc/ssl/certs/ca-certificates.crt" ] && grep -Fxq "version=${APP_VERSION}" "${SERVER_DIR}/UPSTREAM" 2>/dev/null; then
  install_metadata_and_icons
  echo "Vaultwarden ${APP_VERSION} 上游产物已存在，跳过下载。"
  exit 0
fi

work_dir="$(mktemp -d)"
trap 'rm -rf "${work_dir}"' EXIT
rootfs_dir="${work_dir}/rootfs"
mkdir -p "${rootfs_dir}"

token_json="$(curl -fsSL "https://auth.docker.io/token?service=registry.docker.io&scope=repository:${REPOSITORY}:pull")"
registry_token="$(printf '%s' "${token_json}" | jq -er '.token')"
registry_url="https://registry-1.docker.io/v2/${REPOSITORY}"
accept_index="application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json"
accept_manifest="application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json"

index_json="$(curl -fsSL -H "Authorization: Bearer ${registry_token}" -H "Accept: ${accept_index}" "${registry_url}/manifests/${IMAGE_TAG}")"
amd64_manifest="$(printf '%s' "${index_json}" | jq -er '.manifests[] | select(.platform.os == "linux" and .platform.architecture == "amd64") | .digest')"

if [ -n "${EXPECTED_AMD64_MANIFEST}" ] && [ "${amd64_manifest}" != "${EXPECTED_AMD64_MANIFEST}" ]; then
  echo "上游镜像摘要发生变化，拒绝构建。" >&2
  echo "期望: ${EXPECTED_AMD64_MANIFEST}" >&2
  echo "实际: ${amd64_manifest}" >&2
  exit 1
fi

manifest_json="$(curl -fsSL -H "Authorization: Bearer ${registry_token}" -H "Accept: ${accept_manifest}" "${registry_url}/manifests/${amd64_manifest}")"
layers="$(printf '%s' "${manifest_json}" | jq -er '.layers[].digest')"

for layer_digest in ${layers}; do
  layer_file="${work_dir}/${layer_digest#sha256:}.tar.gz"
  echo "下载层 ${layer_digest}"
  curl -fsSL --retry 3 -H "Authorization: Bearer ${registry_token}" "${registry_url}/blobs/${layer_digest}" -o "${layer_file}"
  tar -xzf "${layer_file}" -C "${rootfs_dir}"
done

if [ ! -x "${rootfs_dir}/vaultwarden" ] || [ ! -d "${rootfs_dir}/web-vault" ]; then
  echo "官方镜像中缺少 Vaultwarden 或 Web Vault 产物。" >&2
  exit 1
fi

rm -rf "${SERVER_DIR}/runtime" "${SERVER_DIR}/web-vault" "${SERVER_DIR}/vaultwarden"
mkdir -p "${SERVER_DIR}/runtime/etc/ssl/certs"
cp "${rootfs_dir}/vaultwarden" "${SERVER_DIR}/vaultwarden"
cp -R "${rootfs_dir}/web-vault" "${SERVER_DIR}/web-vault"
if [ -f "${rootfs_dir}/etc/ssl/certs/ca-certificates.crt" ]; then
  cp "${rootfs_dir}/etc/ssl/certs/ca-certificates.crt" "${SERVER_DIR}/runtime/etc/ssl/certs/"
fi
chmod 0755 "${SERVER_DIR}/vaultwarden"

binary_type="$(file "${SERVER_DIR}/vaultwarden")"
case "${binary_type}" in
  *ELF*64-bit*x86-64*) ;;
  *)
    echo "提取的二进制不是 Linux x86_64 ELF: ${binary_type}" >&2
    exit 1
    ;;
esac

source_commit="$(curl -fsSL "https://api.github.com/repos/dani-garcia/vaultwarden/commits/${APP_VERSION}" | jq -er '.sha')"

printf '%s\n' \
  "version=${APP_VERSION}" \
  "source=https://github.com/dani-garcia/vaultwarden/tree/${APP_VERSION}" \
  "source_commit=${source_commit}" \
  "image=docker.io/${REPOSITORY}:${IMAGE_TAG}" \
  "manifest=${amd64_manifest}" \
  > "${SERVER_DIR}/UPSTREAM"

install_metadata_and_icons
echo "已提取 Vaultwarden ${APP_VERSION} (${amd64_manifest})。"
