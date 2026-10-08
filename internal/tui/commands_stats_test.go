package tui

import (
	"os"
	"strings"
	"testing"
)

func TestGuidanceSlashCompletionRegistered(t *testing.T) {
	// r14 companion: /guidance must stay in the completion list and carry a
	// description (registration is split across commands.go/completion.go).
	found := false
	for _, c := range SlashCommands {
		if c == "/guidance" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("/guidance missing from SlashCommands completion list")
	}
	if !strings.Contains(SlashCommandDescriptions["/guidance"], "detector") {
		t.Fatal("/guidance missing a description")
	}
}

func TestSummarizeGuidanceStats(t *testing.T) {
	lines := []string{
		`{"ts":"2026-10-08T10:00:00Z","model":"m1","tag":"## Attention Fragmentation","delivered":3,"suppressed":1}`,
		`{"ts":"2026-10-08T11:00:00Z","model":"m1","tag":"## Attention Fragmentation","delivered":2,"suppressed":0}`,
		`{"ts":"2026-10-08T12:00:00Z","model":"m2","tag":"ACT NOW: verify","delivered":1,"suppressed":4}`,
		`not-json`,
		``,
	}
	out := summarizeGuidanceStats(lines)
	if out == "" {
		t.Fatal("expected non-empty summary")
	}
	// Aggregation: fragmentation 5 fire + 1 supp = 6 (rank 1), verify 1+4=5 (rank 2).
	if !strings.Contains(out, "2 tag(s)") {
		t.Errorf("expected 2 tags, got: %s", out)
	}
	if !strings.Contains(out, "5  1") && !strings.Contains(out, " 5     1") {
		t.Errorf("expected aggregated fire=5 supp=1 row, got: %s", out)
	}
	if !strings.Contains(out, "m1=6 m2=5") {
		t.Errorf("expected per-model totals m1=6 m2=5, got: %s", out)
	}
	if !strings.Contains(out, "window") {
		t.Errorf("expected time window in header, got: %s", out)
	}
	// Sorted by total descending: fragmentation (6) before verify (5).
	fi := strings.Index(out, "Attention")
	vi := strings.Index(out, "ACT NOW")
	if fi < 0 || vi < 0 || fi > vi {
		t.Errorf("expected descending sort by total, got: %s", out)
	}
}

func TestSummarizeGuidanceStatsEmpty(t *testing.T) {
	if out := summarizeGuidanceStats([]string{"garbage"}); out != "" {
		t.Errorf("expected empty summary for unparseable input, got %q", out)
	}
}

func TestReadTailLinesCapsAndSkipsBlanks(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/in.jsonl"
	var b strings.Builder
	for i := 0; i < 5; i++ {
		b.WriteString("row\n\n") // blank lines interleaved
	}
	if err := writeFileForTest(path, b.String()); err != nil {
		t.Fatal(err)
	}
	got, err := readTailLines(path, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("expected tail cap of 3 non-blank lines, got %d", len(got))
	}
}

func writeFileForTest(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}
