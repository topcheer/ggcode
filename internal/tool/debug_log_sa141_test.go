package tool

// debug_log tool coverage (sa-141). Seeds the in-process ring buffer via
// debug.Log and exercises read/export/sanitization paths end to end.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/debug"
)

const sa141Marker = "sa141-marker-7f3d"

func seedSa141Ring(t *testing.T) {
	t.Helper()
	// Log's first arg is a package tag routed through tagToCategory - use
	// real categories so the read-side exact-category filter matches.
	debug.Log("agent", "entry one %s", sa141Marker)
	debug.Log("tui", "entry two without marker")
}

func TestDebugLogReadSa141(t *testing.T) {
	seedSa141Ring(t)
	tool := DebugLogTool{}
	ctx := context.Background()

	// Empty ring for an impossible category.
	r, err := tool.Execute(ctx, json.RawMessage(`{"category":"zz-no-such-cat-xyz"}`))
	if err != nil || r.IsError || !strings.Contains(r.Content, "No debug log entries found") {
		t.Fatalf("read empty category -> (%+v,%v)", r, err)
	}

	// Default action (missing) is read; category filter finds seeded entry.
	r, err = tool.Execute(ctx, json.RawMessage(`{"category":"agent"}`))
	if err != nil || r.IsError || !strings.Contains(r.Content, sa141Marker) {
		t.Fatalf("read seeded -> (%+v,%v)", r, err)
	}
	if !strings.Contains(r.Content, "Showing") {
		t.Fatalf("read header missing -> %+v", r)
	}

	// Invalid JSON.
	r, err = tool.Execute(ctx, json.RawMessage(`{`))
	if err != nil || !r.IsError || !strings.Contains(r.Content, "invalid input") {
		t.Fatalf("invalid JSON -> (%+v,%v)", r, err)
	}
	// Note: the 20KB render-truncation branch is unreachable through the
	// ring buffer because truncateMessage caps messages at write time.
}

func TestDebugLogExportSa141(t *testing.T) {
	seedSa141Ring(t)
	tool := DebugLogTool{}
	ctx := context.Background()

	// No matching keyword.
	r, err := tool.Execute(ctx, json.RawMessage(`{"action":"export","keyword":"zz-no-match-xyz"}`))
	if err != nil || r.IsError || !strings.Contains(r.Content, "No entries matched filters") {
		t.Fatalf("export no match -> (%+v,%v)", r, err)
	}

	// Matching export writes a real file inside TempDir.
	r, err = tool.Execute(ctx, json.RawMessage(`{"action":"export","category":"agent","keyword":"`+sa141Marker+`"}`))
	if err != nil || r.IsError || !strings.Contains(r.Content, "Exported") {
		t.Fatalf("export ok -> (%+v,%v)", r, err)
	}
	idx := strings.Index(r.Content, "to:\n")
	if idx < 0 {
		t.Fatalf("export path missing -> %+v", r)
	}
	rest := r.Content[idx+4:]
	if nl := strings.Index(rest, "\n"); nl >= 0 {
		rest = rest[:nl]
	}
	rest = strings.TrimSpace(rest)
	if !strings.HasPrefix(rest, filepath.Join(os.TempDir(), "ggcode-debug-")) {
		t.Fatalf("export path %q outside TempDir naming scheme", rest)
	}
	data, rerr := os.ReadFile(rest)
	if rerr != nil {
		t.Fatalf("exported file unreadable: %v", rerr)
	}
	if !strings.Contains(string(data), sa141Marker) || !strings.Contains(string(data), "# ggcode debug log export") {
		t.Fatalf("exported content wrong: %.200s", data)
	}
	os.Remove(rest)
}

func TestSanitizeLogNamePartSa141(t *testing.T) {
	cases := map[string]string{
		"plain":          "plain",
		"a/b":            "a_b",
		"a\\\\b":         "a__b", // each backslash becomes one underscore
		"../../etc/pass": "____etc_pass",
		"nul\x00ctl\x01": "nul_ctl_",
		"":               "_",
		"del\x7f":        "del_",
	}
	for in, want := range cases {
		if got := sanitizeLogNamePart(in); got != want {
			t.Errorf("sanitizeLogNamePart(%q) = %q, want %q", in, got, want)
		}
	}
	if s := sanitizeLogNamePart(".."); strings.Contains(s, "..") {
		t.Fatalf("sanitized output still contains ..: %q", s)
	}
}
