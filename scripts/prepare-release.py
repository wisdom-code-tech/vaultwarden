#!/usr/bin/env python3
"""写入包版本与独立的上游来源信息。"""

import json
from pathlib import Path
import re
import sys

from release_version import package_version

if len(sys.argv) not in (3, 4):
    raise SystemExit("用法: prepare-release.sh <vaultwarden-version> <packaging-revision> [rc|stable]")
upstream = sys.argv[1]
revision = int(sys.argv[2])
channel = sys.argv[3] if len(sys.argv) == 4 else "rc"
version = package_version(upstream, revision, channel)
root = Path(__file__).resolve().parent.parent
manifest = root / "packaging/vaultwarden/manifest"
content = manifest.read_text()
content = re.sub(r"^version\s*=.*$", f"version        = {version}", content, flags=re.M)
label = "正式版" if channel == "stable" else "RC 测试版"
content = re.sub(r"^changelog\s*=.*$", f"changelog = Vaultwarden {upstream}；fnOS {label}，修订 {revision}（FPK {version}）。", content, flags=re.M)
manifest.write_text(content)
metadata = dict(upstream_version=upstream, package_version=version, revision=revision, channel=channel)
(root / "packaging/vaultwarden/app/server/PACKAGE.json").write_text(json.dumps(metadata, ensure_ascii=False, indent=2) + "\n")
(root / "VAULTWARDEN_VERSION").write_text(upstream + "\n")
print(f"已准备 Vaultwarden {upstream}；FPK {version}；{label}。")
