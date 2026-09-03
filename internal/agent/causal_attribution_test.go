package agent

import (
	"testing"
)

func TestCausalAttribution_RecordAndAttribute(t *testing.T) {
	s := newCausalAttributionState()

	// Record some edit steps
	s.recordEdit("read_file", "internal/agent/foo.go", 1) // not an edit tool
	s.recordEdit("edit_file", "internal/agent/confidence.go", 2)
	s.recordEdit("edit_file", "internal/agent/agent.go", 3)
	s.recordEdit("write_file", "internal/agent/causal_attribution.go", 4)

	if len(s.edits) != 3 {
		t.Fatalf("expected 3 edits (read_file excluded), got %d", len(s.edits))
	}

	// Simulate a build failure referencing confidence.go
	output := `# internal/agent
./confidence.go:42:10: undefined: fooBar
FAIL	github.com/ggcode/internal/agent [build failed]`

	hint := s.attributeFailure(output)
	if hint == "" {
		t.Fatal("expected attribution guidance, got empty")
	}

	// Should identify confidence.go as the likely cause
	if !contains(hint, "confidence.go") {
		t.Errorf("expected hint to reference confidence.go, got: %s", hint)
	}
	if !contains(hint, "CRS=") {
		t.Errorf("expected hint to contain CRS score, got: %s", hint)
	}
	if !contains(hint, "step 2") {
		t.Errorf("expected hint to reference step 2, got: %s", hint)
	}
}

func TestCausalAttribution_SamePackage(t *testing.T) {
	s := newCausalAttributionState()

	s.recordEdit("edit_file", "internal/agent/bgorphan_detect.go", 1)
	s.recordEdit("edit_file", "internal/agent/confidence.go", 2)

	// Error references a different file in the same package
	output := `./internal/agent/other.go:10:5: cannot use foo (type int) as type string
FAIL	github.com/ggcode/internal/agent`

	hint := s.attributeFailure(output)
	// Should still provide attribution based on same-package match
	if hint == "" {
		t.Fatal("expected attribution for same-package error, got empty")
	}
}

func TestCausalAttribution_NoEdits(t *testing.T) {
	s := newCausalAttributionState()
	hint := s.attributeFailure("build failed")
	if hint != "" {
		t.Errorf("expected empty hint with no edits, got: %s", hint)
	}
}

func TestCausalAttribution_NoFailure(t *testing.T) {
	s := newCausalAttributionState()
	s.recordEdit("edit_file", "main.go", 1)

	// Successful output — no failure indicators
	hint := s.attributeFailure("ok\tpackage main")
	if hint != "" {
		t.Errorf("expected empty hint for successful output, got: %s", hint)
	}
}

func TestCausalAttribution_MaxWarnings(t *testing.T) {
	s := newCausalAttributionState()
	s.recordEdit("edit_file", "main.go", 1)
	s.recordEdit("edit_file", "main.go", 2)
	s.recordEdit("edit_file", "main.go", 3)

	failureOutput := "main.go:10: undefined: foo\nFAIL"

	// First 3 warnings should work
	for i := 0; i < causalMaxWarnings; i++ {
		hint := s.attributeFailure(failureOutput)
		if hint == "" {
			t.Fatalf("expected hint on call %d, got empty", i)
		}
	}

	// 4th should be suppressed
	hint := s.attributeFailure(failureOutput)
	if hint != "" {
		t.Errorf("expected hint suppression after max warnings, got: %s", hint)
	}
}

func TestCausalAttribution_Reset(t *testing.T) {
	s := newCausalAttributionState()
	s.recordEdit("edit_file", "main.go", 1)
	s.warnings = 2

	s.reset()

	if len(s.edits) != 0 {
		t.Errorf("expected edits cleared after reset, got %d", len(s.edits))
	}
	if s.warnings != 0 {
		t.Errorf("expected warnings cleared after reset, got %d", s.warnings)
	}
}

func TestCausalAttribution_SlidingWindow(t *testing.T) {
	s := newCausalAttributionState()

	// Record more than causalMaxEdits
	for i := 0; i < causalMaxEdits+5; i++ {
		s.recordEdit("edit_file", "main.go", i)
	}

	if len(s.edits) > causalMaxEdits {
		t.Errorf("expected at most %d edits, got %d", causalMaxEdits, len(s.edits))
	}
}

