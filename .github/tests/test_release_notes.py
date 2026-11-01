import importlib.util
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("release_notes", Path(__file__).resolve().parents[1] / "release_notes.py")
notes = importlib.util.module_from_spec(spec)
spec.loader.exec_module(notes)


class ReleaseNotesTest(unittest.TestCase):
    def test_scan_new_and_legacy_core_names_and_clients(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp)
            version = "1.15-alpha.13-149ad84c8"
            for name in (
                f"sing-box-{version}-android_arm64.tar.gz",
                f"sing-box-{version}-android-arm64-v8a.tar.gz",
                f"sing-box-{version}-android-arm64-v8a-ebpf-easytier.tar.gz",
                f"sing-box-{version}-linux_amd64-v3-glibc.tar.gz",
                f"sing-box-{version}-windows-amd64.zip",
                f"SFA-{version}-universal.apk",
                f"SFA-{version}-legacy-android-5-universal.apk",
                f"SFW-{version}-x64.exe",
                f"SFL-{version}-x64.deb",
                "SFA-version-metadata.json",
            ):
                (path / name).touch()
            found = notes.scan_builds(path, version)
            self.assertEqual(found["core"]["android"], {"arm64", "arm64-v8a"})
            self.assertEqual(found["core"]["linux"], {"amd64"})
            self.assertEqual(found["core"]["windows"], {"amd64"})
            self.assertEqual(found["variants"], {"ebpf", "easytier", "v3", "glibc"})
            self.assertEqual(found["sfa"], {"universal"})
            self.assertEqual(found["sfa_legacy"], {"universal"})
            self.assertEqual(found["sfw"], {"x64"})
            self.assertTrue(found["sfa_metadata"])
            self.assertEqual(notes.sfl_formats(found["sfl"]), ["deb"])

    def test_release_notes_use_semver_for_description_and_hashed_artifact_names(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            subprocess.run(["git", "init", "-q"], cwd=root, check=True)
            subprocess.run(["git", "-c", "user.name=Test", "-c", "user.email=test@example.com",
                            "commit", "--allow-empty", "-qm", "feat: example"], cwd=root, check=True)
            builds = root / "builds"
            builds.mkdir()
            (builds / "sing-box-1.15-alpha.13-149ad84c8-android-arm64-v8a.tar.gz").touch()
            env = dict(os.environ, TAG="sing-box-1.15-alpha.13-149ad84c8", VERSION="1.15.0-alpha.13",
                       ARTIFACT_VERSION="1.15-alpha.13-149ad84c8", BUILDS_DIR=str(builds),
                       IS_PRE="true", SKIP_ANDROID="false", SKIP_GUI="true", ANDROID_RES="success",
                       EXPERIMENTAL_RES="success", ANDROID_EXPERIMENTAL="success", PREV="")
            result = subprocess.run(["python3", str(spec.origin)], cwd=root, env=env,
                                    capture_output=True, text=True, check=True)
            self.assertIn("基于 SagerNet **1.15.0-alpha.13**", result.stdout)
            self.assertIn("| **Android** | arm64-v8a |", result.stdout)

    def test_resolve_previous_new_tag_and_legacy_fallback(self):
        with tempfile.TemporaryDirectory() as tmp, patch.dict(os.environ, {"PREV": ""}):
            def git(*args):
                return subprocess.run(["git", *args], cwd=tmp, check=True, capture_output=True, text=True).stdout.strip()
            git("init", "-q")
            git("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-qm", "first")
            git("tag", "v1.15.0-alpha.13-xiaobaf14g.8-auto.42.1")
            git("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-qm", "second")
            original_cwd = os.getcwd()
            try:
                os.chdir(tmp)
                _, previous = notes.resolve_range("sing-box-future")
                self.assertEqual(previous, "v1.15.0-alpha.13-xiaobaf14g.8-auto.42.1")
                git("tag", "sing-box-1.15-alpha.13-123456789")
                git("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-qm", "third")
                _, previous = notes.resolve_range("sing-box-future")
                self.assertEqual(previous, "sing-box-1.15-alpha.13-123456789")
            finally:
                os.chdir(original_cwd)


if __name__ == "__main__":
    unittest.main()
