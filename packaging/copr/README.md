# COPR Publishing (manual steps)

COPR builds from this spec; publishing needs a Fedora account + API key
(none of which live in the repo).

## One-time setup

1. Create a Fedora account: https://accounts.fedoraproject.org
2. Install copr-cli: `dnf install copr-cli`
3. Authenticate: `copr-cli login` (opens browser, writes `~/.config/copr`)

## Create the project

```sh
copr-cli create topcheer/ggcode --chroot fedora-40-x86_64 \
    --chroot fedora-40-aarch64 --chroot epel-9-x86_64 \
    --description "ggcode - AI coding agent for the terminal"
```

## Build a release

1. Refresh `Version`/`Release` and the Source0/Source1 sha256 comment lines in
   `ggcode.spec` (values from the release checksums.txt).
2. Build:

```sh
copr-cli build topcheer/ggcode ggcode.spec
```

Note: `%files %license LICENSE` resolves from the extracted tarball during
install; if rpmlint complains about the missing license file in the binary
package, add `Source2: %{github}/raw/v%{version}/LICENSE` and install it under
`%{_licensedir}`.

## Install (user side)

```sh
dnf copr enable topcheer/ggcode
dnf install ggcode
```
