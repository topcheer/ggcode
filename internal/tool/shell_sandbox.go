package tool

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// SandboxPolicy is the OS-level containment layer for agent-driven shell
// execution (run_command / start_command). It is deliberately independent of
// the Go-level PathSandbox in internal/permission: that layer judges file
// tool calls by path arithmetic, while this one asks the kernel to enforce
// the same containment for arbitrary shell-spawned processes - the last
// layer that still works when a heuristic gate, a quoting bug, or an
// unvetted build script escapes the logical checks.
//
// Containment model ("container-lite", mirroring the sandbox profiles used
// by mainstream coding agents):
//   - everything is allowed by default,
//   - file WRITES are denied outside an allow-list (workspace, temp dirs,
//     /dev/null, plus sandbox.extra_write_paths),
//   - network sockets are denied when sandbox.allow_network is false.
//
// The policy is opt-in via config and applies to every agent-driven shell
// spawn once enabled (supervised approvals and heuristic gates stay on top
// of it unchanged).
type SandboxPolicy struct {
	Enabled         bool
	AllowNetwork    bool
	ExtraWritePaths []string
}

// NewSandboxPolicy builds a policy from config values. allowNetwork defaults
// to true when allowNetworkPtr is nil.
func NewSandboxPolicy(enabled bool, allowNetworkPtr *bool, extra []string) *SandboxPolicy {
	allowNetwork := true
	if allowNetworkPtr != nil {
		allowNetwork = *allowNetworkPtr
	}
	return &SandboxPolicy{Enabled: enabled, AllowNetwork: allowNetwork, ExtraWritePaths: extra}
}

// errSandboxUnavailable is returned when the policy is enabled but the
// platform cannot enforce it. The tool then fails closed: a user who opted
// into kernel-level containment must never get a silently unsandboxed run.
var errSandboxUnavailable = errors.New("sandbox.enabled is set but this platform cannot enforce an OS-level sandbox for shell commands")

// wrapShellCommandOS rewrites an already-built shell command
// (cmd = <shell> -c <command>) so it runs under the platform sandbox.
// Returns true when the command was wrapped. A non-nil error means the
// caller must fail the tool call instead of running the command unsandboxed.
func wrapShellCommandOS(cmd *exec.Cmd, workspace string, p *SandboxPolicy) (bool, error) {
	if p == nil || !p.Enabled {
		return false, nil
	}
	wrapped, err := sandboxWrap(cmd, workspace, p)
	if err != nil {
		debug.Log("sandbox", "OS sandbox wrap failed: %v", err)
		return false, err
	}
	if wrapped {
		debug.Log("sandbox", "shell command wrapped in OS sandbox (workspace=%s network_allowed=%v)", workspace, p.AllowNetwork)
	}
	return wrapped, nil
}

// sandboxEPERMHint is appended to command failures that look like sandbox
// denials, so the agent can adapt instead of retrying blindly.
const sandboxEPERMHint = "\n[hint] This failure looks like an OS sandbox write denial. The sandbox allows writes inside the workspace, temp directories and sandbox.extra_write_paths only; extend that config list (e.g. ~/go/pkg/mod, ~/.cache) or set sandbox.enabled=false if the write is legitimate."

// sandboxDeniedOutput reports whether combined command output indicates a
// sandbox-enforced permission failure. Seatbelt surfaces denials as EPERM
// ("Operation not permitted"), shell tools phrase the same errno variously
// ("permission denied, errno = 1", "eperm"), and read-only FS mounts show up
// as "read-only file system". Matching is case-insensitive over a small
// curated set to avoid false positives on ordinary tool errors.
//
// Platforms may extend the marker set via init (Linux Landlock surfaces
// EACCES "Permission denied", which is far too generic to add globally but
// correct once a kernel sandbox is known to be active).
var sandboxDeniedExtraMarkers []string

