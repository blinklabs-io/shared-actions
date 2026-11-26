import os
import subprocess
import tempfile
import unittest
import zipfile
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent.parent / "macos-package" / "sign-zip.py"


class CLIZipTests(unittest.TestCase):
    def run_case(
        self,
        fail="",
        binary="bursa",
        identifier="com.blinklabssoftware.bursa",
        existing=False,
        symlink=False,
    ):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        root = Path(temp.name)
        workspace = root / "workspace"
        workspace.mkdir()
        if symlink:
            (workspace / "bursa-real").write_bytes(b"unsigned")
            (workspace / "bursa").symlink_to("bursa-real")
        else:
            (workspace / "bursa").write_bytes(b"unsigned")
        tools = root / "tools"
        tools.mkdir()
        log = root / "calls"
        codesign = tools / "codesign"
        codesign.write_text("""#!/usr/bin/env python3
import os, pathlib, sys
with open(os.environ["COMMAND_LOG"], "a") as output:
    output.write("codesign " + " ".join(sys.argv[1:]) + "\\n")
if os.environ.get("FAIL_COMMAND") == "codesign" or (os.environ.get("FAIL_COMMAND") == "verify" and "--verify" in sys.argv):
    sys.exit(7)
if "--verify" not in sys.argv:
    pathlib.Path(sys.argv[-1]).write_bytes(b"signed")
""")
        codesign.chmod(0o755)
        zip_command = tools / "zip"
        zip_command.write_text("""#!/usr/bin/env python3
import os, sys, zipfile
with open(os.environ["COMMAND_LOG"], "a") as output:
    output.write("zip " + " ".join(sys.argv[1:]) + "\\n")
if os.environ.get("FAIL_COMMAND") == "zip":
    sys.exit(7)
with zipfile.ZipFile(sys.argv[2], "w") as archive:
    archive.write(sys.argv[3])
""")
        zip_command.chmod(0o755)
        if existing:
            (workspace / "release.zip").write_bytes(b"keep")
        output = root / "output"
        output.write_text("")
        env = os.environ | {
            "GITHUB_WORKSPACE": str(workspace),
            "GITHUB_OUTPUT": str(output),
            "MACOS_APPLICATION_IDENTITY": "Test Identity",
            "COMMAND_LOG": str(log),
            "FAIL_COMMAND": fail,
            "PATH": str(tools) + os.pathsep + os.environ["PATH"],
            "PYTHONDONTWRITEBYTECODE": "1",
        }
        result = subprocess.run(
            ["python3", str(SCRIPT), binary, "release.zip", identifier],
            env=env,
            text=True,
            capture_output=True,
        )
        calls = log.read_text() if log.exists() else ""
        return result, calls, workspace, output

    def test_archive_contains_signed_binary(self):
        result, calls, workspace, output = self.run_case()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((workspace / "bursa").read_bytes(), b"unsigned")
        with zipfile.ZipFile(workspace / "release.zip") as archive:
            self.assertEqual(archive.namelist(), ["bursa"])
            self.assertEqual(archive.read("bursa"), b"signed")
        self.assertLess(calls.index("codesign --force"), calls.index("codesign --verify"))
        self.assertLess(calls.index("codesign --verify"), calls.index("zip "))
        self.assertEqual(output.read_text(), f"archive={(workspace / 'release.zip').resolve()}\n")

    def test_symlinked_binary_keeps_configured_archive_name(self):
        result, _, workspace, _ = self.run_case(symlink=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        with zipfile.ZipFile(workspace / "release.zip") as archive:
            self.assertEqual(archive.namelist(), ["bursa"])
            self.assertEqual(archive.read("bursa"), b"signed")
        self.assertEqual((workspace / "bursa-real").read_bytes(), b"unsigned")

    def test_failures_do_not_publish_archive(self):
        for failure in ("codesign", "verify", "zip"):
            with self.subTest(failure=failure):
                result, _, workspace, output = self.run_case(fail=failure)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse((workspace / "release.zip").exists())
                self.assertEqual(output.read_text(), "")

    def test_invalid_inputs_and_existing_archive(self):
        for kwargs in ({"binary": "../outside"}, {"identifier": "bad;id"}, {"existing": True}):
            with self.subTest(kwargs=kwargs):
                result, calls, workspace, _ = self.run_case(**kwargs)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(calls, "")
                if kwargs.get("existing"):
                    self.assertEqual((workspace / "release.zip").read_bytes(), b"keep")


if __name__ == "__main__":
    unittest.main()
