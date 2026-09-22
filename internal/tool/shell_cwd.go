package tool

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Shell working-directory persistence (environment statefulness).
//
// The run_command tool historically started every invocation in the agent's
// fixed WorkingDir, so a `cd` inside one command was invisible to the next —
// the model had to re-type `cd <dir> &&` on every call and could silently
// run commands in the wrong directory. Mature agent harnesses (Claude Code,
// Gemini CLI) instead persist the shell session working directory across
// calls, matching how a human terminal behaves.
//
// Implementation: a sentinel statement is appended to each command that
// prints the shell's final working directory. After the command completes,
// the harness parses the marker out of the output, validates the directory
// (absolute, exists, inside the workspace when one is set), and adopts it as
// the starting directory of the NEXT command. Commands that exit before the
// sentinel runs (explicit `exit`, `set -e` aborts, kill/timeout) leave the
// previous directory untouched — persistence is best-effort and the marker
// is never surfaced to the model.

// cwdMarker is the sentinel prefix printed by the trailing shell statement.
const cwdMarker = "__GGCODE_CWD__"

// shellCwdState holds the persisted working directory for one agent session.
// It lives on RunCommand as a pointer so Execute's value receiver can mutate
// shared state; a nil pointer (zero-value RunCommand literal) disables
// persistence entirely.
type shellCwdState struct {
	mu  sync.Mutex
	cwd string
}

func newShellCwdState() *shellCwdState { return &shellCwdState{} }

func (s *shellCwdState) get() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cwd
}

func (s *shellCwdState) set(dir string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cwd = dir
}

var (
	cwdPersistOnce     sync.Once
	cwdPersistDisabled bool
)

// cwdPersistenceEnabled reports whether shell cwd persistence is active.
// Opt out with GGCODE_SHELL_CWD_PERSIST=0 (also accepted: false/off/no).
func cwdPersistenceEnabled() bool {
	cwdPersistOnce.Do(func() {
		switch strings.ToLower(strings.TrimSpace(os.Getenv("GGCODE_SHELL_CWD_PERSIST"))) {
		case "0", "false", "off", "no":
			cwdPersistDisabled = true
		}
	})
	return !cwdPersistDisabled
}

// appendCwdSentinel appends the trailing statement that reports the shell's
// final working directory. The statement is separated by a NEWLINE, not `;`,
// so commands ending in `&` (or `&&`) don't turn into `& ;` syntax errors.
// shellName selects per-shell syntax (see util.ShellSpec.Name).
func appendCwdSentinel(command, shellName string) string {
	if shellName == "powershell" {
		return command + "\n" + `Write-Output "` + cwdMarker + `$(Get-Location)"`
	}
	// POSIX family: sh, bash, zsh, git-bash, dash...
	return command + "\n" + `printf '%s\n' "` + cwdMarker + `$(pwd -P)"`
}

// extractCwdMarker scans command output for the sentinel line, removes every
// marker line so the model never sees the noise, and returns the directory
// recorded by the LAST marker (later cd's win). ok is false when the command
// never printed a marker (early exit, timeout kill, persistence disabled).
func extractCwdMarker(output string) (cleaned, dir string, ok bool) {
	lines := strings.Split(output, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if d, has := parseCwdMarkerLine(line); has {
			dir = d
			continue
		}
		kept = append(kept, line)
	}
	if dir == "" {
		return output, "", false
	}
	return strings.Join(kept, "\n"), dir, true
}

// parseCwdMarkerLine returns the directory from a single marker line.
func parseCwdMarkerLine(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, cwdMarker) {
		return "", false
	}
	d := strings.TrimSpace(strings.TrimPrefix(t, cwdMarker))
	if d == "" {
		return "", false
	}
	return d, true
}

// lastCwdMarkerLine returns the directory from the last sentinel line in
// already-split output (command job ring buffers). Returns "" when absent.
func lastCwdMarkerLine(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if d, ok := parseCwdMarkerLine(lines[i]); ok {
			return d
		}
	}
	return ""
}

// stripCwdMarkerLines removes sentinel lines from job snapshot output.
func stripCwdMarkerLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if _, ok := parseCwdMarkerLine(line); ok {
			continue
		}
		out = append(out, line)
	}
	return out
}

// adoptableCwd validates a persisted directory for reuse as the next
// command's working directory: it must be absolute, exist, and — when the
// agent has a workspace — stay inside the workspace (defense in depth: the
// OS sandbox is still scoped to the workspace root, and a `cd` escaping it
// must not silently move execution outside).
func adoptableCwd(workspace, dir string) string {
	if dir == "" || !filepath.IsAbs(dir) {
		return ""
	}
	if workspace != "" && !pathWithinWorkspace(workspace, dir) {
		return ""
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return ""
	}
	return filepath.Clean(dir)
}

// pathWithinWorkspace reports whether target is root itself or beneath it.
// Both sides are symlink-resolved on a best-effort basis (macOS /tmp vs
// /private/tmp, worktree symlinks).
func pathWithinWorkspace(root, target string) bool {
	root = resolveRealPath(root)
	target = resolveRealPath(target)
	if root == "" || target == "" {
		return false
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func resolveRealPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

// workingDirForCommand picks the directory a new command starts in: the
// persisted shell cwd when persistence is active and the directory is still
// valid (absolute, exists, inside the workspace), else the fixed WorkingDir.
func (t RunCommand) workingDirForCommand() string {
	if t.cwdState != nil && cwdPersistenceEnabled() {
		if d := adoptableCwd(t.WorkingDir, t.cwdState.get()); d != "" {
			return d
		}
	}
	return t.WorkingDir
}
