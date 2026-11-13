#!/bin/bash
set -euo pipefail

# 在 Linux CI 上真实执行 root -> 包用户切换；所有文件和服务均为临时夹具。
if [ "$(uname -s)" != Linux ] || [ "$(id -u)" -ne 0 ]; then
  echo "生命周期权限测试需要在 Linux 上以 root 运行。" >&2
  exit 1
fi

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d)"
test_user="vw-lifecycle-$$"
cleanup() {
  if [ -f "${test_root}/var/gateway-proxy.pid" ]; then
    kill -TERM "$(cat "${test_root}/var/gateway-proxy.pid")" 2>/dev/null || true
  fi
  userdel "${test_user}"
  rm -rf "${test_root}"
}
useradd --system --user-group --no-create-home "${test_user}"
trap cleanup EXIT
chmod 0755 "${test_root}"
cp -R "${project_root}/packaging/vaultwarden/cmd" "${test_root}/cmd"
mkdir -p "${test_root}/target/server" "${test_root}/target/ui" "${test_root}/var" "${test_root}/etc" "${test_root}/home"
cp "${project_root}/packaging/vaultwarden/app/server/gateway-proxy" "${test_root}/target/server/gateway-proxy"

export TRIM_APPDEST="${test_root}/target" TRIM_PKGVAR="${test_root}/var" TRIM_PKGETC="${test_root}/etc" TRIM_PKGHOME="${test_root}/home"
export TRIM_USERNAME="${test_user}" TRIM_GROUPNAME="${test_user}" TRIM_TEMP_LOGFILE="${test_root}/error.log" TRIM_APPVER="1.37.5-rc14"
touch "${TRIM_TEMP_LOGFILE}"
port="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')"
export wizard_port="${port}"

cat > "${test_root}/target/server/install-updater.sh" <<'SCRIPT'
#!/bin/bash
set -eu
test "$(id -u)" -eq 0
printf 'root-deploy\n' >> "${TRIM_PKGVAR}/helper-deploy.log"
SCRIPT
cat > "${test_root}/target/server/vaultwarden" <<'PYTHON'
#!/usr/bin/python3
import http.server, os
from pathlib import Path
Path(os.environ['TRIM_PKGVAR'], 'vaultwarden-uid').write_text(str(os.getuid()))
http.server.HTTPServer(('127.0.0.1', int(os.environ['ROCKET_PORT'])), http.server.BaseHTTPRequestHandler).serve_forever()
PYTHON
chmod +x "${test_root}/target/server/vaultwarden"
bash "${test_root}/cmd/install_callback"
test "$(cat "${test_root}/var/helper-deploy.log")" = root-deploy
test "$(stat -c %U "${test_root}/etc/vaultwarden.env")" = "${test_user}"
test "$(stat -c %U "${test_root}/target/server/gateway-proxy")" = root
test "$(stat -c %a "${test_root}/target")" = 1775

# 环境文件确实会执行，但执行身份必须已经降为包用户。
printf '\nid -u > "${TRIM_PKGVAR}/config-uid"\n' >> "${TRIM_PKGETC}/vaultwarden.env"
bash "${test_root}/cmd/main" start
package_uid="$(id -u "${test_user}")"
test "$(cat "${test_root}/var/config-uid")" = "${package_uid}"
test "$(cat "${test_root}/var/vaultwarden-uid")" = "${package_uid}"
gateway_pid="$(cat "${test_root}/var/gateway-proxy.pid")"
test "$(awk '/^Uid:/ {print $2}' "/proc/${gateway_pid}/status")" = "${package_uid}"
bash "${test_root}/cmd/main" status
runuser -u "${test_user}" -- test -w "${test_root}/target"
if runuser -u "${test_user}" -- mv "${test_root}/target/server" "${test_root}/target/server-moved" 2>/dev/null; then
  echo "包用户不应能替换 root 代码目录。" >&2
  exit 1
fi
bash "${test_root}/cmd/main" stop
if bash "${test_root}/cmd/main" status; then
  echo "停止后状态不应为运行中。" >&2
  exit 1
else
  test "$?" -eq 3
fi

# 已有配置的保留分支也必须部署助手；升级回调仍应输出普通用户可读配置。
export wizard_existing_config_action=keep
bash "${test_root}/cmd/install_callback"
bash "${test_root}/cmd/upgrade_callback"
test "$(wc -l < "${test_root}/var/helper-deploy.log")" -eq 3
test "$(stat -c %U "${test_root}/etc/vaultwarden.env")" = "${test_user}"
bash "${test_root}/cmd/config_callback"
test "$(cat "${test_root}/var/vaultwarden-uid")" = "${package_uid}"
bash "${test_root}/cmd/main" stop
echo "root 部署、普通用户加载配置/运行服务、目录保护及安装升级回调测试通过。"
