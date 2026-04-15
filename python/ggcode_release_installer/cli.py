from __future__ import annotations

import errno
import hashlib
import json
import os
import platform
import re
import shutil
import stat
import subprocess
import sys
import ssl
import tarfile
import tempfile
import urllib.parse
import urllib.request
import zipfile
from dataclasses import dataclass
from pathlib import Path

OWNER = "topcheer"
REPO = "ggcode"
MARKER_START = "# >>> ggcode PATH >>>"
MARKER_END = "# <<< ggcode PATH <<<"
METADATA = ".ggcode-wrapper.json"


@dataclass
class InstallResult:
    binary_path: Path
    install_dir: Path
    version: str
    path_updated: bool
    installed_now: bool
    needs_restart: bool
    used_fallback: bool


def normalize_version() -> str:
    selected = os.environ.get("GGCODE_INSTALL_VERSION", "").strip()
    if not selected or selected == "latest":
        return "latest"
    if selected.startswith("v"):
        return selected
    return f"v{selected}"


def resolve_target() -> tuple[str, str]:
    system = sys.platform
    if system.startswith("linux"):
        goos = "linux"
    elif system == "darwin":
        goos = "darwin"
    elif system in ("win32", "cygwin"):
        goos = "windows"
    else:
        raise RuntimeError(f"Unsupported platform: {system}")

    machine = platform.machine().lower() or os.environ.get("PROCESSOR_ARCHITECTURE", "").lower()
    if machine in ("x86_64", "amd64"):
        arch = "x86_64"
    elif machine in ("arm64", "aarch64"):
        arch = "arm64"
    else:
        raise RuntimeError(f"Unsupported architecture: {machine or 'unknown'}")
    return goos, arch


def release_base(version: str) -> str:
    if version == "latest":
        return f"https://github.com/{OWNER}/{REPO}/releases/latest/download"
    return f"https://github.com/{OWNER}/{REPO}/releases/download/{version}"


def preferred_install_dirs() -> list[Path]:
    home = Path.home()
    if os.name == "nt":
        return [
            home / "AppData" / "Local" / "Programs" / "ggcode" / "bin",
            home / ".local" / "bin",
        ]
    return [Path("/usr/local/bin"), home / ".local" / "bin"]


def metadata_path(directory: Path) -> Path:
    return directory / METADATA


def _build_ssl_context() -> ssl.SSLContext:
    ctx = ssl.create_default_context()
    ctx.check_hostname = False
    ctx.verify_mode = ssl.CERT_NONE
    return ctx


def _urlopen(url: str) -> object:
    ctx = _build_ssl_context()
    return urllib.request.urlopen(url, context=ctx)


def download(url: str) -> bytes:
    with _urlopen(url) as response:
        return response.read()


def resolve_release_version(version: str) -> str:
    if version != "latest":
        return version

    with _urlopen(f"https://github.com/{OWNER}/{REPO}/releases/latest") as response:
        final_url = response.geturl()

    parsed = urllib.parse.urlparse(final_url)
    parts = [part for part in parsed.path.split("/") if part]
    if len(parts) < 4 or parts[-2] != "tag":
        raise RuntimeError(f"Could not resolve latest ggcode release from {final_url}")
    return urllib.parse.unquote(parts[-1])


def parse_checksums(body: str) -> dict[str, str]:
    checksums: dict[str, str] = {}
    for raw_line in body.splitlines():
        parts = raw_line.strip().split()
        if len(parts) >= 2:
            checksums[parts[-1]] = parts[0]
    return checksums


def read_metadata(directory: Path) -> dict[str, str] | None:
    try:
        return json.loads(metadata_path(directory).read_text(encoding="utf-8"))
    except (FileNotFoundError, json.JSONDecodeError):
        return None


def write_metadata(directory: Path, version: str) -> None:
    metadata_path(directory).write_text(json.dumps({"version": version}, indent=2) + "\n", encoding="utf-8")


