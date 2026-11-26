import importlib.util
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

PACKAGE_DIR = Path(__file__).resolve().parent.parent / "macos-package"
sys.path.insert(0, str(PACKAGE_DIR))
spec = importlib.util.spec_from_file_location("macos_package_verify", PACKAGE_DIR / "verify.py")
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class PackageVerificationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.apps = self.root / "Applications"
        self.apps.mkdir()
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.package = self.root / "test.pkg"
        self.package.write_bytes(b"package")
        self.metadata = {"application_name": "Adder", "bundle_id": "io.blinklabs.adder", "executable": "adder-tray", "icon_name": "adder", "version": "1.2.3", "architecture": "arm64", "binary_names": ["adder", "adder-tray"], "cli_symlink": "adder"}
        self.calls = []
        self.fail = ""
        self.fails = set()
        self.receipt = False
        self.app = self.apps / "Adder.app"
        self.original_apps = module.APPLICATIONS
        self.original_bin = module.LOCAL_BIN
        module.APPLICATIONS = self.apps
        module.LOCAL_BIN = self.bin
        self.addCleanup(setattr, module, "APPLICATIONS", self.original_apps)
        self.addCleanup(setattr, module, "LOCAL_BIN", self.original_bin)
        patcher = patch.object(module, "command", side_effect=self.fake_command)
        patcher.start()
        self.addCleanup(patcher.stop)
        sleeper = patch.object(module.time, "sleep")
        sleeper.start()
        self.addCleanup(sleeper.stop)

    def fake_command(self, *args, check=True):
        self.calls.append(args)
        label = " ".join(args[:2])
        full_label = " ".join(args[:3])
        if self.fail == "interrupt" and label == "codesign --verify":
            raise KeyboardInterrupt()
        if label == self.fail or full_label == self.fail or label in self.fails or full_label in self.fails:
            if check:
                raise subprocess.CalledProcessError(7, args)
            return subprocess.CompletedProcess(args, 7, "failure")
        if args[:2] == ("pkgutil", "--check-signature"):
            return subprocess.CompletedProcess(args, 0, "Developer ID Installer")
        if args[0] == "spctl":
            return subprocess.CompletedProcess(args, 0, "accepted\nNotarized Developer ID")
        if args[:2] == ("pkgutil", "--pkgs"):
            return subprocess.CompletedProcess(args, 0, self.metadata["bundle_id"] + "\n" if self.receipt else "")
        if args[:2] == ("pkgutil", "--pkg-info"):
            if not self.receipt:
                if check:
                    raise subprocess.CalledProcessError(1, args)
                return subprocess.CompletedProcess(args, 1, "missing")
        if args[:2] == ("sudo", "installer"):
            binaries = self.app / "Contents/MacOS"
            binaries.mkdir(parents=True)
            for name in self.metadata["binary_names"]:
                binary = binaries / name
                binary.write_text("binary")
                binary.chmod(0o755)
            link = self.bin / "adder"
            if not link.exists() and not link.is_symlink():
                link.symlink_to(binaries / "adder")
            self.receipt = True
        if args[:3] == ("sudo", "rm", "-f"):
            Path(args[3]).unlink()
        if args[:3] == ("sudo", "rm", "-rf"):
            import shutil
            shutil.rmtree(args[3], ignore_errors=True)
        if args[:3] == ("sudo", "pkgutil", "--forget"):
            self.receipt = False
        return subprocess.CompletedProcess(args, 0, "arm64")

    def test_signature_and_install(self):
        module.verify(self.package, self.metadata, True)
        self.assertFalse(self.app.exists())
        self.assertFalse((self.bin / "adder").is_symlink())
        self.assertFalse(self.receipt)
        self.assertEqual(sum(call[:2] == ("lipo", "-archs") for call in self.calls), 2)

    def test_signature_failures_stop_before_install(self):
        for failure in ("pkgutil --check-signature", "spctl -a", "xcrun stapler"):
            with self.subTest(failure=failure):
                self.calls.clear()
                self.fail = failure
                with self.assertRaises(ValueError):
                    module.verify(self.package, self.metadata, True)
                self.assertFalse(any(call[:2] == ("sudo", "installer") for call in self.calls))

    def test_existing_install_or_receipt_are_preserved(self):
        self.app.mkdir()
        with self.assertRaisesRegex(ValueError, "already exists"):
            module.verify(self.package, self.metadata, True)
        self.assertTrue(self.app.exists())
        self.app.rmdir()
        self.receipt = True
        with self.assertRaisesRegex(ValueError, "receipt already exists"):
            module.verify(self.package, self.metadata, True)
        self.assertTrue(self.receipt)
        self.assertFalse(any(call[:2] == ("sudo", "installer") for call in self.calls))

    def test_failure_after_install_cleans_up(self):
        self.fail = "codesign --verify"
        with self.assertRaises(subprocess.CalledProcessError):
            module.verify(self.package, self.metadata, True)
        self.assertFalse(self.app.exists())
        self.assertFalse((self.bin / "adder").is_symlink())
        self.assertFalse(self.receipt)

    def test_interruption_cleans_up_installation(self):
        self.fail = "interrupt"
        with self.assertRaises(KeyboardInterrupt):
            module.verify(self.package, self.metadata, True)
        self.assertFalse(self.app.exists())
        self.assertFalse((self.bin / "adder").is_symlink())
        self.assertFalse(self.receipt)

    def test_installer_failure_preserves_original_error(self):
        self.fail = "sudo installer"
        with self.assertRaises(subprocess.CalledProcessError):
            module.verify(self.package, self.metadata, True)
        self.assertFalse(any(call[:3] == ("sudo", "pkgutil", "--forget") for call in self.calls))
        self.assertTrue(any(call[:3] == ("sudo", "rm", "-rf") for call in self.calls))

    def test_receipt_lookup_failure_prevents_installation(self):
        self.fail = "pkgutil --pkgs"
        with self.assertRaises(subprocess.CalledProcessError):
            module.verify(self.package, self.metadata, True)
        self.assertFalse(any(call[:2] == ("sudo", "installer") for call in self.calls))

    def test_cleanup_failures_do_not_skip_later_cleanup(self):
        for failure in ("sudo rm -f", "sudo rm -rf", "sudo pkgutil --forget"):
            with self.subTest(failure=failure):
                self.calls.clear()
                self.fail = failure
                with self.assertRaisesRegex(ValueError, "cleanup failed"):
                    module.verify(self.package, self.metadata, True)
                self.assertTrue(any(call[:3] == ("sudo", "rm", "-rf") for call in self.calls))
                self.assertTrue(any(call[:3] == ("sudo", "pkgutil", "--forget") for call in self.calls))
                self.fail = ""
                import shutil
                shutil.rmtree(self.app, ignore_errors=True)
                (self.bin / "adder").unlink(missing_ok=True)
                self.receipt = False

    def test_primary_and_cleanup_failures_are_both_reported(self):
        self.fails = {"codesign --verify", "sudo rm -f"}
        with self.assertRaisesRegex(ValueError, "CLI link cleanup failed") as error:
            module.verify(self.package, self.metadata, True)
        self.assertIn("codesign", str(error.exception))
        self.assertFalse(self.app.exists())
        self.assertFalse(self.receipt)

    def test_existing_foreign_cli_link_is_preserved(self):
        foreign = self.root / "foreign"
        link = self.bin / "adder"
        link.symlink_to(foreign)
        module.verify(self.package, self.metadata, True)
        self.assertEqual(link.readlink(), foreign)
        self.assertFalse(any(call[:3] == ("sudo", "rm", "-f") for call in self.calls))

    def test_signature_only_never_installs(self):
        module.verify(self.package, self.metadata, False)
        self.assertFalse(any(call[0] == "sudo" for call in self.calls))


if __name__ == "__main__":
    unittest.main()
