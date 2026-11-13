#!/bin/bash
set -euo pipefail

readonly PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly PACKAGE_DIR="${PROJECT_ROOT}/packaging/vaultwarden"

required_files=(
  manifest config/privilege config/resource
  ICON.PNG ICON_256.PNG LICENSE
  app/ui/config app/ui/images/icon_64.png app/ui/images/icon_256.png
  app/ui/manage/index.html app/ui/manage/style.css app/ui/manage/app.js
  app/server/vaultwarden app/server/runtime/etc/ssl/certs/ca-certificates.crt
  app/server/gateway-proxy app/server/PACKAGE.json app/server/UPSTREAM
  app/server/install-updater.sh app/server/vaultwarden-updater.service
  cmd/common cmd/main cmd/install_init cmd/install_callback
  cmd/uninstall_init cmd/uninstall_callback
  cmd/upgrade_init cmd/upgrade_callback
  cmd/config_init cmd/config_callback
  wizard/install wizard/config wizard/uninstall wizard/upgrade
)

for relative_path in "${required_files[@]}"; do
  if [ ! -e "${PACKAGE_DIR}/${relative_path}" ]; then
    echo "缺少必需文件: ${relative_path}" >&2
    exit 1
  fi
done

bash -n "${PACKAGE_DIR}/app/server/install-updater.sh"
python3 - "${PROJECT_ROOT}" <<'PY'
import json
from pathlib import Path
import sys
root = Path(sys.argv[1])
sys.path.insert(0, str(root / "scripts"))
from release_version import package_version
package = root / "packaging/vaultwarden"
fields = lambda path: dict(line.split("=", 1) for line in path.read_text().splitlines() if "=" in line)
manifest = {k.strip(): v.strip() for k, v in fields(package / "manifest").items()}
metadata = json.loads((package / "app/server/PACKAGE.json").read_text())
expected = package_version(metadata["upstream_version"], metadata["revision"], metadata["channel"])
assert manifest["version"] == metadata["package_version"] == expected, "FPK 版本与元数据不一致"
assert fields(package / "app/server/UPSTREAM")["version"] == metadata["upstream_version"], "实际上游程序版本与包元数据不一致"
PY
jq -e '.defaults["run-as"] == "root" and .username == "vaultwarden" and .groupname == "vaultwarden"' "${PACKAGE_DIR}/config/privilege" >/dev/null

for json_file in config/privilege config/resource app/ui/config app/server/PACKAGE.json wizard/install wizard/config wizard/uninstall wizard/upgrade; do
  jq -e . "${PACKAGE_DIR}/${json_file}" >/dev/null
done

for script_file in "${PACKAGE_DIR}"/cmd/*; do
  bash -n "${script_file}"
  if [ ! -x "${script_file}" ]; then
    echo "生命周期脚本不可执行: ${script_file}" >&2
    exit 1
  fi
done

file "${PACKAGE_DIR}/app/server/vaultwarden" | grep -q 'ELF 64-bit.*x86-64.*static-pie linked'
file "${PACKAGE_DIR}/app/server/gateway-proxy" | grep -q 'ELF 64-bit.*x86-64.*statically linked'
grep -q '^platform *= *x86' "${PACKAGE_DIR}/manifest"
if find "${PACKAGE_DIR}" -iname '*docker*' -print -quit | grep -q .; then
  echo "原生包中不允许出现 Docker 相关文件。" >&2
  exit 1
fi

echo "FPK 静态校验通过。"
