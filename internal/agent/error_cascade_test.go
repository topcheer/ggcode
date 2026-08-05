package agent

import (
	"strings"
	"testing"
)

func TestExtractCascadeRoot_FilePath(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantKey string
		wantTyp cascadeRootType
	}{
		{
			"quoted file path",
			`edit_file failed: old_text not found in "internal/agent/agent.go"`,
			"agent/agent.go",
			cascadeRootFile,
		},
		{
			"bare unix path",
			"compile error in /Volumes/new/ggai/ggcode/internal/tool/foo.go:42",
			"tool/foo.go",
			cascadeRootFile,
		},
		{
			"file not found",
			"open /Volumes/new/ggai/ggcode/main.go: no such file or directory",
			"ggcode/main.go",
			cascadeRootFile,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, typ := extractCascadeRoot("edit_file", tt.content)
			if key != tt.wantKey {
				t.Errorf("key = %q, want %q", key, tt.wantKey)
			}
			if typ != tt.wantTyp {
				t.Errorf("type = %v, want %v", typ, tt.wantTyp)
			}
		})
	}
}

func TestExtractCascadeRoot_Symbol(t *testing.T) {
	// Symbol extraction content must NOT contain file paths, otherwise the
	// file-path regex takes priority.
	tests := []struct {
		name    string
		content string
		wantKey string
		wantTyp cascadeRootType
	}{
		{
			"undefined symbol",
			"compile error: undefined: NewFoobar",
			"newfoobar",
			cascadeRootSymbol,
		},
		{
			"undefined: dotted reference",
			"error: undefined: config.LoadConfig",
			"config.loadconfig",
			cascadeRootSymbol,
		},
		{
			"undeclared symbol",
			"undeclared: ProcessData",
			"processdata",
			cascadeRootSymbol,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, typ := extractCascadeRoot("run_command", tt.content)
			if key != tt.wantKey {
				t.Errorf("key = %q, want %q", key, tt.wantKey)
			}
			if typ != tt.wantTyp {
				t.Errorf("type = %v, want %v", typ, tt.wantTyp)
			}
		})
	}
}

func TestExtractCascadeRoot_None(t *testing.T) {
	key, typ := extractCascadeRoot("run_command", "exit status 1")
	if key != "" || typ != cascadeRootNone {
		t.Errorf("expected empty root, got key=%q type=%v", key, typ)
	}

	key, typ = extractCascadeRoot("edit_file", "")
	if key != "" || typ != cascadeRootNone {
		t.Errorf("expected empty root for empty content, got key=%q type=%v", key, typ)
	}
}

func TestExtractCascadeRoot_GoKeyword(t *testing.T) {
	// "undefined: func" should not match because "func" is a keyword.
	key, _ := extractCascadeRoot("run_command", "undefined: func")
	if key != "" {
		t.Errorf("expected empty key for Go keyword, got %q", key)
	}
}

