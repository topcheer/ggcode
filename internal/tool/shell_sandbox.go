package tool

import (
	"errors"
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
	return false
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
