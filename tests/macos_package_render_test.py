import importlib.util
import plistlib
import subprocess
import tempfile
import unittest
from pathlib import Path
from xml.etree import ElementTree as ET

MODULE_PATH = Path(__file__).resolve().parent.parent / "macos-package" / "render.py"
spec = importlib.util.spec_from_file_location("macos_package_render", MODULE_PATH)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class PackageMetadataTests(unittest.TestCase):
    def test_product_metadata_and_installer_hooks(self):
        products = [
            {
                "application_name": "Adder",
                "bundle_id": "io.blinklabs.adder",
                "executable": "adder-tray",
                "icon_name": "adder",
                "version": "1.2.3",
                "architecture": "amd64",
                "binary_names": ["adder", "adder-tray"],
                "cli_symlink": "adder",
                "notification_alert_style": "alert",
                "organization": "io.blinklabs",
                "description": "Adder CLI and tray application",
            },
            {
                "application_name": "Bursa",
                "bundle_id": "com.blinklabssoftware.bursa",
                "executable": "bursa-wallet",
                "icon_name": "bursa",
                "version": "1.2.3",
                "architecture": "arm64",
                "binary_names": ["bursa-wallet"],
                "organization": "com.blinklabssoftware",
                "description": "Bursa Cardano full-node wallet",
            },
        ]
        for config in products:
            with (
                self.subTest(product=config["application_name"]),
                tempfile.TemporaryDirectory() as directory,
            ):
                output = Path(directory)
                module.render(config, output)
                with (output / "Info.plist").open("rb") as source:
                    plist = plistlib.load(source)
                self.assertEqual(plist["CFBundleExecutable"], config["executable"])
                self.assertEqual(plist["CFBundleIdentifier"], config["bundle_id"])
                self.assertEqual(plist["CFBundleShortVersionString"], "1.2.3")
                self.assertEqual(
                    plist.get("NSUserNotificationAlertStyle", ""),
                    config.get("notification_alert_style", ""),
                )
                xml = ET.parse(output / "distribution.xml").getroot()
                self.assertEqual(xml.find("title").text, config["application_name"])
                self.assertEqual(xml.find("organization").text, config["organization"])
                self.assertEqual(
                    xml.find("options").get("hostArchitectures"),
                    {"arm64": "arm64", "amd64": "x86_64"}[config["architecture"]],
                )
                self.assertEqual(
                    xml.find("volume-check/allowed-os-versions/os-version").get("min"), "11.0"
                )
                self.assertEqual(xml.find("pkg-ref").get("id"), config["bundle_id"])
                self.assertEqual(xml.find("pkg-ref").text, "component.pkg")
                postinstall = (output / "scripts/postinstall").read_text()
                if config.get("cli_symlink"):
                    self.assertIn("CLI_LINK=/usr/local/bin/adder", postinstall)
                    self.assertIn('[ ! -L "$CLI_LINK" ]', postinstall)
                    self.assertIn('"$(readlink "$CLI_LINK" 2>/dev/null)" = "$CLI_SRC"', postinstall)
                else:
                    self.assertNotIn("CLI_LINK", postinstall)
                for script in ("preinstall", "postinstall"):
                    subprocess.run(["bash", "-n", str(output / "scripts" / script)], check=True)

    def test_prerelease_uses_numeric_short_version(self):
        config = {
            "application_name": "Adder",
            "bundle_id": "io.blinklabs.adder",
            "executable": "adder",
            "icon_name": "adder",
            "version": "1.2.3-rc.1",
            "architecture": "arm64",
            "binary_names": ["adder"],
        }
        with tempfile.TemporaryDirectory() as directory:
            module.render(config, Path(directory))
            plist = plistlib.loads((Path(directory) / "Info.plist").read_bytes())
            self.assertEqual(plist["CFBundleShortVersionString"], "1.2.3")
            self.assertEqual(plist["CFBundleVersion"], "1.2.3-rc.1")
            self.assertIn(
                'pkill -f "^${_binary//./\\.}( |$)"',
                (Path(directory) / "scripts/preinstall").read_text(),
            )

    def test_installer_preserves_existing_cli_links(self):
        config = {
            "application_name": "Adder",
            "bundle_id": "io.blinklabs.adder",
            "executable": "adder-tray",
            "icon_name": "adder",
            "version": "1.2.3",
            "architecture": "arm64",
            "binary_names": ["adder", "adder-tray"],
            "cli_symlink": "adder",
        }
        for state in (
            "absent",
            "own",
            "foreign",
            "regular-file",
            "dangling",
            "missing",
            "link-failure",
        ):
            with self.subTest(state=state), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                output = root / "output"
                module.render(config, output)
                app = root / "Adder.app"
                binary = app / "Contents/MacOS/adder"
                binary.parent.mkdir(parents=True)
                if state != "missing":
                    binary.write_text("fixture")
                    binary.chmod(0o755)
                link = root / "bin/adder"
                link.parent.mkdir()
                foreign = root / "foreign"
                if state == "own":
                    link.symlink_to(binary)
                elif state == "foreign":
                    foreign.write_text("preserve")
                    link.symlink_to(foreign)
                elif state == "dangling":
                    link.symlink_to(foreign)
                elif state == "regular-file":
                    link.write_text("keep this file")
                script = (
                    (output / "scripts/postinstall")
                    .read_text()
                    .replace("/Applications/Adder.app", str(app))
                    .replace("/usr/local/bin/adder", str(link))
                    .replace("/usr/local/bin", str(link.parent))
                )
                if state == "link-failure":
                    failing_ln = root / "failing-ln"
                    failing_ln.write_text("#!/bin/sh\nexit 1\n")
                    failing_ln.chmod(0o755)
                    script = script.replace("/bin/ln", str(failing_ln))
                hook = root / "postinstall"
                hook.write_text(script)
                result = subprocess.run(
                    ["bash", str(hook)], text=True, capture_output=True, check=True
                )
                if state in ("absent", "own"):
                    self.assertEqual(link.resolve(), binary.resolve())
                elif state in ("foreign", "dangling"):
                    self.assertEqual(link.readlink(), foreign)
                    self.assertIn("leaving it untouched", result.stderr)
                elif state == "regular-file":
                    self.assertEqual(link.read_text(), "keep this file")
                    self.assertIn("leaving it untouched", result.stderr)
                elif state == "missing":
                    self.assertFalse(link.exists())
                    self.assertIn("CLI binary not found", result.stderr)
                else:
                    self.assertFalse(link.exists())
                    self.assertIn("could not create", result.stderr)

    def test_invalid_settings_fail_before_writing(self):
        config = {
            "application_name": "Adder",
            "bundle_id": "io.blinklabs.adder",
            "executable": "adder-tray",
            "icon_name": "adder",
            "version": "1.2.3",
            "architecture": "arm64",
            "binary_names": ["adder", "adder-tray"],
        }
        invalid = [
            {"application_name": "../other"},
            {"bundle_id": "id;rm"},
            {"architecture": "other"},
            {"version": "1/2"},
            {"binary_names": ["adder", "adder"]},
            {"binary_names": ["adder", "ADDER"]},
            {"cli_symlink": "missing"},
            {"notification_alert_style": "other"},
            {"cli_symlink": False},
            {"cli_symlink": None},
            {"cli_symlink": 0},
            {"unknown_setting": "value"},
        ]
        for replacement in invalid:
            with self.subTest(replacement=replacement), tempfile.TemporaryDirectory() as directory:
                path = Path(directory) / "output"
                with self.assertRaises(ValueError):
                    module.render(config | replacement, path)
                self.assertFalse(path.exists())


if __name__ == "__main__":
    unittest.main()
