package restart

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// Request contains all info needed to re-launch after exit.
type Request struct {
	// Binary is the path to the ggcode binary to launch.
	Binary string
	// Args are the CLI arguments for the new process (e.g. --resume, --config).
	Args []string
	// WorkDir is the working directory for the new process.
	WorkDir string
	// PID is the current process PID (the script waits for it to exit).
	PID int
}

// Launch writes a self-deleting helper script to /tmp and starts it.
// The caller should then initiate its own graceful shutdown
// (e.g. set quitting=true and return tea.Quit).
//
// The helper script waits for the current process (PID) to fully exit before
// launching the new one.
func Launch(req Request) error {
	if req.PID <= 0 {
		req.PID = os.Getpid()
	}
	if req.WorkDir == "" {
		wd, _ := os.Getwd()
		req.WorkDir = wd
	}

	scriptPath, err := writePlatformScript(req)
	if err != nil {
		return fmt.Errorf("write restart script: %w", err)
	}

	return launchPlatformScript(scriptPath, req)
}

// ResolveBinary returns the path to the ggcode binary that should be used
// for restart. It resolves symlinks so npm/python wrappers point to the
// latest installed version.
func ResolveBinary() (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	// Resolve symlinks — important for npm/python wrappers where the shim
	// points to a versioned directory.
	resolved, err := filepath.EvalSymlinks(execPath)
	if err != nil {
		return execPath, nil // best effort
	}
	return resolved, nil
}

// --- Unix-only types and helpers (used by restart_unix.go) ---

type templateData struct {
	PID            int
	BinaryEscaped  string
	WorkDirEscaped string
	Args           []string
	ArgsBash       string
}

const scriptTemplate = `#!/bin/bash
# ggcode self-restart helper — auto-generated, self-deleting.
set -euo pipefail

PARENT_PID={{.PID}}
BINARY="{{.BinaryEscaped}}"
WORK_DIR="{{.WorkDirEscaped}}"
{{- if .Args}}
ARGS=({{.ArgsBash}})
{{- else}}
ARGS=()
{{- end}}

SCRIPT="$(cd "$(dirname "$0")" && pwd)/$(basename "$0")"

cleanup() { rm -f "$SCRIPT" 2>/dev/null || true; }
trap cleanup EXIT

echo "[ggcode restart] waiting for process $PARENT_PID to exit..."

# 1. Wait for parent to fully exit (with timeout)
deadline=$((SECONDS + 30))
while kill -0 "$PARENT_PID" 2>/dev/null; do
    if [ $SECONDS -ge $deadline ]; then
        echo "[ggcode restart] ERROR: process $PARENT_PID did not exit within 30s" >&2
        exit 1
    fi
    sleep 0.1
done

echo "[ggcode restart] process $PARENT_PID exited, checking for residuals..."

# 2. Brief pause to let file descriptors / ttys release
sleep 0.3

# 3. cd to original working directory
cd "$WORK_DIR" 2>/dev/null || true

# 4. Launch new process — exec replaces this script
echo "[ggcode restart] starting $BINARY ${ARGS[*]}"
exec "$BINARY" "${ARGS[@]}"
`

// bashEscape wraps a string in single quotes, escaping embedded single quotes.
func bashEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// Platform-specific functions — implemented in restart_unix.go and restart_windows.go.
// writePlatformScript generates the helper script for the current OS.
// launchPlatformScript starts the script and returns immediately.
// These are defined in the build-tagged files.
var _ = template.Must(template.New("restart").Parse(scriptTemplate))
