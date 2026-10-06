# Linux install guide — package channels

ggcode ships prebuilt binaries on every GitHub release. Pick the channel
that fits your distribution:

## Direct download (all distros)

```sh
curl -fL https://github.com/topcheer/ggcode/releases/latest/download/ggcode_linux_x86_64.tar.gz | tar -xz
sudo install ggcode /usr/local/bin/
```

(arm64 machines: use `ggcode_linux_arm64.tar.gz`.)

## deb / rpm / apk / archlinux (goreleaser artifacts)

Every release carries native packages — download from
[releases](https://github.com/topcheer/ggcode/releases) and install with
`dpkg -i`, `rpm -i`, `apk add`, or `pacman -U` respectively.

## Arch Linux (AUR) — `ggcode-bin`

```sh
yay -S ggcode-bin   # or: paru -S ggcode-bin
```

Packaging source: [`packaging/aur/`](../../packaging/aur/). Maintainer
update steps: run `packaging/aur/update.sh <version>`, regenerate
`.SRCINFO`, commit.

## Fedora / RHEL (COPR)

```sh
dnf copr enable topcheer/ggcode
dnf install ggcode
```

Packaging source: [`packaging/copr/`](../../packaging/copr/). Publisher
manual steps (copr-cli create/build) are documented in its README.

## Snap

```sh
snap install ggcode --classic
```

Classic confinement is required (arbitrary shell/file access is the
product); the store request template is at
[`packaging/snap/CLASSIC_REQUEST.md`](../../packaging/snap/CLASSIC_REQUEST.md).

## Nix / NixOS

Skeleton derivation: [`packaging/nix/package.nix`](../../packaging/nix/package.nix).
`vendorHash` must be computed per release — see the file header for the
exact `go mod vendor` + `nix hash` steps. Until an official nixpkgs
PR lands, consume it via your flake inputs/overlays.

## Debian/Ubuntu PPA (planned)

PPA is deliberately deferred; when it lands the install will use the
modern signed-by keyring form:

```sh
# preview of the future shape (not live yet)
curl -fsSL https://ppa.ggcode.dev/gpg | sudo gpg --dearmor -o /usr/share/keyrings/ggcode.gpg
echo "deb [signed-by=/usr/share/keyrings/ggcode.gpg] https://ppa.ggcode.dev/stable $(lsb_release -cs) main" | sudo tee /etc/apt/sources.list.d/ggcode.list
sudo apt update && sudo apt install ggcode
```

## Publisher manual checklist (per release)

1. AUR: `packaging/aur/update.sh <ver>` → regenerate `.SRCINFO` → push to AUR.
2. COPR: bump `Version` + sha256 comments in `packaging/copr/ggcode.spec` → `copr-cli build`.
3. Snap: bump `version:` in `packaging/snap/snapcraft.yaml` → `snapcraft && snapcraft upload`.
4. Nix: bump `version` + recompute `vendorHash` (steps in `package.nix` header).
5. PPA: pending infra; skip until stood up.
