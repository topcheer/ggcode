package agent

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteGuidanceHintsRewriteSemantics(t *testing.T) {
	a := &Agent{}

	a.recordGuidanceHint("## Detector A", "first version", "2026-10-09T10:00:00Z")
	a.recordGuidanceHint("## Detector A", "second version", "2026-10-09T11:00:00Z")
	a.recordGuidanceHint("ACT NOW: verify", "verify before claiming done", "2026-10-09T11:30:00Z")

	dir := t.TempDir()
	a.writeGuidanceHints(dir)

	f, err := os.Open(filepath.Join(dir, ".ggcode", "memory", "guidance-hints.jsonl"))
	if err != nil {
		t.Fatalf("hints file missing: %v", err)
	}
	defer f.Close()

	recs := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var rec guidanceHintRec
		if json.Unmarshal(sc.Bytes(), &rec) == nil && rec.Tag != "" {
			recs[rec.Tag] = rec.Text
		}
	}
	if len(recs) != 2 {
		t.Fatalf("expected 2 distinct tags after dedup, got %d: %v", len(recs), recs)
	}
	if recs["## Detector A"] != "second version" {
		t.Errorf("expected most-recent text to win, got %q", recs["## Detector A"])
	}

	a2 := &Agent{}
	a2.recordGuidanceHint("ACT NOW: verify", "only tag", "2026-10-09T12:00:00Z")
	a2.writeGuidanceHints(dir)
	b, _ := os.ReadFile(filepath.Join(dir, ".ggcode", "memory", "guidance-hints.jsonl"))
	if n := strings.Count(string(b), "\n"); n != 1 {
		t.Errorf("rewrite must shrink file to 1 record, got %d lines", n)
	}
}

func TestWriteGuidanceHintsNoop(t *testing.T) {
	a := &Agent{}
	dir := t.TempDir()
	a.writeGuidanceHints(dir)
	if _, err := os.Stat(filepath.Join(dir, ".ggcode", "memory", "guidance-hints.jsonl")); !os.IsNotExist(err) {
		t.Error("no file expected when nothing was recorded")
	}
	a.recordGuidanceHint("", "text", "ts")
	a.recordGuidanceHint("tag", "", "ts")
	if len(a.guidanceHints) != 0 {
		t.Errorf("empty tag/text must be ignored, got %d entries", len(a.guidanceHints))
	}
}
