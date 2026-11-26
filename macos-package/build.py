#!/usr/bin/env python3
"""Build configured macOS Go executables and restore any staged web assets."""
import argparse
import ctypes
import fcntl
import hashlib
import json
import os
import re
import shutil
import signal
import stat
import subprocess
import sys
import tempfile
from contextlib import contextmanager
from pathlib import Path

from assemble import workspace_path
from render import NAME, VERSION, checked

MODULE = re.compile(r"[A-Za-z0-9][A-Za-z0-9./_~-]*\Z")
TAG = re.compile(r"[A-Za-z0-9_][A-Za-z0-9_]*\Z")
COMMIT = re.compile(r"(?:[0-9a-fA-F]{4,40}|unknown)\Z")


def directory_path(workspace, value):
    if value == ".":
        return workspace
    path = workspace_path(workspace, value, exists=True)
    if not path.is_dir():
        raise ValueError("configured directory does not exist")
    return path


def module_name(directory):
    lines = (directory / "go.mod").read_text(encoding="utf-8").splitlines()
    declarations = [line.split() for line in lines if line.startswith("module ")]
    if len(declarations) != 1 or len(declarations[0]) != 2:
        raise ValueError("go.mod must contain exactly one module declaration")
    return checked(declarations[0][1], MODULE, "Go module path")


