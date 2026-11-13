#!/bin/bash
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
  echo "请使用 sudo 在 NAS 上安装升级辅助服务。" >&2
  exit 1
fi
if [ "$(uname -s)" != "Linux" ]; then
  echo "此脚本仅用于 fnOS NAS。" >&2
  exit 1
fi
command -v systemctl >/dev/null
getent group vaultwarden >/dev/null
source_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
"${source_dir}/gateway-proxy" -h 2>&1 | grep -q -- '-updater-service'
install -d -o root -g root -m 0755 /usr/local/libexec
install -o root -g root -m 0755 "${source_dir}/gateway-proxy" /usr/local/libexec/vaultwarden-updater
install -o root -g root -m 0644 "${source_dir}/vaultwarden-updater.service" /etc/systemd/system/vaultwarden-updater.service
systemctl daemon-reload
systemctl enable vaultwarden-updater.service
systemctl restart vaultwarden-updater.service
echo "升级辅助服务已启用，请回到 Vaultwarden 管理页面检查安装权限。"
