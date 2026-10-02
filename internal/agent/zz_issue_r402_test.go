package agent

// r402 probes: detector-guidance observability foundation.
// 1. guidanceTag clusters messages by stable first line (markdown stripped).
// 2. injectGuidance records delivered/suppressed per tag without
//    double-counting.
// 3. flushGuidanceStats appends JSONL lines on any exit and is a silent
//    no-op when nothing fired or the working dir is unknown.

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuidanceTagClustersByFirstLine(t *testing.T) {
	got := guidanceTag("## Attention Fragmentation Detected\nbody details here")
	if got != "Attention Fragmentation Detected" {
		t.Fatalf("markdown header not stripped: %q", got)
	}
	long := strings.Repeat("字", 100)
	tag := guidanceTag(long + "\nsecond line")
	if want := strings.Repeat("字", guidanceTagMaxRunes); tag != want {
		t.Fatalf("rune cap: got %d runes, want %d", len([]rune(tag)), guidanceTagMaxRunes)
	}
	if guidanceTag("") != "" {
		t.Fatalf("empty text should tag empty")
	}
}

func TestGuidanceStatsRecordNoDoubleCount(t *testing.T) {
	st := guidanceRunStats{}
	st.record("a", true)
	st.record("a", true)
	st.record("a", false)
	got := st["a"]
	if got.Delivered != 2 || got.Suppressed != 1 {
		t.Fatalf("want delivered=2 suppressed=1, got %+v", got)
	}
}

func TestFlushGuidanceStatsAppendsJSONL(t *testing.T) {
	dir := t.TempDir()
	a := &Agent{guidanceStats: guidanceRunStats{}}
	a.SetWorkingDir(dir)
	a.guidanceStats.record("Detector A", true)
	a.guidanceStats.record("Detector A", true)
	a.guidanceStats.record("Detector A", false)
	a.flushGuidanceStats()

	f, err := os.Open(filepath.Join(dir, ".ggcode/memory/guidance-stats.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			lines = append(lines, sc.Text())
		}
	}
	if len(lines) != 1 {
		t.Fatalf("want 1 JSONL line per tag, got %d", len(lines))
	}
	for _, want := range []string{`"tag":"Detector A"`, `"delivered":2`, `"suppressed":1`} {
		if !strings.Contains(lines[0], want) {
			t.Fatalf("line %q missing %s", lines[0], want)
		}
	}

	// Second flush with a fresh stat must APPEND, not truncate (multi-run
	// accumulation is the whole point).
	a.guidanceStats = guidanceRunStats{}
	a.guidanceStats.record("Detector B", true)
	a.flushGuidanceStats()
	data, _ := os.ReadFile(filepath.Join(dir, ".ggcode/memory/guidance-stats.jsonl"))
	if n := strings.Count(string(data), "\n"); n != 2 {
		t.Fatalf("append semantics: want 2 lines total, got %d", n)
	}
}

func TestFlushGuidanceStatsNoopWhenEmptyOrNoDir(t *testing.T) {
	dir := t.TempDir()
	a := &Agent{guidanceStats: guidanceRunStats{}}
	a.flushGuidanceStats() // empty stats: no panic, no file
	if _, err := os.Stat(filepath.Join(dir, ".ggcode")); !os.IsNotExist(err) {
		t.Fatalf("empty stats must not create anything, err=%v", err)
	}
	a2 := &Agent{guidanceStats: guidanceRunStats{}}
	a2.guidanceStats.record("x", true)
	a2.flushGuidanceStats() // no working dir: silent skip, no panic
}
