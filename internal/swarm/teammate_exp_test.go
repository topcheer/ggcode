package swarm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// r457: teammate experience ledger probes (write side).

func TestAppendTeammateExperience_Basics(t *testing.T) {
	dir := t.TempDir()
	appendTeammateExperience(dir, "team-1", "tm-1", "coder", "fixed the login bug")
	appendTeammateExperience(dir, "team-1", "tm-1", "coder", "fixed the signup bug")

	data, err := os.ReadFile(teammateExpPath(dir))
	if err != nil {
		t.Fatalf("ledger missing: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(lines))
	}
	var e teammateExperienceEntry
	if json.Unmarshal([]byte(lines[0]), &e) != nil {
		t.Fatal("malformed entry")
	}
	if e.TeamID != "team-1" || e.Digest != "fixed the login bug" {
		t.Fatalf("entry fields wrong: %+v", e)
	}
	if e.Teammate != "tm-1 (coder)" {
		t.Fatalf("teammate label should be id+name, got %q", e.Teammate)
	}
}

func TestAppendTeammateExperience_DigestTruncated(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("x", 900)
	appendTeammateExperience(dir, "t", "tm", "", long)
	data, _ := os.ReadFile(teammateExpPath(dir))
	var e teammateExperienceEntry
	if json.Unmarshal(data, &e) != nil {
		t.Fatal("unmarshal")
	}
	if len(e.Digest) > teammateExpDigestLen+10 {
		t.Fatalf("digest not truncated: %d", len(e.Digest))
	}
}

func TestAppendTeammateExperience_RollingBound(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < maxTeammateExpEntries+25; i++ {
		appendTeammateExperience(dir, "t", "tm", "", "task "+strings.Repeat("a", i%10))
	}
	data, _ := os.ReadFile(teammateExpPath(dir))
	got := len(splitJSONL(data))
	if got != maxTeammateExpEntries {
		t.Fatalf("rolling window broken: %d entries", got)
	}
	// No empty-dir write for empty input.
	appendTeammateExperience(dir, "", "tm", "", "x") // no workingDir -> no-op
}

func TestSplitJSONL_DropsPartialTrailingLine(t *testing.T) {
	got := splitJSONL([]byte("{\"a\":1}\n{\"b\":2}\n{\"c\":")) // last line partial
	if len(got) != 2 {
		t.Fatalf("partial trailing line must be dropped, got %d", len(got))
	}
}

func TestLedgerAtomicNoResidue(t *testing.T) {
	dir := t.TempDir()
	appendTeammateExperience(dir, "t", "tm", "", "hello")
	entries, _ := os.ReadDir(filepath.Join(dir, ".ggcode"))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("tmp residue: %s", e.Name())
		}
	}
	_ = time.Now // keep time import if probes evolve
}
