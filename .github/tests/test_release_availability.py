import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).resolve().parents[1] / "check_release_availability.sh"
TAG = "sing-box-1.15-alpha.13-123456789"


class ReleaseAvailabilityTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.repo = Path(self.temp.name)
        self.git("init", "-q")
        self.commit("first")
        self.bin = self.repo / "bin"
        self.bin.mkdir()
        gh = self.bin / "gh"
        gh.write_text('#!/bin/sh\nif [ "${GH_FAIL:-}" = 1 ]; then exit 1; fi\nprintf "%s\\n" "${GH_TAGS:-}"\n')
        gh.chmod(0o755)

    def git(self, *args):
        return subprocess.run(["git", *args], cwd=self.repo, check=True, capture_output=True)

    def commit(self, msg):
        self.git("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-qm", msg)

    def check(self, **overrides):
        env = dict(os.environ, PATH=f"{self.bin}:{os.environ['PATH']}", TAG=TAG, REPO="test/repo")
        env.update(overrides)
        return subprocess.run(["bash", str(SCRIPT)], cwd=self.repo, env=env, capture_output=True, text=True)

    def test_available_and_same_commit_tag(self):
        self.assertEqual(self.check().returncode, 0)
        self.git("tag", TAG)
        self.assertEqual(self.check().returncode, 0)

    def test_reject_existing_release_and_api_failure(self):
        self.assertNotEqual(self.check(GH_TAGS=f"other\n{TAG}\nmore").returncode, 0)
        self.assertNotEqual(self.check(GH_FAIL="1").returncode, 0)

    def test_reject_tag_for_other_commit(self):
        self.git("tag", TAG)
        self.commit("second")
        self.assertNotEqual(self.check().returncode, 0)
