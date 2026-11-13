import json
import os
from pathlib import Path
import runpy
import shutil
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from release_version import package_version, stable_release, version_key


class ReleaseVersionTests(unittest.TestCase):
    def test_ordering_and_boundaries(self):
        self.assertEqual(package_version("1.37.5", 14, "rc"), "1.37.5014")
        self.assertEqual(package_version("1.37.5", 15, "stable"), "1.37.5515")
        self.assertEqual(package_version("1.38.0", 15, "stable"), "1.38.515")
        self.assertLess(version_key(package_version("1.37.5", 499, "rc")), version_key(package_version("1.37.5", 1, "stable")))
        self.assertLess(version_key(package_version("1.37.5", 499, "stable")), version_key(package_version("1.37.6", 1, "rc")))
        for revision in (0, 500):
            with self.assertRaises(ValueError):
                package_version("1.37.5", revision, "stable")

    def test_release_tag_identity(self):
        self.assertEqual(stable_release("v1.37.5-fnos15"), ("1.37.5", "1.37.5515"))
        self.assertEqual(stable_release("v1.37.5"), ("1.37.5", "1.37.5"))
        self.assertIsNone(stable_release("v1.37.5-rc14"))

    def test_plan_and_preparation(self):
        source = Path(__file__).resolve().parent
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / "scripts").mkdir()
            (root / "packaging/vaultwarden/app/server").mkdir(parents=True)
            for name in ("plan-release.py", "prepare-release.py", "release_version.py"):
                shutil.copy(source / name, root / "scripts" / name)
            (root / "PACKAGING_REVISION").write_text("15\n")
            (root / "VAULTWARDEN_VERSION").write_text("1.37.5\n")
            manifest = root / "packaging/vaultwarden/manifest"
            manifest.write_text("version = 1.37.5\nchangelog = old\n")

            def plan(version, tags, event="workflow_dispatch", stable="true", force="false"):
                output = root / "outputs"
                output.write_text("")
                def gh(args, **kwargs):
                    if "repos/dani-garcia/vaultwarden/releases/latest" in args:
                        return json.dumps(dict(tag_name=version, prerelease=False, draft=False))
                    return json.dumps([[dict(tag_name=tag, prerelease=False, draft=False) for tag in tags]])
                env = dict(GITHUB_EVENT_NAME=event, GITHUB_REPOSITORY="test/repo", REQUESTED_VERSION="", STABLE_RELEASE=stable, FORCE_BUILD=force, GITHUB_OUTPUT=str(output), GITHUB_STEP_SUMMARY=str(root / "summary"))
                with patch.dict(os.environ, env, clear=True), patch("subprocess.check_output", side_effect=gh):
                    runpy.run_path(str(root / "scripts/plan-release.py"))
                return dict(line.split("=", 1) for line in output.read_text().splitlines())

            migration = plan("1.37.5", ["v1.37.5", "v1.37.5-rc14"])
            self.assertEqual(migration["package_version"], "1.37.5515")
            self.assertEqual(migration["tag"], "v1.37.5-fnos15")
            self.assertEqual(migration["make_latest"], "true")
            self.assertEqual(migration["should_build"], "true")
            self.assertEqual(plan("1.37.5", ["v1.37.5-fnos15"])["should_build"], "false")
            self.assertEqual(plan("1.37.5", ["v1.37.5-fnos15"], force="true")["should_build"], "true")
            self.assertEqual(plan("1.37.5", ["v1.37.5-fnos16"])["make_latest"], "false")
            self.assertEqual(plan("1.37.5", ["v1.37.5-fnos15"], event="schedule")["should_build"], "false")
            scheduled = plan("1.37.6", ["v1.37.5-fnos15"], event="schedule")
            self.assertEqual(scheduled["package_version"], "1.37.6515")
            self.assertEqual(scheduled["should_build"], "true")
            rc = plan("1.37.5", ["v1.37.5"], stable="false")
            self.assertEqual(rc["package_version"], "1.37.5015")
            self.assertEqual(rc["make_latest"], "false")
            self.assertEqual(rc["tag"], "v1.37.5-rc15")

            subprocess.run(["python3", str(root / "scripts/prepare-release.py"), "1.37.5", "15", "stable"], check=True, capture_output=True)
            self.assertIn("1.37.5515", manifest.read_text())
            metadata = json.loads((root / "packaging/vaultwarden/app/server/PACKAGE.json").read_text())
            self.assertEqual(metadata, dict(upstream_version="1.37.5", package_version="1.37.5515", revision=15, channel="stable"))


if __name__ == "__main__":
    unittest.main()
