package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const issue2803FileA = "a.go"
const issue2803FileB = "b.go"

func issue2803StateWithFixedImport(t *testing.T) *fixAmnesiaState {
	t.Helper()
	d := newFixAmnesiaState()

	d.recordErrorObserved("missing-import", issue2803FileA)
	d.recordFileEdited(issue2803FileA)
	return d
}

func TestIssue2803FullContentWithImportNoFalsePositive(t *testing.T) {
	d := issue2803StateWithFixedImport(t)
	full := "package b\n\nimport \"fmt\"\n\nfunc f() {\n\tfmt.Println(\"hi\")\n}\n"
	if got := d.checkContentAgainstFixed("", issue2803FileB, full); got != "" {
		t.Errorf("false positive on content that already imports fmt: %q", got)
	}
}

func TestIssue2803FullContentMissingImportStillWarns(t *testing.T) {
	d := issue2803StateWithFixedImport(t)
	full := "package b\n\nfunc f() {\n\tfmt.Println(\"hi\")\n}\n"
	if got := d.checkContentAgainstFixed("", issue2803FileB, full); got == "" {
		t.Error("expected guidance for genuinely missing import")
	}
}

func TestIssue2803WiringReadsRealFile(t *testing.T) {
	dir := t.TempDir()
	fileB := filepath.Join(dir, issue2803FileB)
	content := "package b\n\nimport \"fmt\"\n\nfunc f() {\n\tfmt.Println(\"hi\")\n}\n"
	if err := os.WriteFile(fileB, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	diffLike := "+\tmsg := fmt.Sprintf(\"x\")\n"
	d := issue2803StateWithFixedImport(t)
	if got := d.checkContentAgainstFixed("", fileB, diffLike); got == "" {
		t.Fatal("precondition: diff-like content without import line must trigger the detector (the #2803 bug shape)")
	}

}

func TestIssue2803CallSiteGated(t *testing.T) {
	// #5543: the edit-fallback site moved out of agent.go; scan the
	// stream-loop source files (shape-agnostic file targeting).
	s, err := readStreamLoopSource("agent.go", "run_tool_call_idx.go")
	if err != nil {
		t.Fatalf("read stream-loop source: %v", err)
	}
	callIdx := strings.Index(s, "checkContentAgainstFixed(extractFilePathFromError")
	if callIdx < 0 {
		t.Fatal("checkContentAgainstFixed call site not found in agent.go")
	}
	window := s[max(0, callIdx-1600):callIdx]
	if !strings.Contains(window, "!result.IsError") {
		t.Error("content check not success-gated - error-text FP source (review pt 2)")
	}
	if !strings.Contains(window, `fp != ""`) {
		t.Error("content check not path-gated (review pt 3)")
	}
	if !strings.Contains(window, "hasFixedPatternsInOtherFiles(fp)") {
		t.Error("content check not fixed-pattern-gated")
	}
	if !strings.Contains(window, "os.ReadFile(fp)") {
		t.Error("call site does not read the real post-edit file (#2803 recurrence)")
	}
	if strings.Contains(window, "newContent := result.Content") {
		t.Error("diff fallback re-introduced (review pt 1)")
	}
}

func TestIssue2803GateMirrorsSameFile(t *testing.T) {
	d := issue2803StateWithFixedImport(t)
	if !d.hasFixedPatternsInOtherFiles(issue2803FileB) {
		t.Error("fix in another file should gate in")
	}
	if d.hasFixedPatternsInOtherFiles(issue2803FileA) {
		t.Error("fix in the same file should gate out (same-file exclusion)")
	}
	empty := newFixAmnesiaState()
	if empty.hasFixedPatternsInOtherFiles(issue2803FileB) {
		t.Error("no fixed patterns should gate out")
	}
}
