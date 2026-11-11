package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// P1 prompt-slimming (#5446): the run-start experience injection must be a
// one-line-per-case POINTER (discovery seed), not the full 3-line
// Task/Files/Approach body - full detail stays behind recall_experience.
func TestFormatIndexPointerTruncatedShape(t *testing.T) {
	dir := t.TempDir()
	es := NewExperienceStore(dir)
	es.Record("fix flaky stdio concurrent reads test in the mcp client", "rerun-verify then quarantine pattern; full detail in the case file", "success", nil)

	idx, ids := es.FormatIndexPointerTruncated("flaky stdio reads test", 3)
	if idx == "" || len(ids) == 0 {
		t.Fatalf("pointer index must return content + tracked ids, got %q / %v", idx, ids)
	}
	if !strings.Contains(idx, "recall_experience") {
		t.Fatalf("pointer must instruct recall_experience for full detail:\n%s", idx)
	}
	for _, forbidden := range []string{"Approach:", "Files touched:"} {
		if strings.Contains(idx, forbidden) {
			t.Fatalf("pointer must not carry the full body (%q found):\n%s", forbidden, idx)
		}
	}
	lines := strings.Split(strings.TrimSpace(idx), "\n")
	if len(lines) != 2 { // header + exactly one case line
		t.Fatalf("expected header + 1 case line, got %d lines:\n%s", len(lines), idx)
	}
	if !strings.Contains(lines[1], "[success]") || !strings.Contains(lines[1], "flaky stdio") {
		t.Fatalf("case line must carry outcome + task gist:\n%s", lines[1])
	}
}

// Pointer and full renderer stay equivalent on ids so run-stats tracking
// (experienceInjectedCaseIDs) is unaffected by the switch.
func TestFormatIndexPointerIdsMatchFull(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "cases"), 0o755); err != nil {
		t.Fatal(err)
	}
	es := NewExperienceStore(dir)
	es.Record("alpha release tagging flow notes", "", "success", nil)
	es.Record("beta rollback drill notes", "", "partial", nil)

	_, idsPtr := es.FormatIndexPointerTruncated("release notes rollback notes", 3)
	_, idsFull := es.FormatIndexTracked("release notes rollback notes", 3)
	if len(idsPtr) == 0 || len(idsPtr) != len(idsFull) {
		t.Fatalf("tracked ids must match between pointer and full renderers: %v vs %v", idsPtr, idsFull)
	}
}
