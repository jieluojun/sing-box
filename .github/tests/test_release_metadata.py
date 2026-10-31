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
        self.git("init", "-q")
        self.git("-c", "user.name=Test", "-c", "user.email=test@example.com",
                 "commit", "--allow-empty", "-qm", "initial")

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

    def test_no_tag_uses_unique_fallback(self):
        result, values = self.run_meta()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(values["tag"], "v0.0.0-auto.123.1")
        self.assertEqual(values["prerelease"], "true")
        _, retry = self.run_meta(RUN_ATTEMPT="2")
        self.assertNotEqual(values["tag"], retry["tag"])

    def test_reachable_tag_and_manual_version(self):
        self.git("tag", "v1.15.0-alpha.13-xiaobaf14g.1")
        result, values = self.run_meta()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(values["version"], "1.15.0-alpha.13-xiaobaf14g.1-auto.123.1")
        _, values = self.run_meta(INPUT_VERSION="v2.3.4", INPUT_PRE="false")
        self.assertEqual(values["tag"], "v2.3.4")
        self.assertEqual(values["prerelease"], "false")

    def test_invalid_manual_version_is_rejected(self):
        result, _ = self.run_meta(INPUT_VERSION="bad/name")
        self.assertNotEqual(result.returncode, 0)

    def test_tag_trigger_unchanged(self):
        result, values = self.run_meta(EVENT="push", REF_NAME="v1.2.3-beta.1")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(values["version"], "1.2.3-beta.1")
        self.assertEqual(values["prerelease"], "true")
        self.assertEqual(values["skip_android"], "false")
        self.assertEqual(values["skip_gui"], "true")


if __name__ == "__main__":
    unittest.main()