func sandboxDeniedOutput(output string) bool {
	lower := strings.ToLower(output)
	for _, marker := range []string{
		"operation not permitted",
		"eperm",
		"read-only file system",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	for _, marker := range sandboxDeniedExtraMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Linux sandbox: Landlock (filesystem) + seccomp (network) via self re-exec.
//
// On macOS the sandbox wraps commands with sandbox-exec, an external binary.
// Linux has no portable external wrapper: Landlock and seccomp must be
// applied with the sandboxed process's OWN privileges (they thread-restrict,
// they cannot restrict a separate child), and bubblewrap is not guaranteed to
// exist. The pragmatic pattern - used by OpenAI Codex CLI's Linux sandbox -
// is self re-exec: run_command rewrites the command to launch ggcode itself
// with a launcher marker; the launcher applies Landlock (deny writes outside
// the allow list) and a seccomp BPF filter (deny internet sockets) to ITSELF
// via prctl, then execve(2)s the real shell. The shell therefore starts
// already restricted; no privileges are elevated (PR_SET_NO_NEW_PRIVS first,
// and unprivileged Landlock/seccomp are used exclusively).
//
// The pure (OS-independent) parts of that protocol - the marker, the payload
// codec, the allow-path builder, the Landlock access-bit table and the
// seccomp BPF program builder - live here so they compile and unit-test on
// every platform. The syscall layer lives in shell_sandbox_linux.go.
// ---------------------------------------------------------------------------

// sandboxLaunchMarker is the argv[1] token that routes a ggcode process into
// the sandbox launcher instead of cobra. The double-underscore shape is
// deliberately not a flag: if routing ever failed, cobra would report an
// unknown argument instead of silently misinterpreting it.
const sandboxLaunchMarker = "__ggcode_sandbox_launch"

// SandboxLaunchMarker is exported for the cmd/ggcode main hook, which must
// intercept the marker before any command-tree wiring runs.
const SandboxLaunchMarker = sandboxLaunchMarker

// SandboxLaunchEntry is set by the linux build (init in
// shell_sandbox_linux.go) to the launcher entry point. It is nil on platforms
// without a launcher; main() treats that as an internal error and exits
// non-zero rather than running the target unsandboxed.
var SandboxLaunchEntry func(payloadJSON string, target []string)

// sandboxLaunchPayload is the JSON argv[2] handed to the launcher. It is
// written by the parent process and consumed by the launcher before any
// untrusted command runs, both sides being the same user's ggcode binary.
type sandboxLaunchPayload struct {
	// WritePaths are absolute, symlink-resolved directories that remain
	// writable under Landlock. Everything outside them is write-denied.
	WritePaths []string `json:"write_paths"`
	// AllowNetwork mirrors SandboxPolicy.AllowNetwork; when false the
	// launcher installs a seccomp BPF filter that denies internet-family
	// sockets (AF_INET/AF_INET6/AF_PACKET). Unix and netlink sockets stay
	// available so basic tooling (getaddrinfo via nss, iproute2) still works.
	AllowNetwork bool `json:"allow_network"`
}

// sandboxLaunchPayloadMaxBytes bounds the argv payload. The workspace and
// extra write paths are user config; a few directories are normal, and
// anything approaching this cap is far beyond a sane allow list.
const sandboxLaunchPayloadMaxBytes = 32 * 1024

func encodeSandboxLaunchPayload(p sandboxLaunchPayload) (string, error) {
	body, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("sandbox launch payload: %w", err)
	}
	if len(body) > sandboxLaunchPayloadMaxBytes {
		return "", fmt.Errorf("%w: sandbox launch payload is %d bytes, over the %d cap", errSandboxUnavailable, len(body), sandboxLaunchPayloadMaxBytes)
	}
	return string(body), nil
}

func decodeSandboxLaunchPayload(s string) (sandboxLaunchPayload, error) {
	var p sandboxLaunchPayload
	if len(s) == 0 || len(s) > sandboxLaunchPayloadMaxBytes {
		return p, fmt.Errorf("sandbox launch payload: invalid length %d", len(s))
	}
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		return p, fmt.Errorf("sandbox launch payload: %w", err)
	}
	return p, nil
}

// sandboxWriteAllowPaths builds the writable directory set for the Linux
// launcher: /dev/null, the per-user temp dir, /tmp, the workspace and
// sandbox.extra_write_paths - each symlink-resolved AND kept in its original
// cleaned spelling (mirroring the Seatbelt profile, because builds may reach
// the same directory through either form, e.g. /tmp vs /private/tmp-style
// aliasing on containerized roots). Duplicates are removed; order is stable.
func sandboxWriteAllowPaths(workspace string, p *SandboxPolicy) []string {
	var out []string
	add := func(path string) {
		if path == "" {
			return
		}
		resolved, cleaned := resolveSandboxPath(path)
		for _, form := range []string{resolved, cleaned} {
			if form == "" {
				continue
			}
			dup := false
			for _, existing := range out {
				if existing == form {
					dup = true
					break
				}
			}
			if !dup {
				out = append(out, form)
			}
		}
	}
	add("/dev/null")
	add(os.TempDir())
	add("/tmp")
	add(workspace)
	if p != nil {
		for _, extra := range p.ExtraWritePaths {
			add(extra)
		}
	}
	return out
}

