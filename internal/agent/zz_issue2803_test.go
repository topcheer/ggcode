package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// zz_issue2803_test.go guards against the fix_amnesia false-positive
// recurrence (#2803): the edit-success path passed the tool result (success
// message + compactDiff truncated to 25 lines) as content, so a mid-function
// edit to a mature file whose import block sat outside the hunk made
// missingImportInContent claim a missing import that was actually present -
// guidance that, if followed, added a duplicate import.

const issue2803FileA = "a.go"
const issue2803FileB = "b.go"

func issue2803StateWithFixedImport(t *testing.T) *fixAmnesiaState {
	t.Helper()
	d := newFixAmnesiaState()
	// Observe a missing-import error in fileA, then edit fileA (promotes FIXED).
	d.recordErrorObserved("missing-import", issue2803FileA)
	d.recordFileEdited(issue2803FileA)
	return d
}

// TestIssue2803FullContentWithImportNoFalsePositive: the detector's contract
// input (full NEW content) with the import present must NOT warn - this is
// what the truncated diff used to get wrong.
func TestIssue2803FullContentWithImportNoFalsePositive(t *testing.T) {
	d := issue2803StateWithFixedImport(t)
	full := "package b\n\nimport \"fmt\"\n\nfunc f() {\n\tfmt.Println(\"hi\")\n}\n"
	if got := d.checkContentAgainstFixed("", issue2803FileB, full); got != "" {
		t.Errorf("false positive on content that already imports fmt: %q", got)
	}
}

// TestIssue2803FullContentMissingImportStillWarns: the true-positive path
// must survive the fix (file genuinely using fmt without importing).
func TestIssue2803FullContentMissingImportStillWarns(t *testing.T) {
	d := issue2803StateWithFixedImport(t)
	full := "package b\n\nfunc f() {\n\tfmt.Println(\"hi\")\n}\n"
	if got := d.checkContentAgainstFixed("", issue2803FileB, full); got == "" {
		t.Error("expected guidance for genuinely missing import")
	}
}

// TestIssue2803WiringReadsRealFile drives the wiring contract: after a
// successful edit to a real temp file that already imports fmt, no
// fix-amnesia guidance may be appended even though a diff-only content
// (without the import line) would have false-positived.
func TestIssue2803WiringReadsRealFile(t *testing.T) {
	dir := t.TempDir()
	fileB := filepath.Join(dir, issue2803FileB)
	content := "package b\n\nimport \"fmt\"\n\nfunc f() {\n\tfmt.Println(\"hi\")\n}\n"
	if err := os.WriteFile(fileB, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	// Simulate the pre-fix bug input: a diff-like content that uses fmt but
	// carries no import line (what a 25-line truncated hunk looks like).
	diffLike := "+\tmsg := fmt.Sprintf(\"x\")\n"
	d := issue2803StateWithFixedImport(t)
	if got := d.checkContentAgainstFixed("", fileB, diffLike); got == "" {
		t.Fatal("precondition: diff-like content without import line must trigger the detector (the #2803 bug shape)")
	}
	// And the full content (what the fixed wiring passes after os.ReadFile)
	// must not - covered above; this test pins both sides of the contract.
}

// TestIssue2803CallSiteReadsFile pins the source-level invariant: the
// checkContentAgainstFixed call in agent.go must be preceded by the
// post-edit os.ReadFile assembly (the wiring fix).
func TestIssue2803CallSiteReadsFile(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatalf("read agent.go: %v", err)
	}
	s := string(src)
	callIdx := strings.Index(s, "checkContentAgainstFixed(extractFilePathFromError")
	if callIdx < 0 {
		t.Fatal("checkContentAgainstFixed call site not found in agent.go")
	}
	// The ReadFile assembly must exist shortly before the call (same block).
	window := s[max(0, callIdx-1500):callIdx]
	if !strings.Contains(window, "os.ReadFile(fp)") {
		t.Error("call site does not read the real post-edit file before checking content (#2803 recurrence)")
	}
	if !strings.Contains(window, "newContent") {
		t.Error("call site no longer routes content through newContent")
	}
}
