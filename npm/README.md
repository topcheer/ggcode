# @ggcode-cli/ggcode

`@ggcode-cli/ggcode` is a thin npm wrapper for the `ggcode` terminal agent.

It does not bundle the native binary in the package. Instead, the wrapper downloads the latest
`ggcode` GitHub Release for your platform on install or first run, caches it locally, and then
launches it for you.

## Install

For normal CLI usage, install it globally:

```bash
npm install -g @ggcode-cli/ggcode
```

Then run:

```bash
ggcode
```

If you install it locally, use `npx ggcode` or `./node_modules/.bin/ggcode`.

## What it does

- Detects your platform and architecture
- Downloads the latest matching `ggcode` binary from GitHub Releases
- Verifies the downloaded archive with `checksums.txt`
- Extracts and caches the binary for reuse

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
