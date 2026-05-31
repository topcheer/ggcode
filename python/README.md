# ggcode Python wrapper

`ggcode` on PyPI bootstraps the native `ggcode` terminal agent from GitHub Releases.

When the bootstrap runs, it installs the real binary into a stable CLI location instead of keeping
it in a wrapper-managed cache:

- macOS / Linux: prefers `/usr/local/bin`, falls back to `~/.local/bin`
- Windows: prefers `%USERPROFILE%\\AppData\\Local\\Programs\\ggcode\\bin`, falls back to `%USERPROFILE%\\.local\\bin`

If that directory is not already on `PATH`, the bootstrap updates your PATH configuration and asks
you to reopen the terminal so future `ggcode` launches resolve directly to the native binary.

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

## Other install methods

### Homebrew (macOS / Linux)

```bash
brew tap topcheer/ggcode
brew install ggcode              # CLI
brew install ggcode-desktop      # Desktop (Linux)
brew install --cask ggcode-desktop  # Desktop (macOS DMG)
```

### winget (Windows)

```powershell
winget install --id gg.ai.ggcode-cli        # CLI
winget install --id gg.ai.ggcode-desktop    # Desktop
```

### Native release packages

Download directly from [GitHub Releases](https://github.com/topcheer/ggcode/releases/latest):

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

## Desktop Application

ggcode also ships as a native desktop GUI application with visual chat, IM integration, and tool approval dialogs.

Download from [GitHub Releases](https://github.com/topcheer/ggcode/releases/latest):

| Platform | Asset |
| --- | --- |
| macOS (Universal) | `ggcode-desktop_*_darwin_universal.dmg` |
| Windows | `ggcode-desktop_*_windows_amd64.exe` |

## Project links

- GitHub Releases: https://github.com/topcheer/ggcode/releases
- Desktop app downloads: available on the same releases page
- Discord community: https://discord.gg/F2v4mJmfG
- Repository: https://github.com/topcheer/ggcode
- Issues: https://github.com/topcheer/ggcode/issues
