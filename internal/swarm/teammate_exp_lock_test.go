package swarm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// #3260 probes: cross-writer lost-update + fixed-tmp collision are gone.

func readTeammateExpLines(t *testing.T, workingDir string) []teammateExperienceEntry {
	t.Helper()
	data, err := os.ReadFile(teammateExpPath(workingDir))
	if err != nil {
		t.Fatal(err)
	}
	var out []teammateExperienceEntry
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e teammateExperienceEntry
		if json.Unmarshal([]byte(line), &e) == nil {
			out = append(out, e)
		}
	}
	return out
}

func TestAppendTeammateExperience_ConcurrentNoLostUpdate(t *testing.T) {
	dir := t.TempDir()
	const writers = 8
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			appendTeammateExperience(dir, "team-x", fmt.Sprintf("tm-%d", n), "", fmt.Sprintf("result from writer %d", n))
		}(i)
	}
	wg.Wait()
	got := readTeammateExpLines(t, dir)
	if len(got) != writers {
		t.Fatalf("lost update: %d writers produced only %d entries", writers, len(got))
	}
	seen := map[string]bool{}
	for _, e := range got {
		seen[e.Teammate] = true
	}
	for i := 0; i < writers; i++ {
		if !seen[fmt.Sprintf("tm-%d", i)] {
			t.Fatalf("writer tm-%d's entry was silently erased: %+v", i, got)
		}
	}
}

func TestAppendTeammateExperience_SequentialAccumulates(t *testing.T) {
	dir := t.TempDir()
	appendTeammateExperience(dir, "t", "a", "alpha", "first result")
	appendTeammateExperience(dir, "t", "b", "", "second result")
	got := readTeammateExpLines(t, dir)
	if len(got) != 2 {
		t.Fatalf("expected 2 accumulated entries, got %d", len(got))
	}
	if got[0].Teammate != "a (alpha)" || got[1].Teammate != "b" {
		t.Fatalf("entries malformed: %+v", got)
	}
}

func TestAppendTeammateExperience_EmptyInputsNoop(t *testing.T) {
	dir := t.TempDir()
	appendTeammateExperience("", "t", "a", "", "x")       // no working dir
	appendTeammateExperience(dir, "t", "a", "", "   \t ") // whitespace-only result
	if _, err := os.Stat(teammateExpPath(dir)); !os.IsNotExist(err) {
		t.Fatal("empty inputs must not create the ledger")
	}
}

func TestAppendTeammateExperience_NoStrayTempFiles(t *testing.T) {
	dir := t.TempDir()
	appendTeammateExperience(dir, "t", "a", "", "result")
	entries, err := os.ReadDir(filepath.Join(dir, ".ggcode"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") && !strings.HasSuffix(e.Name(), ".lock") {
			t.Fatalf("stray temp file left behind: %s", e.Name())
		}
	}
}
