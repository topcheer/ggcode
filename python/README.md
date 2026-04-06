# ggcode Python wrapper

`ggcode` on PyPI is a thin Python wrapper for the `ggcode` terminal agent.

It does not ship the platform binary inside the wheel. Instead, the wrapper downloads the latest
matching `ggcode` GitHub Release on first run, caches it locally, and then launches it.

## Install

```bash
pip install ggcode
```

Then run:

```bash
ggcode
```

## What it does

- Detects your operating system and CPU architecture
- Downloads the latest matching `ggcode` archive from GitHub Releases
- Verifies the archive against `checksums.txt`
- Extracts and caches the binary for future runs

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
