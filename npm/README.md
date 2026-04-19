# @ggcode-cli/ggcode

`@ggcode-cli/ggcode` installs the native `ggcode` binary from GitHub Releases.

During package installation it downloads the matching release archive, verifies it, and places the
real `ggcode` executable in a stable CLI location:

- macOS / Linux: prefers `/usr/local/bin`, falls back to `~/.local/bin`
- Windows: prefers `%USERPROFILE%\\AppData\\Local\\Programs\\ggcode\\bin`, falls back to `%USERPROFILE%\\.local\\bin`

If that directory is not on `PATH`, the installer updates your shell/user PATH and asks you to
reopen the terminal. The npm package keeps a separate `ggcode-bootstrap` helper command for manual
repair, but normal usage should be the real `ggcode` binary.

This package is just the release-backed installer layer. Product usage, harness workflows, and TUI
behavior are documented in the main repository README.

## Install

For normal CLI usage, install it globally:

```bash
npm install -g @ggcode-cli/ggcode
```

Then run:

```bash
ggcode
```

If the native install needs to be retried manually, run:

```bash
ggcode-bootstrap
```

## What it does

- Detects your platform and architecture
- Downloads the latest matching `ggcode` binary from GitHub Releases
- Verifies the downloaded archive with `checksums.txt`
- Installs the real binary into a stable PATH location
- Updates `PATH` when needed so future `ggcode` launches bypass the wrapper

## Pin a specific ggcode release

By default, the wrapper always resolves the latest `ggcode` release.

If you need to pin a specific release, set `GGCODE_INSTALL_VERSION`:

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