@contextmanager
def locked_web(workspace, settings):
    if settings is None:
        yield
        return
    if not isinstance(settings, dict) or not isinstance(settings.get("embed"), str):
        raise ValueError("invalid web embed setting")
    embed = workspace_path(workspace, settings["embed"], exists=False)
    digest = hashlib.sha256(os.fsencode(embed)).hexdigest()
    lock_path = Path("/tmp") / f"macos-web-{os.getuid()}-{digest}.lock"
    descriptor = os.open(lock_path, os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    try:
        info = os.fstat(descriptor)
        if info.st_uid != os.getuid() or not stat.S_ISREG(info.st_mode):
            raise ValueError("invalid web build lock")
        try:
            fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as error:
            raise ValueError("another macOS build is using the web embed directory") from error
        try:
            yield
        finally:
            fcntl.flock(descriptor, fcntl.LOCK_UN)
    finally:
        os.close(descriptor)


@contextmanager
def staged_web(workspace, settings):
    if settings is None:
        yield
        return
    if not isinstance(settings, dict) or set(settings) != {"directory", "dist", "embed", "reuse", "placeholder"}:
        raise ValueError("invalid web asset settings")
    if not isinstance(settings["reuse"], bool) or not isinstance(settings["placeholder"], str) or not settings["placeholder"]:
        raise ValueError("invalid web asset reuse or placeholder setting")
    web = directory_path(workspace, settings["directory"])
    dist = workspace_path(workspace, settings["dist"], exists=False)
    if dist.parent != web:
        raise ValueError("web dist must be a direct child of the web project")
    embed_input = workspace / settings["embed"]
    if embed_input.is_symlink():
        raise ValueError("web embed directory cannot be a symlink")
    embed = directory_path(workspace, settings["embed"])
    if dist == embed or dist in embed.parents or embed in dist.parents:
        raise ValueError("web source and embed directories must be distinct")
    build_copy = None
    if not settings["reuse"]:
        build_copy = tempfile.TemporaryDirectory(prefix="macos-web-build-")
        isolated = Path(build_copy.name) / "web"
        try:
            def ignore_generated(directory, names):
                if Path(directory) == web:
                    return {name for name in names if name in ("node_modules", dist.name)}
                return set()

            shutil.copytree(web, isolated, symlinks=True, ignore=ignore_generated)
            subprocess.run(["npm", "ci"], cwd=isolated, check=True)
            subprocess.run(["npm", "run", "build"], cwd=isolated, check=True)
            dist = isolated / dist.name
        except BaseException:
            build_copy.cleanup()
            raise
    try:
        for root, directories, files in os.walk(dist, followlinks=False):
            if any((Path(root) / name).is_symlink() for name in directories + files):
                raise ValueError("web bundle cannot contain symlinks")
        if not (dist / "index.html").is_file():
            raise ValueError("built web bundle is missing index.html")
        if settings["placeholder"] in (dist / "index.html").read_text(encoding="utf-8"):
            raise ValueError("web bundle contains the placeholder")
    except BaseException:
        if build_copy is not None:
            build_copy.cleanup()
        raise
    backup_dir = Path(tempfile.mkdtemp(prefix=".macos-web-backup-", dir=embed.parent))
    backup = backup_dir / "original"
    try:
        embed.rename(backup)
    except BaseException:
        backup_dir.rmdir()
        if build_copy is not None:
            build_copy.cleanup()
        raise
    try:
        shutil.copytree(dist, embed)
        if not (embed / "index.html").is_file() or settings["placeholder"] in (embed / "index.html").read_text(encoding="utf-8"):
            raise ValueError("embedded web bundle contains the placeholder")
        yield
    finally:
        try:
            if embed.is_symlink():
                embed.unlink()
            elif embed.exists():
                shutil.rmtree(embed)
            backup.rename(embed)
            backup_dir.rmdir()
        except BaseException:
            print(f"web asset restoration failed; original files remain in {backup}", file=sys.stderr)
            raise
        finally:
            if build_copy is not None:
                build_copy.cleanup()


def publish_directory(source, output):
    libc = ctypes.CDLL(None, use_errno=True)
    source_bytes, output_bytes = os.fsencode(source), os.fsencode(output)
    if sys.platform == "darwin":
        operation = libc.renamex_np
        operation.argtypes = (ctypes.c_char_p, ctypes.c_char_p, ctypes.c_uint)
        result = operation(source_bytes, output_bytes, 4)  # RENAME_EXCL
    elif sys.platform.startswith("linux"):
        operation = libc.renameat2
        operation.argtypes = (ctypes.c_int, ctypes.c_char_p, ctypes.c_int, ctypes.c_char_p, ctypes.c_uint)
        result = operation(-100, source_bytes, -100, output_bytes, 1)  # AT_FDCWD, RENAME_NOREPLACE
    else:
        raise ValueError("atomic binary publication is unsupported on this platform")
    if result != 0:
        error = ctypes.get_errno()
        raise OSError(error, os.strerror(error), str(output))


def build(config, workspace, output_path, version, architecture):
    if not isinstance(config, dict) or set(config) - {"binaries", "web", "version_prefix"} or "binaries" not in config:
        raise ValueError("build settings require binaries and optional web assets")
    if not version:
        try:
            described = subprocess.run(["git", "describe", "--tags", "--always", "--dirty"], cwd=workspace, check=False, text=True, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
            version = described.stdout.strip() if described.returncode == 0 else ""
        except FileNotFoundError:
            version = ""
        if not version:
            version = "0.0.0"
    version_prefix = config.get("version_prefix", "strip")
    if version_prefix not in ("strip", "preserve"):
        raise ValueError("version_prefix must be strip or preserve")
    if version_prefix == "strip":
        version = version.removeprefix("v")
    checked(version, VERSION, "version")
    if architecture not in ("arm64", "amd64"):
        raise ValueError("unsupported architecture")
    binaries = config["binaries"]
    if not isinstance(binaries, list) or not binaries:
        raise ValueError("binaries must be a nonempty list")
    specs = []
    names = set()
    for item in binaries:
        if not isinstance(item, dict) or set(item) != {"name", "module", "package", "cgo", "tags"}:
            raise ValueError("invalid binary build settings")
        name = checked(item["name"], NAME, "binary name")
        if name in names:
            raise ValueError("duplicate binary name")
        names.add(name)
        module_dir = directory_path(workspace, item["module"])
        package = item["package"]
        if not isinstance(package, str) or not package.startswith("./") or ".." in Path(package).parts:
            raise ValueError("package must be a module-relative path")
        package_dir = (module_dir / package).resolve(strict=True)
        if not package_dir.is_relative_to(module_dir) or not package_dir.is_dir():
            raise ValueError("package must be a directory within its module")
        if not isinstance(item["cgo"], bool) or not isinstance(item["tags"], list):
            raise ValueError("cgo must be boolean and tags must be a list")
        tags = [checked(tag, TAG, "Go build tag") for tag in item["tags"]]
        specs.append((name, module_dir, package, item["cgo"], tags, module_name(module_dir)))
    original_output = workspace / output_path
    if original_output.exists() or original_output.is_symlink():
        raise ValueError("binary output directory already exists")
    output = workspace_path(workspace, output_path, exists=False)
    if output.exists() or output.is_symlink():
        raise ValueError("binary output directory already exists")
    web_settings = config.get("web")
    if isinstance(web_settings, dict) and isinstance(web_settings.get("embed"), str):
        embed = workspace_path(workspace, web_settings["embed"], exists=True)
        if output == embed or embed in output.parents:
            raise ValueError("binary output cannot be inside the web embed directory")
    try:
        revision = subprocess.run(["git", "rev-parse", "--short", "HEAD"], cwd=workspace, check=False, text=True, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
        commit = revision.stdout.strip() if revision.returncode == 0 else ""
    except FileNotFoundError:
        commit = ""
    checked(commit or "unknown", COMMIT, "commit hash")
    commit = commit or "unknown"
    with tempfile.TemporaryDirectory(prefix="macos-binaries-") as temporary:
        staged = Path(temporary)
        with locked_web(workspace, config.get("web")), staged_web(workspace, config.get("web")):
            for name, module_dir, package, cgo, tags, module in specs:
                ldflags = f"-s -w -X {module}/internal/version.Version={version} -X {module}/internal/version.CommitHash={commit}"
                command = ["go", "build", "-ldflags", ldflags]
                if tags:
                    command.extend(["-tags", ",".join(tags)])
                command.extend(["-o", str(staged / name), package])
                env = os.environ | {"CGO_ENABLED": "1" if cgo else "0", "GOOS": "darwin", "GOARCH": architecture}
                subprocess.run(command, cwd=module_dir, env=env, check=True)
        output.parent.mkdir(parents=True, exist_ok=True)
        published_dir = Path(tempfile.mkdtemp(prefix=".macos-binaries-", dir=output.parent))
        try:
            for name in names:
                shutil.copy2(staged / name, published_dir / name)
            publish_directory(published_dir, output)
        except BaseException:
            if published_dir.exists():
                shutil.rmtree(published_dir)
            raise
    return output, version


def main():
    def interrupted(signum, frame):
        raise InterruptedError("macOS build interrupted")

    signal.signal(signal.SIGTERM, interrupted)
    parser = argparse.ArgumentParser()
    parser.add_argument("config", type=Path)
    parser.add_argument("output")
    parser.add_argument("version")
    parser.add_argument("architecture")
    args = parser.parse_args()
    try:
        workspace = Path(os.environ["GITHUB_WORKSPACE"]).resolve(strict=True)
        with args.config.open(encoding="utf-8") as source:
            config = json.load(source)
        output, version = build(config, workspace, args.output, args.version, args.architecture)
        output_file = os.environ.get("GITHUB_OUTPUT")
        if output_file:
            with open(output_file, "a", encoding="utf-8") as target:
                target.write(f"directory={output.relative_to(workspace)}\n")
                target.write(f"version={version}\n")
        print(output)
    except (KeyError, ValueError, OSError, subprocess.CalledProcessError) as error:
        parser.exit(1, f"macOS binary build: {error}\n")


if __name__ == "__main__":
    main()
