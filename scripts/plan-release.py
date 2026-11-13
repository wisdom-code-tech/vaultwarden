#!/usr/bin/env python3
"""根据上游稳定版本和已发布正式版决定构建渠道。"""

import json
import os
from pathlib import Path
import re
import subprocess

from release_version import package_version as mapped_version, stable_release, version_key


def gh_json(*args):
    return json.loads(subprocess.check_output(["gh", *args], text=True))


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
baseline = (root / "VAULTWARDEN_VERSION").read_text().strip()
stable_versions = [baseline]
stable_packages = []
for release in releases:
    # 历史 RC 曾被误标为正式版，必须同时检查版本号和发布标记。
    if not release["draft"] and not release["prerelease"]:
        identity = stable_release(release["tag_name"])
        if identity:
            upstream, package = identity
            stable_versions.append(upstream)
            stable_packages.append(version_key(package))
baseline = max(stable_versions, key=version_key)

scheduled = os.environ["GITHUB_EVENT_NAME"] == "schedule"
stable = scheduled or os.environ.get("STABLE_RELEASE") == "true"
channel = "stable" if stable else "rc"
package_version = mapped_version(version, int(revision), channel)
tag = f"v{version}-fnos{revision}" if stable else f"v{version}-rc{revision}"
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
    "make_latest": stable and version_key(version) >= version_key(baseline)
    and (not stable_packages or version_key(package_version) >= max(stable_packages)),
}
with open(os.environ["GITHUB_OUTPUT"], "a") as output:
    for key, value in outputs.items():
        print(f"{key}={str(value).lower() if isinstance(value, bool) else value}", file=output)
with open(os.environ["GITHUB_STEP_SUMMARY"], "a") as summary:
    summary.write(f"### Vaultwarden {package_version}\n\n")
    summary.write(f"当前正式版本：{baseline}；渠道：{outputs['release_channel']}；构建：{should_build}\n")
