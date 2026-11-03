package restart

import (
	"fmt"
	"os"
	"strings"
	"text/template"
)

type Request struct {
	Binary string

	Args []string

	WorkDir string

	PID int
}

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

type templateData struct {
	PID      int
	Binary   string
	WorkDir  string
	Args     []string
	ArgsBash string
}

const scriptTemplate = `#!/bin/bash
# ggcode self-restart helper — auto-generated, self-deleting.
set -euo pipefail

PARENT_PID={{.PID}}
BINARY={{.Binary}}
WORK_DIR={{.WorkDir}}
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

# 2. Wait for parent's terminal cleanup (raw mode restore, etc.)
# The kill -0 loop only waits for process exit; the terminal may still
# be in raw mode for a brief moment after. Give it time to stty sane.
sleep 0.5

# 3. Force terminal to sane state as a safety net
if [ -t 0 ]; then
    stty sane 2>/dev/null || true
fi

# 4. cd to original working directory
cd "$WORK_DIR" 2>/dev/null || true

# 4. Launch new process — exec replaces this script
# #797: 'trap cleanup EXIT' never fires after a successful exec (bash only
# runs the fallback on exec FAILURE), so every normal restart leaked a
# temp script. Delete explicitly before exec.
rm -f "$SCRIPT" 2>/dev/null || true
echo "[ggcode restart] starting $BINARY ${ARGS[*]:-}"
if [ ${#ARGS[@]} -gt 0 ]; then
    exec "$BINARY" "${ARGS[@]}"
else
    exec "$BINARY"
fi
`

func bashEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

var _ = template.Must(template.New("restart").Parse(scriptTemplate))
