import os
import shutil
from pathlib import Path
import subprocess
import tarfile
import tempfile
import textwrap
import unittest

WORKFLOW = Path(__file__).resolve().parents[1] / "workflows" / "xiaobaf14g-release.yml"
ARTIFACT_VERSION = "1.15-alpha.13-149ad84c8"
VERSION = "1.15.0-alpha.13"


def step_script(job, name):
    lines = WORKFLOW.read_text().splitlines()
    job_marker = f"  {job}:"
    start = lines.index(job_marker)
    end = next((i for i in range(start + 1, len(lines)) if lines[i].startswith("  ") and not lines[i].startswith("    ") and lines[i].endswith(":")), len(lines))
    step = next(i for i in range(start, end) if lines[i] == f"      - name: {name}")
    run = next(i for i in range(step, end) if lines[i].startswith("        run: |"))
    finish = next((i for i in range(run + 1, end) if lines[i] and not lines[i].startswith("          ")), end)
    return textwrap.dedent("\n".join(lines[run + 1:finish])) + "\n"


class ReleasePackagingTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.dir = Path(self.tmp.name)
        self.bin = self.dir / "bin"
        self.bin.mkdir()
        (self.dir / "release").mkdir()
        (self.dir / "release" / "LDFLAGS").write_text("")
        (self.dir / "release" / "DEFAULT_BUILD_TAGS_OTHERS").write_text("with_quic,with_naive_outbound,with_clash_api\n")
        (self.bin / "go").write_text('''#!/usr/bin/env bash
set -eu
printf '%s\\n' "$*" >> "$PWD/go-log"
if [ "$1" = run ]; then
  printf binary > sing-box-android-arm64-v8a
else
  while [ "$#" -gt 0 ]; do
    if [ "$1" = -o ]; then shift; printf binary > "$1"; break; fi
    shift
  done
fi
''')
        (self.bin / "pigz").write_text('#!/usr/bin/env bash\nexec gzip "$@"\n')
        for tool in self.bin.iterdir():
            tool.chmod(0o755)
        self.env = dict(os.environ, PATH=f"{self.bin}:{os.environ['PATH']}", VERSION=VERSION,
                        ARTIFACT_VERSION=ARTIFACT_VERSION, CGO_ENABLED="0", GOOS="linux",
                        GOARCH="arm64", GOAMD64="", CC_TARGET="", TAGS_SRC="others",
                        EBPF="true", SUFFIX="", EXT="", FEATURE="easytier", FEATURE_TAG="with_easytier")

    def run_step(self, job, step, **env):
        result = subprocess.run(["bash", "-e"], input=step_script(job, step), text=True,
                                cwd=self.dir, env=dict(self.env, **env), capture_output=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def assert_archive(self, name):
        archive = self.dir / "dist" / f"{name}.tar.gz"
        self.assertTrue(archive.exists(), archive)
        with tarfile.open(archive, "r:gz") as tar:
            self.assertEqual(tar.getnames(), [name])
            self.assertEqual(tar.extractfile(name).read(), b"binary")

    def test_desktop_and_experimental_binary_names(self):
        self.run_step("desktop", "Build binaries")
        self.run_step("desktop", "Pack with tar.gz")
        self.assert_archive(f"sing-box-{ARTIFACT_VERSION}-linux_arm64")
        self.assert_archive(f"sing-box-{ARTIFACT_VERSION}-linux_arm64-ebpf")
        self.run_step("experimental", "Build binary")
        self.run_step("experimental", "Pack with tar.gz")
        self.assert_archive(f"sing-box-{ARTIFACT_VERSION}-linux_arm64-easytier")
        self.assertIn(f"constant.Version={VERSION}", (self.dir / "go-log").read_text())

    def test_android_normal_and_experimental_archive_names(self):
        ndk = self.dir / "ndk" / "toolchains/llvm/prebuilt/linux-x86_64/bin"
        ndk.mkdir(parents=True)
        strip = ndk / "llvm-strip"
        strip.write_text("#!/bin/sh\nexit 0\n")
        strip.chmod(0o755)
        env = {"ANDROID_NDK_HOME": str(self.dir / "ndk")}
        self.run_step("android", "Build via build_libbox", **env)
        self.run_step("android", "Build experimental variants via build_libbox", **env)
        for suffix in ("", "-ebpf", "-easytier", "-ebpf-easytier"):
            self.assert_archive(f"sing-box-{ARTIFACT_VERSION}-android_arm64{suffix}")
        self.assertIn(f"-version {VERSION}", (self.dir / "go-log").read_text())

    @unittest.skipUnless(shutil.which("pwsh"), "PowerShell not installed")
    def test_sfw_package_renaming(self):
        release = self.dir / "clients/desktop/release"
        release.mkdir(parents=True)
        (release / f"SFW-{VERSION}-x64.exe").write_text("installer")
        script = step_script("sfw", "Prepare upload").replace("${{ matrix.desktop_arch }}", "x64")
        result = subprocess.run(["pwsh", "-NoProfile", "-Command", "$ErrorActionPreference = 'Stop';\n" + script],
                                text=True, cwd=self.dir, env=self.env, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertTrue((self.dir / "dist" / f"SFW-{ARTIFACT_VERSION}-x64.exe").exists())

    def test_sfa_and_sfl_package_renaming(self):
        apk = self.dir / "clients/android/app/build/outputs/apk/other/release"
        apk.mkdir(parents=True)
        (apk / f"SFA-{VERSION}-universal.apk").write_text("apk")
        self.run_step("sfa", "Prepare upload", VARIANT="other", OUTPUT="app/build/outputs/apk/other/release/*.apk", VERSION_CODE="99")
        self.assertTrue((self.dir / "dist" / f"SFA-{ARTIFACT_VERSION}-universal.apk").exists())
        self.assertIn(f'"version_name": "{VERSION}"', (self.dir / "dist/SFA-version-metadata.json").read_text())
        release = self.dir / "clients/desktop/release"
        release.mkdir(parents=True)
        for ext in ("deb", "rpm", "pkg.tar.zst"):
            (release / f"SFL-{VERSION}-x64.{ext}").write_text("package")
        self.run_step("sfl", "Prepare upload")
        for ext in ("deb", "rpm", "pkg.tar.zst"):
            self.assertTrue((self.dir / "dist" / f"SFL-{ARTIFACT_VERSION}-x64.{ext}").exists())


if __name__ == "__main__":
    unittest.main()
