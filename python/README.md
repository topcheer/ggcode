# ggcode Python wrapper

`ggcode` on PyPI bootstraps the native `ggcode` terminal agent from GitHub Releases.

When the bootstrap runs, it installs the real binary into a stable CLI location instead of keeping
it in a wrapper-managed cache:

- macOS / Linux: prefers `/usr/local/bin`, falls back to `~/.local/bin`
- Windows: prefers `%USERPROFILE%\\AppData\\Local\\Programs\\ggcode\\bin`, falls back to `%USERPROFILE%\\.local\\bin`

If that directory is not already on `PATH`, the bootstrap updates your PATH configuration and asks
you to reopen the terminal so future `ggcode` launches resolve directly to the native binary.

This package is only the release-backed installer wrapper. For product features, harness workflow
details, and the main CLI experience, use the repository README.

## Install

```bash
pip install ggcode
```

Then run:

```bash
ggcode
```

If you ever need to rerun the bootstrap flow explicitly, you can also use:

```bash
ggcode-bootstrap
```

## What it does

- Detects your operating system and CPU architecture
- Downloads the latest matching `ggcode` archive from GitHub Releases
- Verifies the archive against `checksums.txt`
- Installs the real binary into a stable PATH location
- Updates PATH so future `ggcode` launches bypass the Python wrapper

## Pin a specific ggcode release

By default, the wrapper always resolves the latest `ggcode` release.

To force a specific release, set `GGCODE_INSTALL_VERSION`:

```bash
GGCODE_INSTALL_VERSION=vX.Y.Z ggcode
```

or:

```bash
GGCODE_INSTALL_VERSION=X.Y.Z ggcode
```

## Supported platforms

- macOS
- Linux
- Windows

Supported architectures:

- x86_64 / amd64
- arm64

## Project links

- Repository: https://github.com/topcheer/ggcode
- Issues: https://github.com/topcheer/ggcode/issues
