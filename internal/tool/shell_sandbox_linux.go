//go:build linux

package tool

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/topcheer/ggcode/internal/debug"
	"golang.org/x/sys/unix"
)

func init() {
	// Landlock surfaces filesystem denials as EACCES ("Permission denied"),
	// which is far too generic to match globally - but when the command was
	// wrapped in the OS sandbox, matching it is correct and lets the agent
	// adapt instead of retrying blindly.
	sandboxDeniedExtraMarkers = append(sandboxDeniedExtraMarkers, "permission denied")

	SandboxLaunchEntry = sandboxLauncherEntry
}

// sandboxWrap rewrites the command (cmd = <shell> -c <command>) so it launches
// ggcode itself as a launcher: [ggcode] __ggcode_sandbox_launch <payload>
// <shell> <args...>. The launcher (see sandboxLauncherEntry, routed from
// cmd/ggcode main) applies Landlock + seccomp to itself and execve(2)s the
// shell, so the untrusted process starts already restricted.
//
// Linux has no sandbox-exec equivalent; Landlock and seccomp must be applied
// by the sandboxed process itself (they thread-restrict, they cannot restrict
// a separate wrapper binary), hence the self re-exec pattern.
func sandboxWrap(cmd *exec.Cmd, workspace string, p *SandboxPolicy) (bool, error) {
	self, err := os.Executable()
	if err != nil {
		return false, fmt.Errorf("%w: cannot resolve ggcode binary for sandbox launcher: %v", errSandboxUnavailable, err)
	}
	payload, err := encodeSandboxLaunchPayload(sandboxLaunchPayload{
		WritePaths:   sandboxWriteAllowPaths(workspace, p),
		AllowNetwork: p.AllowNetwork,
	})
	if err != nil {
		return false, err
	}
	shellPath := cmd.Path
	shellArgs := append([]string{}, cmd.Args[1:]...)
	cmd.Path = self
	cmd.Args = append([]string{self, sandboxLaunchMarker, payload, shellPath}, shellArgs...)
	return true, nil
}

// sandboxLauncherEntry is SandboxLaunchEntry on linux: apply the sandbox to
// the current process, then replace it with the target command. Never
// returns: success is execve, every failure path exits non-zero (fail closed).
func sandboxLauncherEntry(payloadJSON string, target []string) {
	if err := applySandboxSelf(payloadJSON); err != nil {
		fmt.Fprintf(os.Stderr, "ggcode sandbox launcher: %v\n", err)
		os.Exit(126)
	}
	if len(target) == 0 {
		fmt.Fprintln(os.Stderr, "ggcode sandbox launcher: no target command")
		os.Exit(125)
	}
	if !filepath.IsAbs(target[0]) {
		resolved, err := exec.LookPath(target[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "ggcode sandbox launcher: %v\n", err)
			os.Exit(127)
		}
		target[0] = resolved
	}
	if err := syscall.Exec(target[0], target, os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "ggcode sandbox launcher: exec %s: %v\n", target[0], err)
		os.Exit(127)
	}
	// unreachable
}

// applySandboxSelf turns this process into its own sandbox: NO_NEW_PRIVS
// first (required for unprivileged seccomp and to lock out privilege
// escalation through setuid helpers), then Landlock for the filesystem and
// seccomp BPF for the network.
func applySandboxSelf(payloadJSON string) error {
	payload, err := decodeSandboxLaunchPayload(payloadJSON)
	if err != nil {
		return err
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("PR_SET_NO_NEW_PRIVS: %w", err)
	}
	if err := applyLandlockWriteDeny(payload.WritePaths); err != nil {
		return err
	}
	if !payload.AllowNetwork {
		if err := applySeccompNetworkDeny(); err != nil {
			return err
		}
	}
	debug.Log("sandbox", "linux launcher sandbox applied (write_paths=%d network_allowed=%v)", len(payload.WritePaths), payload.AllowNetwork)
	return nil
}

