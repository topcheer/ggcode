from __future__ import annotations

import hashlib
import os
import platform
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile
import urllib.parse
import urllib.request
import zipfile
from pathlib import Path

OWNER = "topcheer"
REPO = "ggcode"


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


def cache_root() -> Path:
    if os.name == "nt":
        base = Path(os.environ.get("LOCALAPPDATA", tempfile.gettempdir()))
        return base / "ggcode" / "python"
    return Path.home() / ".cache" / "ggcode" / "python"


def download(url: str) -> bytes:
    with urllib.request.urlopen(url) as response:
        return response.read()


def resolve_release_version(version: str) -> str:
    if version != "latest":
        return version

    with urllib.request.urlopen(f"https://github.com/{OWNER}/{REPO}/releases/latest") as response:
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


def ensure_installed() -> Path:
    version = resolve_release_version(normalize_version())
    goos, arch = resolve_target()
    archive_ext = ".zip" if goos == "windows" else ".tar.gz"
    archive_name = f"ggcode_{goos}_{arch}{archive_ext}"
    binary_name = "ggcode.exe" if goos == "windows" else "ggcode"
    install_dir = cache_root() / version / f"{goos}-{arch}"
    binary_path = install_dir / binary_name
    if binary_path.exists():
        return binary_path

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

        install_dir.mkdir(parents=True, exist_ok=True)
        shutil.copy2(extracted, binary_path)
        if os.name != "nt":
            current_mode = binary_path.stat().st_mode
            binary_path.chmod(current_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)

    return binary_path


def main() -> int:
    binary = ensure_installed()
    result = subprocess.run([str(binary), *sys.argv[1:]])
    return result.returncode


if __name__ == "__main__":
    raise SystemExit(main())
