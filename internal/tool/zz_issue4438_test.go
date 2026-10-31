//go:build goolm

package tool

// #4438 regression:
//  1. dep_graph now evaluates //go:build constraints with the default
//     build.Context - tag-excluded files no longer pollute the graph.
//  2. reverse_deps reports ambiguous bare names and not-found targets
//     instead of silently unioning / returning nil.
//  3. deriveActivityLabel truncates by rune - no more invalid UTF-8 when
//     a CJK description exceeds 60 runes.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestIssue4438_BuildTaggedFilesExcludedFromGraph(t *testing.T) {
	dir := setupTestModule(t)
	// A file whose imports must NEVER enter the graph: excluded by a
	// non-default build tag (same class as the repo's own goolm guards).
	tagged := filepath.Join(dir, "zztagged", "tagged.go")
	if err := os.MkdirAll(filepath.Dir(tagged), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tagged, []byte("//go:build nonexistenttagzz\n\npackage zztagged\n\nimport _ \"example.com/onlytagged\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := DepGraphTool{WorkingDir: dir}
	input := mustJSON(t, map[string]any{
		"path":        dir,
		"action":      "reverse_deps",
		"target":      "onlytagged",
		"description": "test tagged file excluded",
	})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("tag-excluded import leaked into graph: %s (#4438)", result.Content)
	}
	if !contains(result.Content, "not found") {
		t.Fatalf("expected not-found error, got: %s (#4438)", result.Content)
	}
}

func TestIssue4438_ReverseDepsAmbiguityAndNotFound(t *testing.T) {
	graph := map[string]*pkgNode{
		"m/x/util":  {Path: "m/x/util", Imported: []string{"m/x/alpha"}},
		"m/y/util":  {Path: "m/y/util"},
		"m/x/alpha": {Path: "m/x/alpha", Imports: []string{"m/x/util"}},
	}
	// Bare "util" matches two distinct packages -> ambiguous error.
	if _, err := findReverseDeps(graph, "util", 30); err == nil {
		t.Fatalf("ambiguous target must error (#4438)")
	} else if !strings.Contains(err.Error(), "ambiguous") || !strings.Contains(err.Error(), "m/x/util") {
		t.Fatalf("error must list candidates: %v (#4438)", err)
	}
	// Full path disambiguates and returns the importer.
	deps, err := findReverseDeps(graph, "m/x/util", 30)
	if err != nil || len(deps) != 1 || deps[0] != "m/x/alpha" {
		t.Fatalf("full-path target: deps=%v err=%v (#4438)", deps, err)
	}
	// No match at all -> explicit not-found (was silent nil).
	if _, err := findReverseDeps(graph, "typo", 30); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing target must be explicit: %v (#4438)", err)
	}
}

func TestIssue4438_ActivityLabelRuneTruncation(t *testing.T) {
	// 70 CJK runes = 210 bytes: byte-truncation at 57 bytes split a rune
	// and produced invalid UTF-8.
	long := strings.Repeat("中", 70)
	label := deriveActivityLabel("edit_file", map[string]json.RawMessage{
		"path": json.RawMessage(`"` + long + `"`),
	})
	if !utf8.ValidString(label) {
		t.Fatalf("activity label is not valid UTF-8: %q (#4438)", label)
	}
	// Truncation applies to the DESCRIPTION part: "edit_file " prefix (10
	// runes) + at most 60 runes of description.
	if n := utf8.RuneCountInString(label); n > 10+60 {
		t.Fatalf("label not truncated: %d runes (#4438)", n)
	}
}
