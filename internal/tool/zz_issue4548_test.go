package tool

import (
	"os"
	"strings"
	"testing"
)

func TestIssue4548_TruncationExemptsDeletionDetection(t *testing.T) {
	src, err := os.ReadFile("code_index_manager.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(src)

	scanStart := strings.Index(s, "func (m *CodeIndexManager) scanForExternalChanges(")
	if scanStart < 0 {
		t.Fatal("scanForExternalChanges not found")
	}
	scanEnd := strings.Index(s[scanStart:], "\nfunc ")
	if scanEnd < 0 {
		t.Fatal("scanForExternalChanges end not found")
	}
	body := s[scanStart : scanStart+scanEnd]

	if !strings.Contains(body, "walkTruncated = true") {
		t.Fatal("#4548: the file-cap SkipAll branch must set walkTruncated - otherwise the walk's early stop is indistinguishable from deletions beyond the cut")
	}

	walkIf := strings.Index(body, "if walkTruncated {")
	if walkIf < 0 {
		t.Fatal("#4548: deletion detection must be skipped when the walk was truncated (found set is partial; absence is not deletion)")
	}
	deletionMark := strings.Index(body, "m.dirtyFiles[p] = -1")
	if deletionMark < 0 || walkIf > deletionMark {
		t.Fatalf("#4548: truncation check must precede the -1 deletion marking")
	}
}
