# Installation

ggcode is available for macOS, Linux, and Windows via multiple methods.

All install scripts default to a **non-privileged user install** (no `sudo` or admin required).

---

## macOS

**Homebrew (recommended):**

```bash
brew install topcheer/tap/ggcode
```

**Install script:**

```bash
curl -fsSL https://ggcode.dev/install.sh | bash
```

---

## Linux

**Install script:**

```bash
curl -fsSL https://ggcode.dev/install.sh | bash
```

**deb / rpm from GitHub Releases:**

Download from [github.com/topcheer/ggcode/releases](https://github.com/topcheer/ggcode/releases) and install with your package manager:

```bash
sudo dpkg -i ggcode_*.deb   # Debian/Ubuntu
sudo rpm -i ggcode-*.rpm    # Fedora/RHEL
```

---

## pip

```bash
pip install ggcode-cli
```

---

## Windows

**winget:**

```powershell
winget install gg.ai.ggcode-cli
```

**PowerShell install script:**

```powershell
irm https://ggcode.dev/install.ps1 | iex
```

**Scoop:**

```powershell
scoop install ggcode
```

---

## npm

```bash
npm install -g @ggcode-cli/ggcode
```

---

## Build from Source

Requires Go 1.26.2 or later.

```bash
CGO_ENABLED=0 go install -tags goolm github.com/topcheer/ggcode/cmd/ggcode@latest
```

The `-tags goolm` build tag enables the bundled pure-Go Olm crypto implementation (no C headers needed). `CGO_ENABLED=0` ensures a static build, matching CI.

---

## Desktop App

Download the latest release for your platform:

| Platform | Format |
|----------|--------|
| macOS    | Universal `.dmg` |
| Windows  | `.msi` |
| Linux    | `.deb` / `.rpm` / `.AppImage` |

Downloads: [github.com/topcheer/ggcode/releases](https://github.com/topcheer/ggcode/releases)

---

## Mobile

| Platform | Distribution |
|----------|-------------|
| iOS      | TestFlight   |
| Android  | Google Play  |

---

## Verify Installation

```bash
ggcode version
```

You should see the version number printed. See [Getting Started](./getting-started.md) to configure your API key.
