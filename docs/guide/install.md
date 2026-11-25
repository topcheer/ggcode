# Installation

ggcode is available for macOS, Linux, and Windows via multiple methods.

All install scripts default to a **non-privileged user install** (no `sudo` or admin required).

---

## Shell installer (recommended)

**macOS / Linux** — installs to `~/.local/bin`:

```bash
curl -fsSL https://ggcode.dev/install.sh | bash
```

**Windows** — PowerShell 5.1+ (installs to `%LOCALAPPDATA%\ggcode\bin` and adds it to the user `PATH`):

```powershell
irm https://ggcode.dev/install.ps1 | iex
```

Both installers detect your OS and architecture (`x86_64` / `arm64`), download the matching
archive from the latest release (resolved through `https://download.ggcode.dev/latest/manifest.json` -
a Cloudflare Worker mirror that streams and edge-caches the GitHub release assets - falling back to GitHub), **verify its SHA-256 against the release's `checksums.txt`**, install the
binary and print the installed version. They never use `sudo` or admin rights.

Useful options:

| | macOS / Linux | Windows (save the script to a file) |
|---|---|---|
| Show resolved URLs only | `bash install.sh --dry-run` | `.\install.ps1 -DryRun` |
| Custom install directory | `GGCODE_INSTALL_DIR=~/.bin bash install.sh` | `.\install.ps1 -InstallDir C:\Tools` |
| Pin a release | `GGCODE_VERSION=v1.3.292 bash install.sh` | `.\install.ps1 -Version v1.3.292` |

Verify:

```bash
ggcode --version
```

---

## macOS

### Homebrew

```bash
brew install topcheer/homebrew-ggcode/ggcode
```

### Direct Download

Download the latest `.tar.gz` from [GitHub Releases](https://github.com/topcheer/ggcode/releases) — or use the shell installer above, which does this for you with checksum verification:

```bash
# Apple Silicon (arm64)
curl -fL https://github.com/topcheer/ggcode/releases/latest/download/ggcode_darwin_arm64.tar.gz | tar -xz
# Intel (x86_64)
curl -fL https://github.com/topcheer/ggcode/releases/latest/download/ggcode_darwin_x86_64.tar.gz | tar -xz

mkdir -p ~/.local/bin && mv ggcode ~/.local/bin/ && ggcode --version
```

---

## Linux

### Shell installer

```bash
curl -fsSL https://ggcode.dev/install.sh | bash
```

### Direct Download

```bash
# x86_64
curl -fL https://github.com/topcheer/ggcode/releases/latest/download/ggcode_linux_x86_64.tar.gz | tar -xz
# arm64
curl -fL https://github.com/topcheer/ggcode/releases/latest/download/ggcode_linux_arm64.tar.gz | tar -xz

mkdir -p ~/.local/bin && mv ggcode ~/.local/bin/ && ggcode --version
```

### Package Managers

Packages are published for each release (see the [Linux install guide](./install-linux.md)):

| Format | Example asset |
|--------|-------------|
| Debian/Ubuntu (.deb) | `ggcode_<version>_amd64.deb`, `ggcode_<version>_arm64.deb` |
| Fedora/RHEL (.rpm) | `ggcode-<version>-1.x86_64.rpm`, `ggcode-<version>-1.aarch64.rpm` |
| Alpine (.apk) | `ggcode_<version>_x86_64.apk`, `ggcode_<version>_aarch64.apk` |
| Arch Linux | `ggcode-<version>-1-x86_64.pkg.tar.zst` |

Also available: AUR (`ggcode-bin`), COPR (`topcheer/ggcode`) and Snap — see [install-linux.md](./install-linux.md).

---

## Windows

### PowerShell installer

```powershell
irm https://ggcode.dev/install.ps1 | iex
```

### Direct Download

Download the `.zip` from [GitHub Releases](https://github.com/topcheer/ggcode/releases):

1. Download `ggcode_windows_x86_64.zip` (or `ggcode_windows_arm64.zip`)
2. Extract `ggcode.exe` to a directory in your `PATH`

> `winget install topcheer.ggcode` is not available yet: the CLI has not been published to winget. Use the PowerShell installer.

---

## Build From Source

### Prerequisites

- **Go 1.26.2+** (check your Go version: `go version`)
- **libolm** (for the mautrix crypto dependency)

macOS:
```bash
brew install libolm
```

Linux:
```bash
# Debian/Ubuntu
sudo apt install libolm-dev

# Fedora
sudo dnf install libolm-devel
```

### Build

```bash
git clone https://github.com/topcheer/ggcode.git
cd ggcode
make install   # installs to $GOPATH/bin with -tags goolm
```

> **Note**: All Go build/test commands require the `-tags goolm` build tag. The Makefile handles this automatically via `TAGS := goolm`. If running `go` commands directly, always add `-tags goolm`.

### Verify

```bash
ggcode version
```

You should see the version number printed. See [Getting Started](./getting-started.md) to configure your API key.
