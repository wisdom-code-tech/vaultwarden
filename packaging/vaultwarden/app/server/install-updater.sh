#!/bin/bash
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
  echo "升级辅助服务必须由飞牛的 root 生命周期部署。" >&2
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
temporary_binary="$(mktemp /usr/local/libexec/.vaultwarden-updater.XXXXXX)"
trap 'rm -f "${temporary_binary}"' EXIT
install -o root -g root -m 0755 "${source_dir}/gateway-proxy" "${temporary_binary}"
mv -f "${temporary_binary}" /usr/local/libexec/vaultwarden-updater
install -o root -g root -m 0644 "${source_dir}/vaultwarden-updater.service" /etc/systemd/system/vaultwarden-updater.service
systemctl daemon-reload
systemctl enable vaultwarden-updater.service
if systemctl is-active --quiet vaultwarden-updater.service; then
  # reload 只请求切换：助手会等待正在执行的升级任务完成后再退出重启。
  systemctl reload vaultwarden-updater.service
else
  systemctl start vaultwarden-updater.service
fi
echo "升级辅助服务已由应用生命周期部署。"