def existing_install(requested_version: str, binary_name: str) -> InstallResult | None:
    for directory in preferred_install_dirs():
        binary_path = directory / binary_name
        if not binary_path.exists():
            continue
        metadata = read_metadata(directory)
        if metadata is None:
            # No .ggcode-wrapper.json means this binary was not installed by the wrapper
            # (e.g. a Python pip entry point script with the same name). Skip it.
            continue
        if requested_version != "latest" and metadata.get("version") != requested_version:
            continue
        path_updated = ensure_installed_path(directory)
        return InstallResult(
            binary_path=binary_path,
            install_dir=directory,
            version=metadata.get("version", "unknown"),
            path_updated=path_updated,
            installed_now=False,
            needs_restart=path_updated,
            used_fallback=directory != preferred_install_dirs()[0],
        )
    return None


def ensure_installed() -> InstallResult:
    requested_version = normalize_version()
    goos, arch = resolve_target()
    archive_ext = ".zip" if goos == "windows" else ".tar.gz"
    archive_name = f"ggcode_{goos}_{arch}{archive_ext}"
    binary_name = "ggcode.exe" if goos == "windows" else "ggcode"

    existing = existing_install(requested_version, binary_name)
    if existing is not None:
        return existing

    version = resolve_release_version(requested_version)
    base = release_base(version)
    archive = download(f"{base}/{archive_name}")
    checksums = parse_checksums(download(f"{base}/checksums.txt").decode("utf-8"))
    expected = checksums.get(archive_name)
    if not expected:
        raise RuntimeError(f"Checksum for {archive_name} not found")
    actual = hashlib.sha256(archive).hexdigest()
    if actual.lower() != expected.lower():
        raise RuntimeError(f"Checksum mismatch for {archive_name}")

    with tempfile.TemporaryDirectory(prefix="ggcode-py-") as temp_dir:
        temp_root = Path(temp_dir)
        archive_path = temp_root / archive_name
        archive_path.write_bytes(archive)
        extract_dir = temp_root / "extract"
        extract_dir.mkdir(parents=True, exist_ok=True)

        if archive_ext == ".zip":
            with zipfile.ZipFile(archive_path) as zf:
                zf.extractall(extract_dir)
        else:
            with tarfile.open(archive_path, "r:gz") as tf:
                tf.extractall(extract_dir)

        extracted = next((p for p in extract_dir.rglob(binary_name) if p.is_file()), None)
        if extracted is None:
            raise RuntimeError(f"Could not find {binary_name} in {archive_name}")

        last_error: Exception | None = None
        preferred = preferred_install_dirs()
        for index, install_dir in enumerate(preferred):
            try:
                binary_path = install_binary(extracted, install_dir / binary_name, version)
                path_updated = ensure_installed_path(install_dir)
                return InstallResult(
                    binary_path=binary_path,
                    install_dir=install_dir,
                    version=version,
                    path_updated=path_updated,
                    installed_now=True,
                    needs_restart=path_updated,
                    used_fallback=index > 0,
                )
            except OSError as exc:
                last_error = exc
                if not is_permission_error(exc) or index == len(preferred) - 1:
                    break

        if last_error is not None:
            raise RuntimeError(f"Failed to install ggcode: {last_error}") from last_error

    raise RuntimeError("Failed to install ggcode")


