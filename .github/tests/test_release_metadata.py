import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).resolve().parents[1] / "release_metadata.sh"


class ReleaseMetadataTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.repo = Path(self.tmp.name)
        (self.repo / "release").mkdir()
        (self.repo / "release" / "BASE_VERSION").write_text("1.15.0-alpha.13-xiaobaf14g.8\n")
        self.git("init", "-q")
        self.git("-c", "user.name=Test", "-c", "user.email=test@example.com",
                 "commit", "--allow-empty", "-qm", "initial")
        self.sha = self.git("rev-parse", "HEAD").stdout.decode().strip()[:9]

    def git(self, *args):
        return subprocess.run(["git", *args], cwd=self.repo, check=True, capture_output=True)

    def run_meta(self, **overrides):
        env = dict(os.environ, EVENT="workflow_dispatch", INPUT_VERSION="", INPUT_PRE="true",
                   INPUT_SKIP="false", INPUT_SKIP_GUI="true", REF_NAME="branch", RUN_ID="123",
                   RUN_ATTEMPT="1", GITHUB_OUTPUT=str(self.repo / "output"))
        env.update(overrides)
        result = subprocess.run(["bash", str(SCRIPT)], cwd=self.repo, env=env, text=True, capture_output=True)
        values = dict(line.split("=", 1) for line in result.stdout.splitlines() if "=" in line)
        return result, values

    def test_no_tag_uses_baseline_and_commit(self):
        result, values = self.run_meta()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(values["tag"], f"sing-box-1.15-alpha.13-{self.sha}")
        self.assertEqual(values["artifact_version"], f"1.15-alpha.13-{self.sha}")
        self.assertEqual(values["version"], "1.15.0-alpha.13")
        self.assertEqual(values["prerelease"], "true")
        _, retry = self.run_meta(RUN_ID="456", RUN_ATTEMPT="2")
        self.assertEqual(values["tag"], retry["tag"])

    def test_previous_auto_and_new_release_are_not_version_baselines(self):
        self.git("tag", "v0.0.0-auto.42.1")
        self.git("tag", f"sing-box-1.99-alpha.1-{self.sha}")
        _, values = self.run_meta()
        self.assertEqual(values["version"], "1.15.0-alpha.13")
        self.assertEqual(values["tag"], f"sing-box-1.15-alpha.13-{self.sha}")

    def test_invalid_base_fails(self):
        (self.repo / "release" / "BASE_VERSION").write_text("0.0.0\n")
        result, _ = self.run_meta()
        self.assertNotEqual(result.returncode, 0)

    def test_reachable_tag_and_manual_version(self):
        self.git("tag", "v1.15.0-alpha.13-xiaobaf14g.1")
        result, values = self.run_meta()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(values["version"], "1.15.0-alpha.13")
        _, values = self.run_meta(INPUT_VERSION="v2.3.4", INPUT_PRE="false")
        self.assertEqual(values["tag"], f"sing-box-2.3.4-{self.sha}")
        self.assertEqual(values["version"], "2.3.4")
        self.assertEqual(values["prerelease"], "false")

    def test_fork_and_auto_suffix_are_removed(self):
        result, values = self.run_meta(INPUT_VERSION="1.15.0-alpha.13-xiaobaf14g.8-auto.456.1")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(values["tag"], f"sing-box-1.15-alpha.13-{self.sha}")
        self.assertEqual(values["version"], "1.15.0-alpha.13")
        _, values = self.run_meta(EVENT="push", REF_NAME="v1.15.0-alpha.13-xiaobaf14g.8")
        self.assertEqual(values["tag"], f"sing-box-1.15-alpha.13-{self.sha}")
        self.assertEqual(values["version"], "1.15.0-alpha.13")
        _, values = self.run_meta(INPUT_VERSION="1.0.0.0-alpha.1")
        self.assertEqual(values["artifact_version"], f"1.0.0.0-alpha.1-{self.sha}")

    def test_release_name_tracks_new_commit(self):
        _, before = self.run_meta()
        self.git("-c", "user.name=Test", "-c", "user.email=test@example.com",
                 "commit", "--allow-empty", "-qm", "next")
        _, after = self.run_meta()
        self.assertNotEqual(before["tag"], after["tag"])
        self.assertEqual(before["version"], after["version"])

    def test_zero_patch_and_nonzero_patch(self):
        _, values = self.run_meta(INPUT_VERSION="2.3.0")
        self.assertEqual(values["artifact_version"], f"2.3-{self.sha}")
        self.assertEqual(values["version"], "2.3.0")
        _, values = self.run_meta(INPUT_VERSION="2.3.10-rc.1")
        self.assertEqual(values["artifact_version"], f"2.3.10-rc.1-{self.sha}")

    def test_invalid_manual_version_is_rejected(self):
        for version in ("bad/name", "bad..name", "bad@{name"):
            result, _ = self.run_meta(INPUT_VERSION=version)
            self.assertNotEqual(result.returncode, 0, version)

    def test_tag_trigger(self):
        result, values = self.run_meta(EVENT="push", REF_NAME="v1.2.3-beta.1")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(values["tag"], f"sing-box-1.2.3-beta.1-{self.sha}")
        self.assertEqual(values["version"], "1.2.3-beta.1")
        self.assertEqual(values["prerelease"], "true")
        self.assertEqual(values["skip_android"], "false")
        self.assertEqual(values["skip_gui"], "true")


if __name__ == "__main__":
    unittest.main()
