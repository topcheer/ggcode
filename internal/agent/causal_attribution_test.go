package agent

import (
	"testing"
)

func TestCausalAttribution_RecordAndAttribute(t *testing.T) {
	s := newCausalAttributionState()

	s.recordEdit("read_file", "internal/agent/foo.go", 1)
	s.recordEdit("edit_file", "internal/agent/confidence.go", 2)
	s.recordEdit("edit_file", "internal/agent/agent.go", 3)
	s.recordEdit("write_file", "internal/agent/causal_attribution.go", 4)

	if len(s.edits) != 3 {
		t.Fatalf("expected 3 edits (read_file excluded), got %d", len(s.edits))
	}

	output := `# internal/agent
./confidence.go:42:10: undefined: fooBar
FAIL	github.com/ggcode/internal/agent [build failed]`

	sus := s.attributeFailure(output)
	if sus == nil {
		t.Fatal("expected attribution, got none")
	}
	if !contains(sus.step.filePath, "confidence.go") {
		t.Errorf("expected suspect confidence.go, got: %+v", sus.step)
	}
	if s.lastSuspectCRS <= 0 {
		t.Errorf("expected a positive CRS score, got %d", s.lastSuspectCRS)
	}
	if sus.step.iteration != 2 {
		t.Errorf("expected suspect step 2, got: %+v", sus.step)
	}
}

func TestCausalAttribution_SamePackage(t *testing.T) {
	s := newCausalAttributionState()

	s.recordEdit("edit_file", "internal/agent/bgorphan_detect.go", 1)
	s.recordEdit("edit_file", "internal/agent/confidence.go", 2)

	output := `./internal/agent/other.go:10:5: cannot use foo (type int) as type string
FAIL	github.com/ggcode/internal/agent`

	hint := s.attributeFailure(output)

	if hint == nil {
		t.Fatal("expected attribution for same-package error, got none")
	}
}

func TestCausalAttribution_NoEdits(t *testing.T) {
	s := newCausalAttributionState()
	if hint := s.attributeFailure("build failed"); hint != nil {
		t.Errorf("expected no attribution with no edits, got: %+v", hint.step)
	}
}

func TestCausalAttribution_NoFailure(t *testing.T) {
	s := newCausalAttributionState()
	s.recordEdit("edit_file", "main.go", 1)

	if hint := s.attributeFailure("ok\tpackage main"); hint != nil {
		t.Errorf("expected no attribution for successful output, got: %+v", hint.step)
	}
}

func TestCausalAttribution_Reset(t *testing.T) {
	s := newCausalAttributionState()
	s.recordEdit("edit_file", "main.go", 1)

	s.reset()

	if len(s.edits) != 0 {
		t.Errorf("expected edits cleared after reset, got %d", len(s.edits))
	}
}

func TestCausalAttribution_SlidingWindow(t *testing.T) {
	s := newCausalAttributionState()

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

func TestCausalAttribution_LastSuspectTracksTopSuspect(t *testing.T) {
	s := newCausalAttributionState()
	s.recordEdit("edit_file", "internal/tool/grep.go", 0)

	out := "# /repo/internal/tool/grep.go:12:3: undefined: Foo\nFAIL\tinternal/tool [build failed]\nexit status 1"
	if g := s.attributeFailureCmd(out, "go test ./internal/tool/", true); g == nil {
		t.Fatal("expected attribution for error-file match")
	}
	if s.lastSuspect != "internal/tool/grep.go" {
		t.Fatalf("lastSuspect = %q, want the attributed file (experiment would arm on empty)", s.lastSuspect)
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

	s.recordEdit("edit_file", "internal/agent/old.go", 1)
	s.recordEdit("edit_file", "internal/agent/new.go", 2)

	output := "./internal/agent/third.go:10: syntax error\nFAIL"

	hint := s.attributeFailure(output)
	if hint == nil {
		t.Fatal("expected attribution")
	}

	if !contains(hint.step.filePath, "new.go") {
		t.Errorf("expected most recent edit (new.go) to be identified as cause, got: %+v", hint.step)
	}
}

func TestCausalAttribution_MultiLineGrepNotCmdOutput(t *testing.T) {
	s := newCausalAttributionState()
	s.recordEdit("edit_file", "internal/im/crypto.go", 1)

	grepish := "internal/im/crypto.go:40: msg := fmt.Errorf(\"failed to decrypt\")\n" +
		"internal/im/crypto.go:41: // if decryption failed we bail\n" +
		"internal/im/crypto.go:42: return nil, err\n" +
		"internal/im/keys.go:10: key, _ := pem.Decode(block)\n" +
		"internal/im/keys.go:11: // decode failed case\n" +
		"internal/im/keys.go:12: return nil\n"
	if hint := s.attributeFailure(grepish); hint != nil {
		t.Fatalf("multi-line grep output misattributed: %+v", hint.step)
	}

	compilery := "./internal/im/crypto.go:10:2: cannot use err (type error)\nFAIL\tgithub.com/x/y\t0.5s\n"
	s2 := newCausalAttributionState()
	s2.recordEdit("edit_file", "internal/im/crypto.go", 1)
	if hint := s2.attributeFailure(compilery); hint == nil {
		t.Fatal("real compiler failure no longer attributes")
	}
}

func TestComputeCRSRecencyAloneInsufficient(t *testing.T) {
	edit := causalEditStep{filePath: "/w/main.go", dirPath: "/w"}

	if got := computeCRS(edit, nil, 10); got != 0 {
		t.Fatalf("recency without file evidence must score 0, got %d", got)
	}

	if got := computeCRS(edit, []string{"/w/main.go"}, 5); got < causalWtErrorFileMatch {
		t.Fatalf("exact match + recency must include the file-match weight, got %d", got)
	}
}

func TestComputeCRSDetail_FileMatchNotInferableFromScore(t *testing.T) {
	edit := causalEditStep{filePath: "internal/agent/my_edit.go", dirPath: "internal/agent"}

	score, matched, _ := computeCRSDetail(edit, []string{"cmd/app/main.go:12: oops"}, 8, nil)
	if matched {
		t.Fatal("unrelated error file must not set fileMatch")
	}
	if score < causalWtErrorFileMatch {
		t.Logf("note: recency-heavy score %d still exceeds 50 - wording must rely on fileMatch only", score)
	}

	score2, matched2, _ := computeCRSDetail(edit, []string{"internal/agent/my_edit.go"}, 1, nil)
	if !matched2 || score2 < causalWtErrorFileMatch {
		t.Fatalf("exact match must set fileMatch with weight, got matched=%v score=%d", matched2, score2)
	}
}

func TestCausalAttribution_SuffixAmbiguityEndToEnd(t *testing.T) {
	s := newCausalAttributionState()
	s.recordEdit("edit_file", "pkg/api/types.go", 1)
	s.recordEdit("edit_file", "internal/api/types.go", 2)

	output := `api/types.go:12:5: undefined: Foo
FAIL	github.com/x/y [build failed]`

	hint := s.attributeFailure(output)
	if hint == nil {
		t.Fatal("expected attribution (weak tier), got none")
	}
	if hint.fileMatch {
		t.Fatalf("ambiguous suffix match must not count as file-tier evidence: %+v", hint)
	}
}