// Landlock filesystem access bits (UAPI linux/landlock.h). Values are kept
// as local constants instead of golang.org/x/sys/unix identifiers because
// this table lives in the shared, OS-neutral layer (the same bits are needed
// on every GOOS for the pure builder and its tests). Source: Linux v5.13
// ABI 1 base set; REFER added in ABI 2 (v5.19); TRUNCATE in ABI 3 (v6.2).
// Read-side bits are intentionally absent: reads and directory listing stay
// unrestricted, mirroring the Seatbelt "deny file-write*" policy shape.
const (
	landlockAccessWriteFile  = 0x0002
	landlockAccessRemoveFile = 0x0020
	landlockAccessRemoveDir  = 0x0010
	landlockAccessMakeChar   = 0x0040
	landlockAccessMakeDir    = 0x0080
	landlockAccessMakeReg    = 0x0100
	landlockAccessMakeSock   = 0x0200
	landlockAccessMakeFifo   = 0x0400
	landlockAccessMakeBlock  = 0x0800
	landlockAccessMakeSym    = 0x1000
	landlockAccessRefer      = 0x2000 // ABI 2+
	landlockAccessTruncate   = 0x4000 // ABI 3+
)

// landlockWriteBits returns the access-right set the ruleset handles for a
// given Landlock ABI version. A ruleset must NOT reference bits the running
// kernel does not know (landlock_add_rule rejects unknown bits with EINVAL),
// so the set grows with the probed ABI. ABI 0 or negative means Landlock is
// unavailable.
func landlockWriteBits(abi int) (uint64, error) {
	if abi < 1 {
		return 0, fmt.Errorf("Landlock ABI %d unsupported (kernel < 5.13 or landlock LSM disabled)", abi)
	}
	bits := uint64(landlockAccessWriteFile | landlockAccessRemoveFile |
		landlockAccessRemoveDir | landlockAccessMakeChar | landlockAccessMakeDir |
		landlockAccessMakeReg | landlockAccessMakeSock | landlockAccessMakeFifo |
		landlockAccessMakeBlock | landlockAccessMakeSym)
	if abi >= 2 {
		bits |= landlockAccessRefer
	}
	if abi >= 3 {
		bits |= landlockAccessTruncate
	}
	return bits, nil
}

// --- seccomp BPF program (network deny), pure builder ----------------------
//
// The filter follows the cBPF layout of struct sock_filter. Constants are
// local (not unix.*) because the builder must compile on darwin for tests.
// Program logic, for the native architecture only:
//
//	arch != native                     -> ERRNO(ENOSYS)   (32-bit/foreign shells must not fall through)
//	socket(AF_INET/INET6/PACKET)       -> ERRNO(EPERM)
//	socketcall(SYS_SOCKET) [amd64]     -> ERRNO(EPERM)
//	everything else                    -> ALLOW
//
// seccomp_data field offsets: nr @ 0, arch @ 4, instruction_pointer @ 8,
// args[0] @ 16.

type sbSockFilter struct {
	Code uint32
	Jt   uint32
	Jf   uint32
	K    uint32
}

const (
	sbBpfLdWAbs = 0x20 // BPF_LD | BPF_W | BPF_ABS
	sbBpfJeqK   = 0x15 // BPF_JMP | BPF_JEQ | BPF_K
	sbBpfRetK   = 0x06 // BPF_RET | BPF_K

	sbSeccompRetAllow  = 0x7fff0000
	sbSeccompErrnoBase = 0x00050000
)

// UAPI constants duplicated for the shared layer (values fixed by the kernel
// ABI, stable across versions).
const (
	sbSeccompDataNr    = 0
	sbSeccompDataArch  = 4
	sbSeccompDataArgs0 = 16

	sbAuditArchX8664   = 0xc000003e
	sbAuditArchAarch64 = 0xc00000b7

	sbSysSocket           = 41  // amd64
	sbSysSocketArm        = 198 // arm64
	sbSysSocketcall       = 102 // amd64 only
	sbSocketcallSysSocket = 1

	sbAfInet   = 2
	sbAfInet6  = 10
	sbAfPacket = 17

	sbENosys = 38
	sbEperm  = 1
)