def install_binary(source: Path, destination: Path, version: str) -> Path:
    destination.parent.mkdir(parents=True, exist_ok=True)
    temp_path = destination.with_name(f"{destination.name}.tmp-{os.getpid()}")
    shutil.copy2(source, temp_path)
    if os.name != "nt":
        current_mode = temp_path.stat().st_mode
        temp_path.chmod(current_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
    os.replace(temp_path, destination)
    write_metadata(destination.parent, version)
    return destination


def is_permission_error(exc: OSError) -> bool:
    return exc.errno in {errno.EACCES, errno.EPERM}


def ensure_installed_path(directory: Path) -> bool:
    entries = path_entries(os.environ.get("PATH", ""))
    if any(same_path(entry, directory) for entry in entries):
        return False
    if os.name == "nt":
        changed = ensure_windows_user_path(directory)
    else:
        changed = ensure_unix_path(directory)
    os.environ["PATH"] = os.pathsep.join([str(directory), *entries])
    return changed


def path_entries(value: str) -> list[str]:
    return [entry.strip() for entry in value.split(os.pathsep) if entry.strip()]


def same_path(left: str | Path, right: str | Path) -> bool:
    left_path = os.path.abspath(os.fspath(left))
    right_path = os.path.abspath(os.fspath(right))
    if os.name == "nt":
        left_path = left_path.rstrip("\\/").lower()
        right_path = right_path.rstrip("\\/").lower()
    return left_path == right_path


def ensure_windows_user_path(directory: Path) -> bool:
    escaped_dir = str(directory).replace("'", "''")
    script = (
        f"$dir = '{escaped_dir}'; "
        "$current = [Environment]::GetEnvironmentVariable('Path', 'User'); "
        "$parts = @(); "
        "if ($current) { $parts = $current -split ';' | Where-Object { $_ -and $_.Trim() -ne '' } }; "
        "$exists = $parts | Where-Object { $_.TrimEnd('\\\\') -ieq $dir.TrimEnd('\\\\') }; "
        "if (-not $exists) { "
        "  $new = @($dir) + $parts; "
        "  [Environment]::SetEnvironmentVariable('Path', ($new -join ';'), 'User'); "
        "  Write-Output 'updated' "
        "} else { "
        "  Write-Output 'unchanged' "
        "}"
    )
    result = subprocess.run(
        ["powershell", "-NoProfile", "-Command", script],
        check=True,
        text=True,
        capture_output=True,
    )
    return result.stdout.strip() == "updated"


def ensure_unix_path(directory: Path) -> bool:
    changed = False
    for target in profile_targets():
        before = target.read_text(encoding="utf-8") if target.exists() else ""
        after = upsert_path_block(before, directory)
        if after != before:
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text(after, encoding="utf-8")
            changed = True
    return changed


def profile_targets() -> list[Path]:
    home = Path.home()
    shell = Path(os.environ.get("SHELL", "")).name
    preferred: list[str] = []
    if shell == "zsh":
        preferred.extend([".zshrc", ".zprofile"])
    elif shell == "bash":
        preferred.extend([".bashrc", ".bash_profile"])
    preferred.append(".profile")

    existing = [".zshrc", ".zprofile", ".bashrc", ".bash_profile", ".profile"]
    targets: list[Path] = []
    for name in preferred + [name for name in existing if (home / name).exists()]:
        path = home / name
        if path not in targets:
            targets.append(path)
    return targets


def upsert_path_block(content: str, directory: Path) -> str:
    block = f'{MARKER_START}\nexport PATH="{directory}:$PATH"\n{MARKER_END}\n'
    pattern = re.compile(re.escape(MARKER_START) + r"[\s\S]*?" + re.escape(MARKER_END) + r"\n?", re.MULTILINE)
    if pattern.search(content):
        return pattern.sub(block, content)
    suffix = "\n" if content and not content.endswith("\n") else ""
    return f"{content}{suffix}{block}"


def print_install_message(result: InstallResult) -> None:
    action = "installed" if result.installed_now else "already available"
    print(f"ggcode {result.version} {action} at {result.binary_path}", file=sys.stderr)
    if result.needs_restart:
        print("Reopen your terminal, then run `ggcode` directly.", file=sys.stderr)
    print("If you ever need to rerun the bootstrap flow, use `ggcode-bootstrap`.", file=sys.stderr)


def main() -> int:
    result = ensure_installed()
    if result.installed_now or result.needs_restart:
        print_install_message(result)
    env = dict(os.environ)
    env["GGCODE_WRAPPER_KIND"] = "python"
    completed = subprocess.run([str(result.binary_path), *sys.argv[1:]], env=env)
    return completed.returncode


def bootstrap_main() -> int:
    return main()


if __name__ == "__main__":
    raise SystemExit(main())
