package tool

import (
	"bytes"
	"go/format"
	"path/filepath"

	"github.com/topcheer/ggcode/internal/debug"
)

// formatGoBytes applies gofmt-style formatting to Go source files (.go).
// Non-Go files and code that fails to parse (e.g. an edit-in-progress or a
// generated fragment) are returned unchanged with changed=false — this never
// corrupts the agent's output. Returns the (possibly reformatted) bytes and
// whether formatting changed the content.
func formatGoBytes(path string, data []byte) ([]byte, bool) {
	if filepath.Ext(path) != ".go" {
		return data, false
	}
	formatted, err := format.Source(data)
	if err != nil {
		return data, false
	}
	if bytes.Equal(formatted, data) {
		return data, false
	}
	debug.Log("tool", "auto-format: %s (%d → %d bytes)", filepath.Base(path), len(data), len(formatted))
	return formatted, true
}
