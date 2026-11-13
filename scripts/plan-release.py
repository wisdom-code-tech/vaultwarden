#!/usr/bin/env python3
"""根据上游稳定版本和已发布正式版决定构建渠道。"""

import json
import os
from pathlib import Path
import re
import subprocess


def gh_json(*args):
    return json.loads(subprocess.check_output(["gh", *args], text=True))


def version_key(version):
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", version):
        raise ValueError(f"无效的稳定版本号: {version}")
    return tuple(map(int, version.split(".")))


root = Path(__file__).resolve().parent.parent
version = os.environ.get("REQUESTED_VERSION", "").removeprefix("v")
if not version:
    upstream = gh_json("api", "repos/dani-garcia/vaultwarden/releases/latest")
    if upstream["draft"] or upstream["prerelease"]:
        raise ValueError("上游 latest 不是已发布的稳定版")
    version = upstream["tag_name"].removeprefix("v")
version_key(version)

revision = (root / "PACKAGING_REVISION").read_text().strip()
if not re.fullmatch(r"[0-9]+", revision):
    raise ValueError(f"无效的打包修订号: {revision}")

pages = gh_json(
    "api", "--paginate", "--slurp",
    f"repos/{os.environ['GITHUB_REPOSITORY']}/releases?per_page=100",
)
releases = [release for page in pages for release in page]
manifest = (root / "packaging/vaultwarden/manifest").read_text()
baseline = re.search(r"^version\s*=\s*([0-9]+\.[0-9]+\.[0-9]+)", manifest, re.M)[1]
stable_versions = [baseline]
for release in releases:
    tag_version = release["tag_name"].removeprefix("v")
    # 历史 RC 曾被误标为正式版，必须同时检查版本号和发布标记。
    if (not release["draft"] and not release["prerelease"]
            and re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", tag_version)):
        stable_versions.append(tag_version)
baseline = max(stable_versions, key=version_key)

scheduled = os.environ["GITHUB_EVENT_NAME"] == "schedule"
stable = scheduled or os.environ.get("STABLE_RELEASE") == "true"
package_version = version if stable else f"{version}-rc{revision}"
tag = f"v{package_version}"
exists = any(release["tag_name"] == tag for release in releases)
new_upstream = version_key(version) > version_key(baseline)
should_build = not exists or os.environ.get("FORCE_BUILD") == "true"
if scheduled:
    should_build = new_upstream and not exists

outputs = {
    "version": version,
    "revision": revision,
    "package_version": package_version,
    "release_channel": "stable" if stable else "rc",
    "tag": tag,
    "release_exists": exists,
    "should_build": should_build,
    "make_latest": stable and version_key(version) >= version_key(baseline),
}
with open(os.environ["GITHUB_OUTPUT"], "a") as output:
    for key, value in outputs.items():
        print(f"{key}={str(value).lower() if isinstance(value, bool) else value}", file=output)
with open(os.environ["GITHUB_STEP_SUMMARY"], "a") as summary:
    summary.write(f"### Vaultwarden {package_version}\n\n")
    summary.write(f"当前正式版本：{baseline}；渠道：{outputs['release_channel']}；构建：{should_build}\n")