// applyLandlockWriteDeny creates a Landlock ruleset handling every write-side
// filesystem right the running kernel supports, adds a PATH_BENEATH rule per
// allow-listed directory, and restricts the current thread. Reads, execution
// and IPC remain unrestricted - the same deny-write/allow-list shape as the
// macOS Seatbelt profile.
func applyLandlockWriteDeny(allowPaths []string) error {
	// Probe the supported ABI: CREATE_RULESET with the VERSION flag returns
	// the highest supported ABI (0 on pre-5.13 kernels, EOPNOTSUPP when the
	// landlock LSM is not enabled in the running kernel's lsm= list).
	version, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		return fmt.Errorf("%w: Landlock probe failed (%v); kernel < 5.13 or landlock missing from lsm=", errSandboxUnavailable, errno)
	}
	abi := int(int32(version))
	handled, err := landlockWriteBits(abi)
	if err != nil {
		return fmt.Errorf("%w: %v", errSandboxUnavailable, err)
	}

	// Build the ruleset attribute sized to the probed ABI. The kernel
	// validates `size` against its own struct: too small is EINVAL on newer
	// kernels, too big (with unknown fields) is rejected by compat checks on
	// older ones. The prefix-struct layout matches C exactly (uint64 fields,
	// no padding), so each variant is wire-compatible with its ABI.
	var attrPtr unsafe.Pointer
	var attrSize uintptr
	switch {
	case abi >= 4:
		// ABI 4+ struct carries Access_fs + Access_net (+ Scoped in ABI 6);
		// only Access_fs is used, the rest stays zero.
		attr := unix.LandlockRulesetAttr{Access_fs: handled}
		attrPtr, attrSize = unsafe.Pointer(&attr), unsafe.Sizeof(attr)
	default:
		// ABI 1-3: the struct is exactly { __u64 access_fs }.
		type landlockRulesetABI3 struct{ AccessFS uint64 }
		attr := landlockRulesetABI3{AccessFS: handled}
		attrPtr, attrSize = unsafe.Pointer(&attr), unsafe.Sizeof(attr)
	}
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(attrPtr), attrSize, 0)
	if errno != 0 {
		return fmt.Errorf("%w: Landlock create ruleset: %v", errSandboxUnavailable, errno)
	}
	rulesetFD := int(fd)
	defer unix.Close(rulesetFD)

	opened := 0
	for _, path := range allowPaths {
		if path == "" || !filepath.IsAbs(path) {
			continue // defensive: the launcher trusts only absolute allow roots
		}
		dirFD, err := unix.Open(path, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			// A vanished allow root (e.g. a cleaned-up temp dir) must not
			// abort containment; the remaining roots still apply, matching
			// how a Seatbelt subpath on a missing directory behaves.
			debug.Log("sandbox", "landlock: skip vanished allow root %s: %v", path, err)
			continue
		}
		rule := unix.LandlockPathBeneathAttr{
			Allowed_access: handled,
			Parent_fd:      int32(dirFD),
		}
		if _, _, errno := unix.Syscall(unix.SYS_LANDLOCK_ADD_RULE, uintptr(rulesetFD), unix.LANDLOCK_RULE_PATH_BENEATH, uintptr(unsafe.Pointer(&rule))); errno != 0 {
			unix.Close(dirFD)
			return fmt.Errorf("%w: Landlock add rule %s: %v", errSandboxUnavailable, path, errno)
		}
		unix.Close(dirFD)
		opened++
	}
	debug.Log("sandbox", "landlock: %d/%d allow roots registered (abi=%d handled=0x%x)", opened, len(allowPaths), abi, handled)

	if _, _, errno := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, uintptr(rulesetFD), 0, 0); errno != 0 {
		return fmt.Errorf("%w: Landlock restrict self: %v", errSandboxUnavailable, errno)
	}
	return nil
}

// applySeccompNetworkDeny installs a cBPF filter that denies internet-family
// socket creation (AF_INET / AF_INET6 / AF_PACKET), returning EPERM to the
// caller. Unix-domain and netlink sockets remain available so basic tooling
// keeps working; this blocks outbound TCP/UDP but is not a full network
// namespace (mirrors Codex CLI's Landlock+seccomp tier, not its bubblewrap
// tier). PR_SET_NO_NEW_PRIVS must already be set (applySandboxSelf does).
func applySeccompNetworkDeny() error {
	prog, err := sbSeccompNetworkDenyFilter(runtime.GOARCH)
	if err != nil {
		return fmt.Errorf("%w: %v", errSandboxUnavailable, err)
	}
	filter := make([]unix.SockFilter, len(prog))
	for i, f := range prog {
		filter[i] = unix.SockFilter{Code: uint16(f.Code), Jt: uint8(f.Jt), Jf: uint8(f.Jf), K: f.K}
	}
	fprog := unix.SockFprog{Len: uint16(len(filter)), Filter: &filter[0]}
	if err := unix.Prctl(unix.PR_SET_SECCOMP, unix.SECCOMP_MODE_FILTER, uintptr(unsafe.Pointer(&fprog)), 0, 0); err != nil {
		return fmt.Errorf("%w: PR_SET_SECCOMP(SECCOMP_MODE_FILTER): %v (CONFIG_SECCOMP_FILTER disabled?)", errSandboxUnavailable, err)
	}
	return nil
}
