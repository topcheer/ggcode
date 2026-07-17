# ggcode (npm wrapper) — DEPRECATED

> **This package is deprecated.** The npm wrapper is no longer maintained.
> Please use one of the [recommended installation methods](https://github.com/topcheer/ggcode#installation) instead.

This npm package installs the native `ggcode` binary from GitHub Releases.

## Recommended install methods

### Homebrew (macOS / Linux)

```bash
brew tap topcheer/ggcode
brew trust topcheer/ggcode
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

## Project links

- GitHub Releases: https://github.com/topcheer/ggcode/releases
- Desktop app downloads (macOS DMG, Windows EXE): available on the same releases page
- Discord community: https://discord.gg/F2v4mJmfG
- Repository: https://github.com/topcheer/ggcode
- Issues: https://github.com/topcheer/ggcode/issues