func TestNormalizeFilePath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/a/b/c/d.go", "c/d.go"},
		{"/x.go", "x.go"},
		{"C:\\Users\\foo\\bar.go", "foo/bar.go"},
		{"", ""},
	}
	for _, tt := range tests {
		got := normalizeCascadePath(tt.input)
		if got != tt.want {
			t.Errorf("normalizeCascadePath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestErrorCascade_SoftThreshold(t *testing.T) {
	s := newErrorCascadeState()
	root := "agent/agent.go" // normalized form

	// First two errors: no guidance.
	for i := 0; i < 2; i++ {
		g := s.recordError("edit_file", `failed in "internal/agent/agent.go"`)
		if g != "" {
			t.Fatalf("iteration %d: expected no guidance, got %q", i, g)
		}
	}

	// Third error: soft guidance.
	g := s.recordError("run_command", `compile error in /code/internal/agent/agent.go:42`)
	if g == "" {
		t.Fatal("expected soft cascade guidance at 3rd error")
	}
	if !strings.Contains(g, "Error Cascade") {
		t.Errorf("guidance missing 'Error Cascade' marker: %q", g)
	}
	if !strings.Contains(g, root) {
		t.Errorf("guidance missing root key %q: %q", root, g)
	}

	// Fourth error: already fired, should not re-fire.
	g = s.recordError("grep", `error in "internal/agent/agent.go"`)
	if g != "" {
		t.Errorf("expected no re-fire after soft guidance, got %q", g)
	}
}

func TestErrorCascade_HardThreshold(t *testing.T) {
	s := newErrorCascadeState()

	// Record 4 errors with different tools but same root.
	tools := []string{"edit_file", "run_command", "grep", "read_file"}
	var lastGuidance string
	for i, tool := range tools {
		g := s.recordError(tool, `error in "internal/util/util.go"`)
		if i < 2 && g != "" {
			t.Fatalf("iteration %d: expected no guidance, got %q", i, g)
		}
		if i == 2 {
			if g == "" || !strings.Contains(g, "Error Cascade") {
				t.Fatalf("iteration %d: expected soft guidance, got %q", i, g)
			}
			lastGuidance = g
		}
	}
	_ = lastGuidance

	// Since it already fired at threshold 3, the 4th should not re-fire.
	g := s.recordError("run_command", `error in "internal/util/util.go"`)
	if g != "" {
		t.Errorf("expected no re-fire after fired=true, got %q", g)
	}
}

func TestErrorCascade_DifferentRootsNoCascade(t *testing.T) {
	s := newErrorCascadeState()

	// Errors on different files should NOT trigger cascade.
	s.recordError("edit_file", `error in "dir/file_a.go"`)
	s.recordError("run_command", `error in "dir/file_b.go"`)
	g := s.recordError("grep", `error in "dir/file_c.go"`)
	if g != "" {
		t.Errorf("expected no cascade for different roots, got %q", g)
	}
}

func TestErrorCascade_Reset(t *testing.T) {
	s := newErrorCascadeState()

	// Trigger cascade.
	for i := 0; i < 3; i++ {
		s.recordError("edit_file", `error in "foo/bar.go"`)
	}

	totalRoots, maxCluster, totalErrors := s.cascadeStats()
	if totalRoots != 1 {
		t.Errorf("expected 1 root, got %d", totalRoots)
	}
	if maxCluster != 3 {
		t.Errorf("expected max cluster 3, got %d", maxCluster)
	}
	if totalErrors != 3 {
		t.Errorf("expected 3 total errors, got %d", totalErrors)
	}

	// Reset.
	s.reset()
	totalRoots, maxCluster, totalErrors = s.cascadeStats()
	if totalRoots != 0 || maxCluster != 0 || totalErrors != 0 {
		t.Errorf("after reset: roots=%d max=%d errors=%d, want all 0",
			totalRoots, maxCluster, totalErrors)
	}

	// After reset, can trigger again.
	g := ""
	for i := 0; i < 3; i++ {
		g = s.recordError("edit_file", `error in "foo/bar.go"`)
	}
	if g == "" {
		t.Error("expected cascade to fire again after reset")
	}
}

func TestErrorCascade_SymbolCascade(t *testing.T) {
	s := newErrorCascadeState()

	// Three errors all referencing the same undefined symbol.
	// Content must not contain file paths (otherwise file regex takes priority).
	contents := []string{
		"build error: undefined: MyFunction",
		"link failed: undefined: MyFunction",
		"test failed: undefined: MyFunction",
	}
	var guidance string
	for i, c := range contents {
		guidance = s.recordError("run_command", c)
		if i < 2 && guidance != "" {
			t.Fatalf("iteration %d: expected no guidance yet, got %q", i, guidance)
		}
	}
	if guidance == "" {
		t.Fatal("expected cascade guidance at 3rd symbol error")
	}
	if !strings.Contains(strings.ToLower(guidance), "symbol") {
		t.Errorf("guidance should mention symbol type: %q", guidance)
	}
}

func TestErrorCascade_MemoryBound(t *testing.T) {
	s := newErrorCascadeState()

	// Record errors for more roots than cascadeMaxRoots.
	for i := 0; i < cascadeMaxRoots+10; i++ {
		content := `error in "dir/file_` + string(rune('a'+(i%26))) + `.go"`
		s.recordError("edit_file", content)
	}

	totalRoots, _, _ := s.cascadeStats()
	if totalRoots > cascadeMaxRoots {
		t.Errorf("roots %d exceeds max %d (memory bound violated)",
			totalRoots, cascadeMaxRoots)
	}
}

func TestErrorCascade_MaxRootsAllowSingleCluster(t *testing.T) {
	// When the max root is the same as the current root (high-count cluster),
	// it should not be evicted even under memory pressure.
	s := newErrorCascadeState()
	for i := 0; i < cascadeMaxRoots; i++ {
		s.recordError("edit_file", `error in "dir/file_`+string(rune('a'+(i%26)))+`.go"`)
	}
	// Add one more to trigger eviction.
	s.recordError("edit_file", `error in "dir/file_z.go"`)
	// No panic, bounded.
	_, _, totalErrors := s.cascadeStats()
	if totalErrors != cascadeMaxRoots+1 {
		t.Errorf("expected %d total errors, got %d", cascadeMaxRoots+1, totalErrors)
	}
}

func TestIsEditingTool(t *testing.T) {
	editTools := []string{"edit_file", "multi_edit_file", "multi_file_edit", "write_file", "notebook_edit"}
	for _, tool := range editTools {
		if !isEditingTool(tool) {
			t.Errorf("isEditingTool(%q) = false, want true", tool)
		}
	}
	nonEdit := []string{"read_file", "run_command", "grep", "search_files"}
	for _, tool := range nonEdit {
		if isEditingTool(tool) {
			t.Errorf("isEditingTool(%q) = true, want false", tool)
		}
	}
}
