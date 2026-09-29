# Snap Store classic confinement request (forum post template)

Post this to https://forum.snapcraft.io/c/requests/ with the subject:
`Snapcrafters feedback request: classic confinement for ggcode`

---

## The `ggcode` snap requires classic confinement

**Name of the snap**: ggcode
**Publisher**: GG AI Studio
**Link to GitHub**: https://github.com/topcheer/ggcode

### Why classic is required

ggcode is a terminal AI coding agent whose core function is executing
arbitrary shell commands, editing files anywhere in the user's project
trees, and spawning toolchains (git, language servers, package managers)
against them. A strict-confinement snap cannot:

- write outside `$HOME` snap dirs (breaks every project outside it),
- spawn the user's own shell/git/toolchain verbatim (breaks PATH parity),
- reuse the user's terminal and tty raw-mode the TUI requires.

These are the same arguments accepted for `code`, `sublime-text`, `git`
and other development-tool snaps. There is no viable strict mode for a
general-purpose coding agent.

### Security notes

- The snap ships the official prebuilt release binary, identical bytes
  to the GitHub release tarball and the deb/rpm/apk artifacts.
- No auto-refresh surprises: releases are tagged and deterministic.

---

Until the store grants classic, `snap install ggcode --classic` from a
local build works for testing: `snapcraft --use-lxd && snap install
ggcode_*.snap --classic --dangerous`.
