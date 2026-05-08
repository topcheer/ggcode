# @ggcode-cli/ggcode

`@ggcode-cli/ggcode` installs the native `ggcode` binary from GitHub Releases.

During package installation it downloads the matching release archive, verifies it, and places the
real `ggcode` executable in a stable CLI location:

- macOS / Linux: prefers `/usr/local/bin`, falls back to `~/.local/bin`
- Windows: prefers `%USERPROFILE%\\AppData\\Local\\Programs\\ggcode\\bin`, falls back to `%USERPROFILE%\\.local\\bin`

If that directory is not on `PATH`, the installer updates your shell/user PATH and asks you to
reopen the terminal. The npm package keeps a separate `ggcode-bootstrap` helper command for manual
repair, but normal usage should be the real `ggcode` binary.

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

## TLS / Corporate proxy

The installer verifies TLS certificates by default.

If you are behind a corporate proxy with a custom CA certificate that Node.js does not trust, you have two options:

1. **Recommended** — add your CA to Node's trust store:

   ```bash
   NODE_EXTRA_CA_CERTS=/path/to/corporate-ca.pem npm install -g @ggcode-cli/ggcode
   ```

2. **Not recommended** — disable TLS verification entirely:

   ```bash
   GGCODE_INSECURE_TLS=1 npm install -g @ggcode-cli/ggcode
   ```

   This prints a security warning and makes the download vulnerable to man-in-the-middle attacks.

## Native installers

Prefer a native package? Download directly from [GitHub Releases](https://github.com/topcheer/ggcode/releases/latest):

| Platform | Format | Install command |
| --- | --- | --- |
| macOS | `.pkg` | `sudo installer -pkg ./ggcode_*_darwin_universal.pkg -target /` |
| Windows | `.msi` | `msiexec /i .\ggcode_*_windows_x64.msi` |
| Debian / Ubuntu | `.deb` | `sudo dpkg -i ./ggcode_*_linux_*.deb` |
| Fedora / RHEL | `.rpm` | `sudo rpm -i ./ggcode-*-1.*.rpm` |
| Alpine | `.apk` | `sudo apk add --allow-untrusted ./ggcode-*-r1.*.apk` |
| Arch Linux | `.pkg.tar.zst` | `sudo pacman -U ./ggcode-*-1-*.pkg.tar.zst` |

## What is ggcode?

**ggcode** is a terminal-native AI coding agent — not a browser wrapper, not a VS Code extension.
It runs entirely in your terminal with a polished TUI:

- **Multi-provider LLM support** — OpenAI, Anthropic, Google Gemini, GitHub Copilot, DeepSeek, and more
- **Five permission modes** — supervised, plan, auto, bypass, autopilot — you decide how much autonomy the agent gets
- **LSP integration** — go-to-definition, references, rename, diagnostics, code actions via your language server
- **MCP tools** — connect external tool servers (browser, databases, APIs) seamlessly
- **Sub-agents** — spawn parallel workers for research, coding, and testing tasks
- **Harness workflows** — structured engineering pipelines with git worktrees, review, and promotion
- **IM gateway** — connect QQ, Telegram, Discord, Slack, DingTalk, or Feishu for remote coding
- **Bilingual UI** — full English and Chinese support
- **Session persistence** — resume past sessions with `ggcode --resume`
- **File checkpoints** — undo bad edits instantly without git

## Supported platforms

- macOS
- Linux
- Windows

Supported architectures:

- x86_64 / amd64
- arm64

## Project links

- GitHub Releases: https://github.com/topcheer/ggcode/releases
- Repository: https://github.com/topcheer/ggcode
- Issues: https://github.com/topcheer/ggcode/issues
