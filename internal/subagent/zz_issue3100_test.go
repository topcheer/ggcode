package subagent

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func probeStore(t *testing.T) *TemplateStore {
	t.Helper()
	dir := t.TempDir()
	return &TemplateStore{dir: dir}
}

func TestIssue3100_SaveLeavesNoTmpArtifacts(t *testing.T) {
	s := probeStore(t)
	err := s.Save(NamedAgentTemplate{Name: "reviewer", SystemPrompt: "p"})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "reviewer.json" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("stray artifacts after save: %v", names)
	}
	got, err := s.Load("reviewer")
	if err != nil || got.SystemPrompt != "p" {
		t.Fatalf("round-trip: %+v err=%v", got, err)
	}
}

func TestIssue3100_SaveRebuildsCorruptFile(t *testing.T) {
	s := probeStore(t)
	path := filepath.Join(s.dir, "broken.json")
	if err := os.MkdirAll(s.dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"name":"bro`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(NamedAgentTemplate{Name: "broken", SystemPrompt: "rebuilt"}); err != nil {
		t.Fatalf("save over corrupt file: %v", err)
	}
	got, err := s.Load("broken")
	if err != nil || got.SystemPrompt != "rebuilt" {
		t.Fatalf("rebuild round-trip: %+v err=%v", got, err)
	}
}

func TestIssue3100_CollisionCheckStillEnforced(t *testing.T) {
	s := probeStore(t)
	if err := s.Save(NamedAgentTemplate{Name: "code reviewer"}); err != nil {
		t.Fatal(err)
	}
	err := s.Save(NamedAgentTemplate{Name: "code_reviewer"})
	if err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("collision check regressed: %v", err)
	}
}

func TestIssue3100_ConcurrentSaveNoTearing(t *testing.T) {
	s := probeStore(t)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				_ = s.Save(NamedAgentTemplate{
					Name:         "shared",
					SystemPrompt: strings.Repeat("x", 512),
					CreatedAt:    time.Unix(int64(w), 0),
				})
			}
		}(w)
	}
	wg.Wait()

	got, err := s.Load("shared")
	if err != nil || len(got.SystemPrompt) != 512 {
		t.Fatalf("torn final state: %+v err=%v", got, err)
	}
	entries, _ := os.ReadDir(s.dir)
	if len(entries) != 1 || entries[0].Name() != "shared.json" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("concurrent save leaked artifacts: %v", names)
	}
}

func TestIssue3100_DeleteVsSaveRace(t *testing.T) {
	s := probeStore(t)
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				_ = s.Save(NamedAgentTemplate{Name: "racy", SystemPrompt: "y"})
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				_ = s.Delete("racy")
			}
		}()
	}
	wg.Wait()
	if _, err := s.Load("racy"); err == nil {

		if got, lerr := s.Load("racy"); lerr != nil || got.SystemPrompt != "y" {
			t.Fatalf("torn survivor: %+v err=%v", got, lerr)
		}
	}
	entries, _ := os.ReadDir(s.dir)
	for _, e := range entries {
		if e.Name() != "racy.json" {
			t.Fatalf("race leaked artifact: %s", e.Name())
		}
	}
}

func TestIssue3100_ListSurvivesCorruptSibling(t *testing.T) {
	s := probeStore(t)
	if err := s.Save(NamedAgentTemplate{Name: "good"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, "bad.json"), []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}
	list, err := s.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Name != "good" {
		t.Fatalf("good template lost: %+v", list)
	}
}
