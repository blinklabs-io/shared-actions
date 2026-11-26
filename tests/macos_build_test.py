import importlib.util
import json
import os
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent.parent / "macos-package" / "build.py"
sys.path.insert(0, str(SCRIPT.parent))
spec = importlib.util.spec_from_file_location("macos_build", SCRIPT)
build_module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(build_module)


class MacOSBuildTests(unittest.TestCase):
    def run_case(
        self,
        product,
        fail="",
        invalid=None,
        placeholder=False,
        reuse=True,
        output_path="build/shared-macos",
        version="v1.2.3",
    ):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        workspace = root / "workspace"
        workspace.mkdir()
        (workspace / "go.mod").write_text("module example.com/adder\n")
        for name in ("adder", "adder-tray"):
            (workspace / "cmd" / name).mkdir(parents=True)
        ui = workspace / "ui"
        ui.mkdir()
        (ui / "go.mod").write_text("module example.com/bursa/ui\n")
        (ui / "cmd/bursa-wallet").mkdir(parents=True)
        web = ui / "web"
        (web / "dist").mkdir(parents=True)
        (web / "dist/index.html").write_text(
            "Pre-build placeholder" if placeholder else "real web app"
        )
        (web / "node_modules").mkdir()
        (web / "node_modules/keep").write_text("developer dependencies")
        embed = ui / "internal/webui/dist"
        embed.mkdir(parents=True)
        (embed / "index.html").write_text("original developer content")
        (embed / "other.txt").write_text("keep me")
        tools = root / "tools"
        tools.mkdir()
        log = root / "calls.jsonl"
        stub = """#!/usr/bin/env python3
import json, os, pathlib, sys
name = pathlib.Path(sys.argv[0]).name
if name == "go" and sys.argv[1:] == ["mod", "edit", "-json"]:
    import re
    match = re.search(r'^module\\s+([^\\s]+)', pathlib.Path("go.mod").read_text(), re.MULTILINE)
    print(json.dumps({"Module": {"Path": match.group(1).strip('"')}}))
    sys.exit(0)
with open(os.environ["BUILD_LOG"], "a") as output:
    output.write(json.dumps({"tool": name, "args": sys.argv[1:], "cwd": os.getcwd(), "cgo": os.environ.get("CGO_ENABLED"), "goos": os.environ.get("GOOS"), "arch": os.environ.get("GOARCH")}) + "\\n")
if name == "git":
    if os.environ.get("FAIL_BUILD") == "git":
        sys.exit(7)
    print("v1.2.3-2-gabcdef1" if sys.argv[1] == "describe" else "abcdef1")
elif name == "go":
    if os.environ.get("FAIL_BUILD") == "go":
        sys.exit(7)
    if "bursa-wallet" in sys.argv[-1]:
        assert pathlib.Path("internal/webui/dist/index.html").read_text() == "real web app"
    pathlib.Path(sys.argv[sys.argv.index("-o") + 1]).write_bytes(b"binary")
    if os.environ.get("FAIL_BUILD") == "publish":
        destination = pathlib.Path(os.environ["GITHUB_WORKSPACE"]) / "build/shared-macos"
        destination.mkdir(parents=True, exist_ok=True)
        (destination / "competitor").write_text("keep")
elif name == "npm":
    if os.environ.get("FAIL_BUILD") == "npm":
        sys.exit(7)
    if sys.argv[1:] == ["run", "build"]:
        pathlib.Path("dist").mkdir()
        pathlib.Path("dist/index.html").write_text("real web app")
"""
        for name in ("git", "go", "npm"):
            path = tools / name
            path.write_text(stub)
            path.chmod(0o755)
        if product == "Adder":
            config = {
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
            }
        else:
            config = {
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
                    "reuse": reuse,
                    "placeholder": "Pre-build placeholder",
                },
            }
        if invalid:
            invalid(config, workspace)
        config_path = root / "config.json"
        config_path.write_text(json.dumps(config))
        output = root / "output"
        output.write_text("")
        env = os.environ | {
            "GITHUB_WORKSPACE": str(workspace),
            "GITHUB_OUTPUT": str(output),
            "PATH": str(tools) + os.pathsep + os.environ["PATH"],
            "BUILD_LOG": str(log),
            "FAIL_BUILD": fail,
            "PYTHONDONTWRITEBYTECODE": "1",
        }
        result = subprocess.run(
            ["python3", str(SCRIPT), str(config_path), output_path, version, "arm64"],
            env=env,
            text=True,
            capture_output=True,
        )
        calls = [json.loads(line) for line in log.read_text().splitlines()] if log.exists() else []
        self.assertEqual((embed / "index.html").read_text(), "original developer content")
        self.assertEqual((embed / "other.txt").read_text(), "keep me")
        self.assertEqual(
            (web / "dist/index.html").read_text(),
            "Pre-build placeholder" if placeholder else "real web app",
        )
        self.assertEqual((web / "node_modules/keep").read_text(), "developer dependencies")
        return result, calls, workspace, output

    def test_adder_build_flags(self):
        result, calls, workspace, output = self.run_case("Adder")
        self.assertEqual(result.returncode, 0, result.stderr)
        builds = [call for call in calls if call["tool"] == "go"]
        self.assertEqual([call["cgo"] for call in builds], ["0", "1"])
        self.assertTrue(
            all(call["goos"] == "darwin" and call["arch"] == "arm64" for call in builds)
        )
        self.assertIn("nodbus", builds[0]["args"])
        self.assertIn(
            "example.com/adder/internal/version.Version=1.2.3", " ".join(builds[0]["args"])
        )
        self.assertIn(
            "example.com/adder/internal/version.CommitHash=abcdef1", " ".join(builds[0]["args"])
        )
        self.assertEqual((workspace / "build/shared-macos/adder").read_bytes(), b"binary")
        self.assertEqual((workspace / "build/shared-macos/adder-tray").read_bytes(), b"binary")
        self.assertEqual(output.read_text(), "directory=build/shared-macos\nversion=1.2.3\n")

    def test_version_defaults_to_git_description(self):
        result, calls, _, _ = self.run_case("Adder", version="")
        self.assertEqual(result.returncode, 0, result.stderr)
        builds = [call for call in calls if call["tool"] == "go"]
        self.assertIn(
            "example.com/adder/internal/version.Version=1.2.3-2-gabcdef1",
            " ".join(builds[0]["args"]),
        )

    def test_build_without_git_metadata_uses_local_fallbacks(self):
        result, calls, _, _ = self.run_case("Adder", version="", fail="git")
        self.assertEqual(result.returncode, 0, result.stderr)
        builds = [call for call in calls if call["tool"] == "go"]
        self.assertIn(
            "example.com/adder/internal/version.Version=0.0.0", " ".join(builds[0]["args"])
        )
        self.assertIn(
            "example.com/adder/internal/version.CommitHash=unknown", " ".join(builds[0]["args"])
        )

    def test_version_prefix_can_be_preserved_for_cli(self):
        def preserve(config, workspace):
            config["version_prefix"] = "preserve"

        result, calls, _, _ = self.run_case("Adder", invalid=preserve)
        self.assertEqual(result.returncode, 0, result.stderr)
        builds = [call for call in calls if call["tool"] == "go"]
        self.assertIn(
            "example.com/adder/internal/version.Version=v1.2.3", " ".join(builds[0]["args"])
        )

    def test_bursa_web_bundle_build_and_restore(self):
        for reuse in (True, False):
            with self.subTest(reuse=reuse):
                result, calls, workspace, _ = self.run_case("Bursa", reuse=reuse)
                self.assertEqual(result.returncode, 0, result.stderr)
                builds = [call for call in calls if call["tool"] == "go"]
                self.assertEqual(len(builds), 1)
                self.assertEqual(builds[0]["cgo"], "1")
                self.assertIn("webview", builds[0]["args"])
                self.assertIn(
                    "example.com/bursa/ui/internal/version.Version=1.2.3",
                    " ".join(builds[0]["args"]),
                )
                self.assertEqual(sum(call["tool"] == "npm" for call in calls), 0 if reuse else 2)
                self.assertTrue((workspace / "build/shared-macos/bursa-wallet").is_file())

    def test_failure_restores_web_and_leaves_no_output(self):
        for fail, placeholder in (("go", False), ("npm", False), ("", True)):
            with self.subTest(fail=fail, placeholder=placeholder):
                result, _, workspace, output = self.run_case(
                    "Bursa", fail=fail, placeholder=placeholder, reuse=fail != "npm"
                )
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse((workspace / "build/shared-macos").exists())
                self.assertEqual(output.read_text(), "")

    def test_build_output_feeds_package_and_cli_actions(self):
        result, _, workspace, output = self.run_case("Adder")
        self.assertEqual(result.returncode, 0, result.stderr)
        directory = output.read_text().splitlines()[0].split("=", 1)[1]
        root = workspace.parent
        tools = root / "tools"
        stub = """#!/usr/bin/env python3
import pathlib, plistlib, sys
name = pathlib.Path(sys.argv[0]).name
if name == 'pkgbuild' and '--analyze' in sys.argv:
    with open(sys.argv[-1], 'wb') as output:
        plistlib.dump([{'BundleIsRelocatable': True}], output)
elif name == 'pkgbuild' or name == 'productbuild':
    pathlib.Path(sys.argv[-1]).write_bytes(b'package')
"""
        for name in ("xattr", "pkgbuild", "productbuild", "codesign"):
            path = tools / name
            path.write_text(stub)
            path.chmod(0o755)
        icon = workspace / "adder.icns"
        icon.write_bytes(b"icon")
        package_config = {
            "metadata": {
                "application_name": "Adder",
                "bundle_id": "io.blinklabs.adder",
                "executable": "adder-tray",
                "icon_name": "adder",
                "version": "1.2.3",
                "architecture": "arm64",
                "binary_names": ["adder", "adder-tray"],
                "cli_symlink": "adder",
            },
            "binaries": {"adder": directory + "/adder", "adder-tray": directory + "/adder-tray"},
            "icon": "adder.icns",
        }
        config_path = root / "package-config.json"
        config_path.write_text(json.dumps(package_config))
        env = os.environ | {
            "GITHUB_WORKSPACE": str(workspace),
            "PATH": str(tools) + os.pathsep + os.environ["PATH"],
            "PYTHONDONTWRITEBYTECODE": "1",
        }
        env.pop("GITHUB_OUTPUT", None)
        assembler = SCRIPT.parent / "assemble.py"
        package = subprocess.run(
            ["python3", str(assembler), str(config_path), "dist/adder.pkg"],
            env=env,
            text=True,
            capture_output=True,
        )
        self.assertEqual(package.returncode, 0, package.stderr)
        self.assertEqual((workspace / "dist/adder.pkg").read_bytes(), b"package")
        zip_script = SCRIPT.parent / "sign-zip.py"
        env["MACOS_APPLICATION_IDENTITY"] = "Test Identity"
        archive = subprocess.run(
            [
                "python3",
                str(zip_script),
                directory + "/adder",
                "dist/adder.zip",
                "io.blinklabs.adder",
            ],
            env=env,
            text=True,
            capture_output=True,
        )
        self.assertEqual(archive.returncode, 0, archive.stderr)
        self.assertTrue((workspace / "dist/adder.zip").is_file())

    def test_competing_publisher_is_preserved(self):
        result, _, workspace, output = self.run_case("Adder", fail="publish")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((workspace / "build/shared-macos/competitor").read_text(), "keep")
        self.assertEqual(list((workspace / "build").glob(".macos-binaries-*")), [])
        self.assertEqual(output.read_text(), "")

    def test_binary_output_cannot_be_inside_web_embed(self):
        result, calls, _, output = self.run_case(
            "Bursa", output_path="ui/internal/webui/dist/output"
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any(call["tool"] in ("go", "npm") for call in calls))
        self.assertEqual(output.read_text(), "")

    def test_concurrent_web_build_does_not_replace_original_assets(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            workspace = root / "workspace"
            workspace.mkdir()
            ui = workspace / "ui"
            (ui / "cmd/bursa-wallet").mkdir(parents=True)
            (ui / "go.mod").write_text("module example.com/bursa/ui\n")
            web = ui / "web/dist"
            web.mkdir(parents=True)
            (web / "index.html").write_text("real web app")
            embed = ui / "internal/webui/dist"
            embed.mkdir(parents=True)
            (embed / "index.html").write_text("original developer content")
            tools = root / "tools"
            tools.mkdir()
            (tools / "git").write_text("#!/bin/sh\nprintf 'abcdef1\n'\n")
            (tools / "git").chmod(0o755)
            (tools / "go").write_text("""#!/usr/bin/env python3
import json, os, pathlib, sys, time
if sys.argv[1:] == ["mod", "edit", "-json"]:
    print(json.dumps({"Module": {"Path": "example.com/bursa/ui"}}))
    sys.exit(0)
if os.environ.get('HOLD_BUILD'):
    pathlib.Path(os.environ['READY']).write_text('ready')
    while not pathlib.Path(os.environ['RELEASE']).exists():
        time.sleep(0.05)
pathlib.Path(sys.argv[sys.argv.index('-o') + 1]).write_bytes(b'binary')
""")
            (tools / "go").chmod(0o755)
            config = {
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
                    "reuse": True,
                    "placeholder": "Pre-build placeholder",
                },
            }
            config_path = root / "config.json"
            config_path.write_text(json.dumps(config))
            ready, release = root / "ready", root / "release"
            env = os.environ | {
                "GITHUB_WORKSPACE": str(workspace),
                "PATH": str(tools) + os.pathsep + os.environ["PATH"],
                "READY": str(ready),
                "RELEASE": str(release),
                "PYTHONDONTWRITEBYTECODE": "1",
            }
            env.pop("GITHUB_OUTPUT", None)
            first_env = env | {"HOLD_BUILD": "1"}
            first = subprocess.Popen(
                ["python3", str(SCRIPT), str(config_path), "build/first", "1.2.3", "arm64"],
                env=first_env,
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
            )
            try:
                deadline = time.monotonic() + 15
                while not ready.exists() and first.poll() is None and time.monotonic() < deadline:
                    time.sleep(0.05)
                self.assertTrue(ready.exists(), "first build did not reach the compiler")
                second = subprocess.run(
                    ["python3", str(SCRIPT), str(config_path), "build/second", "1.2.3", "arm64"],
                    env=env,
                    text=True,
                    capture_output=True,
                    timeout=15,
                )
                self.assertNotEqual(second.returncode, 0)
                self.assertIn("another macOS build", second.stderr)
            finally:
                release.touch()
                first_stdout, first_stderr = first.communicate(timeout=15)
            self.assertEqual(first.returncode, 0, first_stdout + first_stderr)
            self.assertEqual((embed / "index.html").read_text(), "original developer content")
            third = subprocess.run(
                ["python3", str(SCRIPT), str(config_path), "build/third", "1.2.3", "arm64"],
                env=env,
                text=True,
                capture_output=True,
                timeout=15,
            )
            self.assertEqual(third.returncode, 0, third.stderr)
            self.assertEqual((embed / "index.html").read_text(), "original developer content")
            ready.unlink()
            release.unlink()
            interrupted = subprocess.Popen(
                ["python3", str(SCRIPT), str(config_path), "build/fourth", "1.2.3", "arm64"],
                env=first_env,
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
            )
            try:
                deadline = time.monotonic() + 15
                while (
                    not ready.exists()
                    and interrupted.poll() is None
                    and time.monotonic() < deadline
                ):
                    time.sleep(0.05)
                self.assertTrue(ready.exists(), "interrupted build did not reach the compiler")
                interrupted.terminate()
                interrupted.communicate(timeout=15)
            finally:
                if interrupted.poll() is None:
                    interrupted.kill()
                    interrupted.communicate(timeout=15)
            self.assertNotEqual(interrupted.returncode, 0)
            self.assertEqual((embed / "index.html").read_text(), "original developer content")
            final = subprocess.run(
                ["python3", str(SCRIPT), str(config_path), "build/fifth", "1.2.3", "arm64"],
                env=env,
                text=True,
                capture_output=True,
                timeout=15,
            )
            self.assertEqual(final.returncode, 0, final.stderr)

    def test_interruption_after_publication_leaves_complete_output(self):
        with tempfile.TemporaryDirectory() as directory:
            workspace = Path(directory).resolve()
            (workspace / "go.mod").write_text("module example.com/adder\n")
            (workspace / "cmd/adder").mkdir(parents=True)
            config = {
                "binaries": [
                    {
                        "name": "adder",
                        "module": ".",
                        "package": "./cmd/adder",
                        "cgo": False,
                        "tags": [],
                    }
                ]
            }
            original_publish = build_module.publish_directory

            def interrupted_publish(source, output):
                original_publish(source, output)
                raise InterruptedError("interrupted after publication")

            def fake_run(args, **kwargs):
                if args[0] == "git":
                    return subprocess.CompletedProcess(args, 0, "abcdef1\n")
                if args[1:4] == ["mod", "edit", "-json"]:
                    return subprocess.CompletedProcess(
                        args, 0, '{"Module":{"Path":"example.com/adder"}}'
                    )
                Path(args[args.index("-o") + 1]).write_bytes(b"complete binary")
                return subprocess.CompletedProcess(args, 0)

            with (
                patch.object(build_module, "publish_directory", side_effect=interrupted_publish),
                patch.object(build_module.subprocess, "run", side_effect=fake_run),
            ):
                with self.assertRaisesRegex(InterruptedError, "interrupted after publication"):
                    build_module.build(config, workspace, "build/output", "1.2.3", "arm64")
            self.assertEqual((workspace / "build/output/adder").read_bytes(), b"complete binary")
            self.assertEqual(list((workspace / "build").glob(".macos-binaries-*")), [])
            with self.assertRaisesRegex(ValueError, "already exists"):
                build_module.build(config, workspace, "build/output", "1.2.3", "arm64")

    def test_module_path_accepts_comments_and_quotes(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "go.mod").write_text('module "example.com/adder" // comment\n')
            self.assertEqual(build_module.module_name(root), "example.com/adder")

    def test_web_symlink_and_placeholder_in_assets_are_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            web = root / "web"
            web.mkdir()
            dist = web / "dist"
            dist.mkdir()
            (dist / "index.html").write_text("real")
            embed = root / "embed"
            embed.mkdir()
            (embed / "index.html").write_text("original")
            config = {
                "directory": "web",
                "dist": "web/dist",
                "embed": "embed",
                "reuse": True,
                "placeholder": "Pre-build placeholder",
            }
            (dist / "app.js").write_text("Pre-build placeholder")
            with self.assertRaisesRegex(ValueError, "placeholder"):
                with build_module.staged_web(root, config):
                    pass
            (dist / "app.js").unlink()
            dist.rename(web / "real")
            dist.symlink_to(web / "real", target_is_directory=True)
            with self.assertRaisesRegex(ValueError, "symlink"):
                with build_module.staged_web(root, config):
                    pass
            self.assertEqual((embed / "index.html").read_text(), "original")

    def test_interruption_immediately_after_backup_restores_embed(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            (root / "web/dist").mkdir(parents=True)
            (root / "web/dist/index.html").write_text("real")
            (root / "embed").mkdir()
            (root / "embed/index.html").write_text("original")
            config = {
                "directory": "web",
                "dist": "web/dist",
                "embed": "embed",
                "reuse": True,
                "placeholder": "placeholder",
            }
            original_rename = Path.rename

            def interrupted(path, target):
                result = original_rename(path, target)
                if path == root / "embed":
                    raise InterruptedError("after backup")
                return result

            with patch.object(Path, "rename", interrupted):
                with self.assertRaises(InterruptedError):
                    with build_module.staged_web(root, config):
                        pass
            self.assertEqual((root / "embed/index.html").read_text(), "original")

    def test_case_colliding_binaries_are_rejected(self):
        def collide(config, workspace):
            config["binaries"][1]["name"] = "ADDER"

        result, _, _, _ = self.run_case("Adder", invalid=collide)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("duplicate binary name", result.stderr)

    def test_invalid_settings_fail_without_running_build(self):
        def escape(config, workspace):
            config["binaries"][0]["package"] = "../../outside"

        def duplicate(config, workspace):
            config["binaries"].append(config["binaries"][0].copy())

        def existing(config, workspace):
            (workspace / "build/shared-macos").mkdir(parents=True)
            (workspace / "build/shared-macos/keep").write_text("keep")

        for invalid in (escape, duplicate, existing):
            with self.subTest(invalid=invalid.__name__):
                result, calls, workspace, _ = self.run_case("Adder", invalid=invalid)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(any(call["tool"] == "go" for call in calls))
                if invalid is existing:
                    self.assertEqual((workspace / "build/shared-macos/keep").read_text(), "keep")


if __name__ == "__main__":
    unittest.main()