func TestCausalAttribution_ExtractErrorFiles(t *testing.T) {
	output := `# pkg
./internal/agent/foo.go:42: undefined: bar
./internal/agent/baz.go:10: cannot use x
ok	other/pkg`
	files := extractErrorFiles(output)
	if len(files) != 2 {
		t.Fatalf("expected 2 error files, got %d: %v", len(files), files)
	}
}

func TestExtractErrorFiles_None(t *testing.T) {
	output := "build succeeded"
	files := extractErrorFiles(output)
	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d", len(files))
	}
}

func TestCausalAttribution_NonEditTool(t *testing.T) {
	s := newCausalAttributionState()
	s.recordEdit("grep", "somefile.go", 1)
	s.recordEdit("read_file", "other.go", 2)
	s.recordEdit("search_files", "third.go", 3)

	if len(s.edits) != 0 {
		t.Errorf("expected 0 edits for non-edit tools, got %d", len(s.edits))
	}
}

func TestCausalAttribution_EmptyPath(t *testing.T) {
	s := newCausalAttributionState()
	s.recordEdit("edit_file", "", 1)
	s.recordEdit("edit_file", "  ", 2)

	if len(s.edits) != 0 {
		t.Errorf("expected 0 edits for empty paths, got %d", len(s.edits))
	}
}

func TestLooksLikeFailure(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"ok\tpackage main", false},
		{"PASS", false},
		{"undefined: foo", true},
		{"FAIL\tinternal/agent", true},
		{"panic: runtime error", true},
		{"build failed", true},
		{"compilation error", true},
		{"everything looks great", false},
	}

	for _, tt := range tests {
		got := looksLikeFailure(tt.input)
		if got != tt.want {
			t.Errorf("looksLikeFailure(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestCausalAttribution_RecencyBonus(t *testing.T) {
	s := newCausalAttributionState()

	// Two edits to same package, neither matches error file exactly
	s.recordEdit("edit_file", "internal/agent/old.go", 1)
	s.recordEdit("edit_file", "internal/agent/new.go", 2)

	// Error references a third file in the same package
	output := "./internal/agent/third.go:10: syntax error\nFAIL"

	hint := s.attributeFailure(output)
	if hint == "" {
		t.Fatal("expected attribution hint")
	}
	// More recent edit (new.go) should be preferred due to recency bonus
	if !contains(hint, "new.go") {
		t.Errorf("expected most recent edit (new.go) to be identified as cause, got: %s", hint)
	}
}

// TestCausalAttribution_MultiLineGrepNotCmdOutput pins #1442-A layer 2:
// a multi-line grep-shaped output with a stray failure word and NO
// compiler/tester feature word must not qualify as command output - its
// path:line: lines are causalErrorFileRe's exact shape and used to blame
// innocent edits (probe CRS=84 on a passing test's grep).
func TestCausalAttribution_MultiLineGrepNotCmdOutput(t *testing.T) {
	s := newCausalAttributionState()
	s.recordEdit("edit_file", "internal/im/crypto.go", 1)

	// 6 lines of grep output mentioning 'failed' in content - no feature word.
	grepish := "internal/im/crypto.go:40: msg := fmt.Errorf(\"failed to decrypt\")\n" +
		"internal/im/crypto.go:41: // if decryption failed we bail\n" +
		"internal/im/crypto.go:42: return nil, err\n" +
		"internal/im/keys.go:10: key, _ := pem.Decode(block)\n" +
		"internal/im/keys.go:11: // decode failed case\n" +
		"internal/im/keys.go:12: return nil\n"
	if hint := s.attributeFailure(grepish); hint != "" {
		t.Fatalf("multi-line grep output misattributed: %s", hint)
	}

	// The same shape WITH a compiler feature word still attributes.
	compilery := "./internal/im/crypto.go:10:2: cannot use err (type error)\nFAIL\tgithub.com/x/y\t0.5s\n"
	s2 := newCausalAttributionState()
	s2.recordEdit("edit_file", "internal/im/crypto.go", 1)
	if hint := s2.attributeFailure(compilery); hint == "" {
		t.Fatal("real compiler failure no longer attributes")
	}
}
