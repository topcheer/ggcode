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

	sandboxDeniedExtraMarkers = append(sandboxDeniedExtraMarkers, "permission denied")

	SandboxLaunchEntry = sandboxLauncherEntry
}

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

}

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

func applyLandlockWriteDeny(allowPaths []string) error {

	version, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		return fmt.Errorf("%w: Landlock probe failed (%v); kernel < 5.13 or landlock missing from lsm=", errSandboxUnavailable, errno)
	}
	abi := int(int32(version))
	handled, err := landlockWriteBits(abi)
	if err != nil {
		return fmt.Errorf("%w: %v", errSandboxUnavailable, err)
	}

	var attrPtr unsafe.Pointer
	var attrSize uintptr
	switch {
	case abi >= 4:

		attr := unix.LandlockRulesetAttr{Handled_access_fs: handled}
		attrPtr, attrSize = unsafe.Pointer(&attr), unsafe.Sizeof(attr)
	default:

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
			continue
		}
		dirFD, err := unix.Open(path, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {

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
