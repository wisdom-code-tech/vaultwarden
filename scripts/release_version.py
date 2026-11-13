"""统一 fnOS 三段数字包版本与上游版本的映射。"""

import re


def version_key(value):
    if not re.fullmatch(r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)", value):
        raise ValueError(f"无效的稳定版本号: {value}")
    return tuple(map(int, value.split(".")))


def package_version(version, revision, channel):
    major, minor, patch = version_key(version)
    if not 1 <= revision <= 499:
        raise ValueError("打包修订号必须在 1 到 499 之间")
    if channel not in ("rc", "stable"):
        raise ValueError(f"无效的发布类型: {channel}")
    return f"{major}.{minor}.{patch * 1000 + (500 if channel == 'stable' else 0) + revision}"


def stable_release(tag):
    value = tag.removeprefix("v")
    match = re.fullmatch(r"([0-9]+\.[0-9]+\.[0-9]+)-fnos([0-9]+)", value)
    if match:
        upstream, revision = match.groups()
        return upstream, package_version(upstream, int(revision), "stable")
    if re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", value):
        version_key(value)
        return value, value
    return None
