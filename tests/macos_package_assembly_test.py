import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path

ASSEMBLER = Path(__file__).resolve().parent.parent / "macos-package" / "assemble.py"


class PackageAssemblyTests(unittest.TestCase):
    def run_case(self, product, signed, fail_command="", settings_update=None, fail_publish=""):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        workspace = root / "workspace"
        workspace.mkdir()
        bin_dir = root / "tools"
        bin_dir.mkdir()
        log = root / "commands.jsonl"
        stub = """#!/usr/bin/env python3
import json, os, pathlib, plistlib, sys
name = pathlib.Path(sys.argv[0]).name
with open(os.environ["COMMAND_LOG"], "a") as f:
    f.write(json.dumps([name] + sys.argv[1:]) + "\\n")
if os.environ.get("FAIL_COMMAND") == name:
    sys.exit(7)
if name == "pkgbuild" and "--analyze" in sys.argv:
    with open(sys.argv[-1], "wb") as f:
        plistlib.dump([{"BundleIsRelocatable": True}], f)
elif name == "pkgbuild":
    component = pathlib.Path(sys.argv[sys.argv.index("--component-plist") + 1])
    with component.open("rb") as f:
        assert plistlib.load(f)[0]["BundleIsRelocatable"] is False
    pathlib.Path(sys.argv[-1]).write_bytes(b"component")
elif name in ("productbuild", "productsign"):
    pathlib.Path(sys.argv[-1]).write_bytes(b"package")
"""
        for name in ("xattr", "codesign", "pkgbuild", "productbuild", "productsign", "pkgutil"):
            path = bin_dir / name
            path.write_text(stub)
            path.chmod(0o755)
        binary_names = ["adder", "adder-tray"] if product == "Adder" else ["bursa-wallet"]
        for name in binary_names:
            (workspace / name).write_bytes(b"binary")
        (workspace / "icon.icns").write_bytes(b"icon")
        config = {
            "metadata": {
                "application_name": product,
                "bundle_id": "io.example." + product.lower(),
                "executable": binary_names[-1],
                "icon_name": product.lower(),
                "version": "1.2.3",
                "architecture": "arm64",
                "binary_names": binary_names,
            },
            "binaries": {name: name for name in binary_names},
            "icon": "icon.icns",
        }
        if settings_update:
            settings_update(config, workspace)
        config_path = root / "config.json"
        config_path.write_text(json.dumps(config))
        (root / "sitecustomize.py").write_text("""import os
if os.environ.get('FAIL_PUBLISH'):
    def fail_link(source, destination, *args, **kwargs):
        if os.environ['FAIL_PUBLISH'] == 'compete':
            with open(destination, 'wb') as output:
                output.write(b'competing artifact')
        raise OSError('simulated publish failure')
    os.link = fail_link
""")
        action_output = root / "action-output"
        action_output.write_text("")
        env = os.environ | {
            "GITHUB_WORKSPACE": str(workspace),
            "GITHUB_OUTPUT": str(action_output),
            "PATH": str(bin_dir) + os.pathsep + os.environ["PATH"],
            "COMMAND_LOG": str(log),
            "FAIL_COMMAND": fail_command,
            "PYTHONDONTWRITEBYTECODE": "1",
            "PYTHONPATH": str(root),
            "FAIL_PUBLISH": fail_publish,
            "MACOS_APPLICATION_IDENTITY": "Application Identity",
            "MACOS_INSTALLER_IDENTITY": "Installer Identity",
        }
        args = ["python3", str(ASSEMBLER), str(config_path), "dist/release.pkg"]
        if signed:
            args.extend(["--mode", signed if isinstance(signed, str) else "signed"])
        result = subprocess.run(args, env=env, text=True, capture_output=True)
        calls = [json.loads(line) for line in log.read_text().splitlines()] if log.exists() else []
        expected_output = (
            f"package={(workspace / 'dist/release.pkg').resolve()}\n"
            if result.returncode == 0
            else ""
        )
        self.assertEqual(action_output.read_text(), expected_output)
        return result, calls, workspace

    def test_signed_packages_for_both_products(self):
        for product in ("Adder", "Bursa"):
            with self.subTest(product=product):
                result, calls, workspace = self.run_case(product, True)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual((workspace / "dist/release.pkg").read_bytes(), b"package")
                names = [call[0] for call in calls]
                self.assertEqual(names.count("codesign"), 4 if product == "Adder" else 3)
                self.assertLess(names.index("codesign"), names.index("pkgbuild"))
                self.assertLess(names.index("productbuild"), names.index("productsign"))
                self.assertLess(names.index("productsign"), names.index("pkgutil"))

    def test_unsigned_build_and_signing_failures(self):
        result, calls, workspace = self.run_case("Adder", False)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((workspace / "dist/release.pkg").is_file())
        self.assertFalse({"codesign", "productsign", "pkgutil"} & {call[0] for call in calls})
        for command in ("codesign", "pkgbuild", "productbuild", "productsign", "pkgutil"):
            with self.subTest(command=command):
                result, _, workspace = self.run_case("Adder", True, fail_command=command)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse((workspace / "dist/release.pkg").exists())

    def test_adhoc_signs_bundle_and_binaries(self):
        result, calls, workspace = self.run_case("Adder", "adhoc")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((workspace / "dist/release.pkg").is_file())
        codesign = [call for call in calls if call[0] == "codesign"]
        self.assertEqual(len(codesign), 4)
        self.assertTrue(all(call[1:4] == ["--force", "--sign", "-"] for call in codesign[:-1]))
        self.assertEqual(codesign[-1][1], "--verify")
        self.assertNotIn("productsign", [call[0] for call in calls])

    def test_missing_icon_is_allowed_only_for_development(self):
        def remove_icon(config, workspace):
            (workspace / "icon.icns").unlink()

        for mode in (False, "adhoc", True):
            with self.subTest(mode=mode):
                result, _, workspace = self.run_case("Adder", mode, settings_update=remove_icon)
                if mode is True:
                    self.assertNotEqual(result.returncode, 0)
                    self.assertFalse((workspace / "dist/release.pkg").exists())
                else:
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertIn("icon is absent", result.stderr)
                    self.assertTrue((workspace / "dist/release.pkg").is_file())

    def test_atomic_publish_failure_and_competitor(self):
        for failure in ("error", "compete"):
            with self.subTest(failure=failure):
                result, _, workspace = self.run_case("Adder", True, fail_publish=failure)
                self.assertNotEqual(result.returncode, 0)
                output = workspace / "dist/release.pkg"
                if failure == "compete":
                    self.assertEqual(output.read_bytes(), b"competing artifact")
                else:
                    self.assertFalse(output.exists())
                self.assertEqual(list((workspace / "dist").glob(".macos-package-*")), [])

    def test_rejects_unsafe_or_missing_inputs(self):
        def outside(config, workspace):
            config["binaries"]["adder"] = "../outside"

        def missing(config, workspace):
            config["binaries"]["adder"] = "missing"

        def preexisting(config, workspace):
            (workspace / "dist").mkdir()
            (workspace / "dist/release.pkg").write_bytes(b"keep")

        def newline_target(config, workspace):
            target = workspace / "directory\nwith-newline"
            target.mkdir()
            (target / "binary").write_bytes(b"binary")
            (workspace / "safe-link").symlink_to(target, target_is_directory=True)
            config["binaries"]["adder"] = "safe-link/binary"

        for change in (outside, missing, preexisting, newline_target):
            with self.subTest(change=change.__name__):
                result, calls, workspace = self.run_case("Adder", True, settings_update=change)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(calls, [])
                if change is preexisting:
                    self.assertEqual((workspace / "dist/release.pkg").read_bytes(), b"keep")


if __name__ == "__main__":
    unittest.main()
