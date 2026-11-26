import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent.parent / "macos-release" / "compose.py"


class MacOSReleaseSettingsTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.output = self.root / "output"
        self.output.write_text("")
        self.config = {
            "artifact_type": "pkg",
            "artifact_basename": "adder",
            "build": {
                "binaries": [
                    {
                        "name": "adder",
                        "module": ".",
                        "package": "./cmd/adder",
                        "cgo": False,
                        "tags": ["nodbus"],
                    },
                    {
                        "name": "adder-tray",
                        "module": ".",
                        "package": "./cmd/adder-tray",
                        "cgo": True,
                        "tags": [],
                    },
                ]
            },
            "metadata": {
                "application_name": "Adder",
                "bundle_id": "io.blinklabs.adder",
                "executable": "adder-tray",
                "icon_name": "adder",
                "cli_symlink": "adder",
            },
            "icon": ".github/assets/Adder.icns",
        }

    def run_compose(
        self,
        phase,
        mode="signed",
        ref="refs/heads/main",
        version="1.2.3",
        directory="build/macos",
        arch="arm64",
    ):
        env = os.environ | {
            "MACOS_RELEASE_CONFIG": json.dumps(self.config),
            "MACOS_RELEASE_MODE": mode,
            "MACOS_RELEASE_ARCH": arch,
            "MACOS_BUILD_DIRECTORY": directory,
            "MACOS_BUILD_VERSION": version,
            "GITHUB_REF": ref,
            "GITHUB_OUTPUT": str(self.output),
            "PYTHONDONTWRITEBYTECODE": "1",
        }
        result = subprocess.run(
            ["python3", str(SCRIPT), phase], env=env, text=True, capture_output=True
        )
        values = dict(line.split("=", 1) for line in self.output.read_text().splitlines())
        return result, values

    def test_adder_package_settings(self):
        result, values = self.run_compose("prepare")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(values["artifact-type"], "pkg")
        self.assertEqual(json.loads(values["build-config"]), self.config["build"])
        self.assertRegex(values["build-directory"], r"^\.macos-release/[0-9a-f]{32}$")
        self.output.write_text("")
        result, values = self.run_compose("package")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(values["artifact-path"], "dist/adder-1.2.3-darwin-arm64.pkg")
        package = json.loads(values["package-config"])
        self.assertEqual(
            package["binaries"],
            {"adder": "build/macos/adder", "adder-tray": "build/macos/adder-tray"},
        )
        self.assertEqual(package["metadata"]["cli_symlink"], "adder")
        self.assertEqual(package["metadata"]["version"], "1.2.3")
        self.assertEqual(json.loads(values["metadata"]), package["metadata"])

    def test_bursa_wallet_package_settings(self):
        self.config = {
            "artifact_type": "pkg",
            "artifact_basename": "bursa-wallet",
            "build": {
                "binaries": [
                    {
                        "name": "bursa-wallet",
                        "module": "ui",
                        "package": "./cmd/bursa-wallet",
                        "cgo": True,
                        "tags": ["webview"],
                    }
                ],
                "web": {
                    "directory": "ui/web",
                    "dist": "ui/web/dist",
                    "embed": "ui/internal/webui/dist",
                    "reuse": False,
                    "placeholder": "Pre-build placeholder",
                },
            },
            "metadata": {
                "application_name": "Bursa",
                "bundle_id": "com.blinklabssoftware.bursa",
                "executable": "bursa-wallet",
                "icon_name": "bursa",
            },
            "icon": ".github/assets/Bursa.icns",
        }
        result, values = self.run_compose("package")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(values["artifact-path"], "dist/bursa-wallet-1.2.3-darwin-arm64.pkg")
        package = json.loads(values["package-config"])
        self.assertEqual(package["binaries"], {"bursa-wallet": "build/macos/bursa-wallet"})
        self.assertEqual(package["metadata"]["bundle_id"], "com.blinklabssoftware.bursa")

    def test_bursa_zip_preserves_version(self):
        self.config = {
            "artifact_type": "zip",
            "artifact_basename": "bursa",
            "build": {
                "binaries": [
                    {
                        "name": "bursa",
                        "module": ".",
                        "package": "./cmd/bursa",
                        "cgo": False,
                        "tags": [],
                    }
                ],
                "version_prefix": "preserve",
            },
            "cli_binary": "bursa",
            "signing_identifier": "com.blinklabssoftware.bursa",
        }
        result, values = self.run_compose("package", version="v1.2.3")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(values["binary-path"], "build/macos/bursa")
        self.assertEqual(values["artifact-path"], "dist/bursa-v1.2.3-darwin-arm64.zip")
        self.assertEqual(values["identifier"], "com.blinklabssoftware.bursa")

    def test_invalid_or_unsigned_tag_settings_fail(self):
        cases = [
            {"mode": "unsigned", "ref": "refs/tags/v1.2.3"},
            {"arch": "x86"},
            {"directory": "../outside", "phase": "package"},
            {"version": "bad/name", "phase": "package"},
        ]
        for case in cases:
            with self.subTest(case=case):
                self.output.write_text("")
                phase = case.get("phase", "prepare")
                kwargs = {key: value for key, value in case.items() if key != "phase"}
                result, values = self.run_compose(phase, **kwargs)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(values, {})
        self.config["unknown"] = True
        self.output.write_text("")
        result, values = self.run_compose("prepare")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(values, {})


if __name__ == "__main__":
    unittest.main()
