# Installation

ggcode is available for macOS, Linux, and Windows via multiple methods.

All install scripts default to a **non-privileged user install** (no `sudo` or admin required).

---

## macOS

### Homebrew

```bash
brew install topcheer/tap/ggcode
```

### Direct Download

Download the latest `.tar.gz` from [GitHub Releases](https://github.com/topcheer/ggcode/releases):

```bash
# Apple Silicon (arm64)
curl -L https://github.com/topcheer/ggcode/releases/latest/download/ggcode_Darwin_arm64.tar.gz | tar xz
sudo mv ggcode /usr/local/bin/

# Intel (amd64)
curl -L https://github.com/topcheer/ggcode/releases/latest/download/ggcode_Darwin_amd64.tar.gz | tar xz
sudo mv ggcode /usr/local/bin/
```

---

## Linux

### Direct Download

```bash
# amd64
curl -L https://github.com/topcheer/ggcode/releases/latest/download/ggcode_Linux_x86_64.tar.gz | tar xz
sudo mv ggcode /usr/local/bin/

# arm64
curl -L https://github.com/topcheer/ggcode/releases/latest/download/ggcode_Linux_arm64.tar.gz | tar xz
sudo mv ggcode /usr/local/bin/
```

### Package Managers

Packages are published for each release:

| Format | Package Name |
|--------|-------------|
| Debian/Ubuntu (.deb) | `ggcode_<version>_linux_amd64.deb` |
| Fedora/RHEL (.rpm) | `ggcode_<version>_linux_amd64.rpm` |
| Alpine (.apk) | `ggcode_<version>_linux_amd64.apk` |
| Arch Linux | `ggcode_<version>_linux_amd64.pkg.tar.zst` |

---

## Windows

### Winget

```powershell
winget install topcheer.ggcode
```

### Direct Download

Download the `.zip` from [GitHub Releases](https://github.com/topcheer/ggcode/releases):

1. Download `ggcode_Windows_x86_64.zip`
2. Extract `ggcode.exe` to a directory in your `PATH`

---

## Package Managers

### npm

```bash
npm install -g @ggcode-cli/ggcode
```

### PyPI

```bash
pip install ggcode
```

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