// sbSeccompNetworkDenyFilter builds the BPF program denying internet-family
// socket creation for the given GOARCH. Unsupported architectures fail with
// an error so the launcher fails closed instead of installing a filter it
// cannot reason about.
//
// The two arch layouts are written out literally (not with computed emit/rel
// helpers) so every jump offset is visible and auditable; the simulated
// interpreter in shell_sandbox_test.go re-verifies every path.
func sbSeccompNetworkDenyFilter(goarch string) ([]sbSockFilter, error) {
	errnoRet := func(errno uint32) uint32 { return sbSeccompErrnoBase | errno }
	retAllow := sbSockFilter{Code: sbBpfRetK, K: sbSeccompRetAllow}
	retEperm := sbSockFilter{Code: sbBpfRetK, K: errnoRet(sbEperm)}
	retEnosys := sbSockFilter{Code: sbBpfRetK, K: errnoRet(sbENosys)}
	ldArch := sbSockFilter{Code: sbBpfLdWAbs, K: sbSeccompDataArch}
	ldNr := sbSockFilter{Code: sbBpfLdWAbs, K: sbSeccompDataNr}
	ldArgs0 := sbSockFilter{Code: sbBpfLdWAbs, K: sbSeccompDataArgs0}

	switch goarch {
	case "amd64":
		//  0: LD  arch
		//  1: JEQ 0xc000003e  jt->2  jf->12 (ENOSYS)
		//  2: LD  nr
		//  3: JEQ 41 (socket) jt->6  jf->4 (socketcall)
		//  4: LD  args[0] (socketcall call number)
		//  5: JEQ 1 (SYS_SOCKET) jt->11 (EPERM) jf->10 (ALLOW)
		//  6: LD  args[0] (socket domain)
		//  7: JEQ 2  (AF_INET)   jt->11 (EPERM) jf->next
		//  8: JEQ 10 (AF_INET6)  jt->11 (EPERM) jf->next
		//  9: JEQ 17 (AF_PACKET) jt->11 (EPERM) jf->10 (ALLOW)
		// 10: RET ALLOW
		// 11: RET EPERM
		// 12: RET ENOSYS
		return []sbSockFilter{
			ldArch,
			{Code: sbBpfJeqK, Jt: 0, Jf: 10, K: sbAuditArchX8664},
			ldNr,
			{Code: sbBpfJeqK, Jt: 2, Jf: 0, K: sbSysSocket},
			ldArgs0,
			{Code: sbBpfJeqK, Jt: 5, Jf: 4, K: sbSocketcallSysSocket},
			ldArgs0,
			{Code: sbBpfJeqK, Jt: 3, Jf: 0, K: sbAfInet},
			{Code: sbBpfJeqK, Jt: 2, Jf: 0, K: sbAfInet6},
			{Code: sbBpfJeqK, Jt: 1, Jf: 0, K: sbAfPacket},
			retAllow,
			retEperm,
			retEnosys,
		}, nil
	case "arm64":
		//  0: LD  arch
		//  1: JEQ 0xc00000b7  jt->2  jf->10 (ENOSYS)
		//  2: LD  nr
		//  3: JEQ 198 (socket) jt->4  jf->8 (ALLOW)
		//  4: LD  args[0] (socket domain)
		//  5: JEQ 2  (AF_INET)   jt->9 (EPERM) jf->next
		//  6: JEQ 10 (AF_INET6)  jt->9 (EPERM) jf->next
		//  7: JEQ 17 (AF_PACKET) jt->9 (EPERM) jf->8 (ALLOW)
		//  8: RET ALLOW
		//  9: RET EPERM
		// 10: RET ENOSYS
		return []sbSockFilter{
			ldArch,
			{Code: sbBpfJeqK, Jt: 0, Jf: 8, K: sbAuditArchAarch64},
			ldNr,
			{Code: sbBpfJeqK, Jt: 0, Jf: 4, K: sbSysSocketArm},
			ldArgs0,
			{Code: sbBpfJeqK, Jt: 3, Jf: 0, K: sbAfInet},
			{Code: sbBpfJeqK, Jt: 2, Jf: 0, K: sbAfInet6},
			{Code: sbBpfJeqK, Jt: 1, Jf: 0, K: sbAfPacket},
			retAllow,
			retEperm,
			retEnosys,
		}, nil
	default:
		return nil, fmt.Errorf("seccomp network deny: unsupported arch %q (amd64/arm64 only)", goarch)
	}
}

// seatbeltEscaped rejects profile-unsafe paths. Seatbelt profile strings are
// not shell strings; rather than implementing escaping for a security
// boundary, refuse exotic paths and fail closed.
func seatbeltEscaped(path string) bool {
	return !strings.ContainsAny(path, "\"\\")
}

// resolveSandboxPath returns the canonical form of path plus the original
// cleaned form. Symlinks are resolved so the profile can list both spellings
// (macOS: /tmp vs /private/tmp) and containment cannot be dodged by spelling.
func resolveSandboxPath(path string) (resolved, cleaned string) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", ""
	}
	cleaned = filepath.Clean(abs)
	original := cleaned
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		return filepath.Clean(resolved), cleaned
	}
	// Non-existing path: resolve the longest existing prefix so e.g. a
	// not-yet-created output dir still lands under a real allowed root,
	// keeping the missing relative structure intact (root/out/new.txt).
	dir := filepath.Dir(original)
	for dir != original {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			root := filepath.Clean(real)
			if rel, rerr := filepath.Rel(root, original); rerr == nil && rel != ".." && !strings.HasPrefix(rel, "..") {
				return filepath.Join(root, rel), original
			}
			return filepath.Join(root, filepath.Base(original)), original
		}
		dir = filepath.Dir(dir)
	}
	return original, original
}
