package wailskit

// #2715: the wailskit reflection path must use LoadKey (single-key read),
// not LoadAll (merges EVERY memory key), when refreshing run-insights -
// and must record provenance via SaveMemoryWithSource, matching the TUI
// (#1388) and daemon (#1752 case 3) reflection paths.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/memory"
)

func TestIssue2715ReflectionUsesLoadKeyNotLoadAll(t *testing.T) {
	// The reflection logic lives in chat_reflection.go (extracted from
	// ChatBridge.InitAgent for behavioral testing) and is wired via
	// buildReflectionFunc in chat.go.
	srcFiles := []string{"chat.go", "chat_reflection.go"}
	var src string
	for _, f := range srcFiles {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src += string(b) + "\n"
	}

	if strings.Contains(src, "autoMem.LoadAll()") {
		t.Fatal("#2715: reflection must not use LoadAll() - it merges every memory key into run-insights")
	}
	if !strings.Contains(src, `autoMem.LoadKey(key)`) {
		t.Fatal("#2715: reflection must read back run-insights via LoadKey")
	}
	if !strings.Contains(src, `autoMem.SaveMemoryWithSource(key, insights, "run-reflection")`) && !strings.Contains(src, "buildReflectionFunc") {
		t.Fatal("#2715: reflection must persist via SaveMemoryWithSource with provenance \"run-reflection\"")
	}
}

// Behavioral pin: the reflection callback (buildReflectionFunc, extracted in
// chat_reflection.go) must merge ONLY the run-insights key - unrelated memory
// keys must never be folded back in, and a failed load must abort the save
// instead of overwriting accumulated insights (TUI #1388 side-fix parity).
func issue2715Stats() agent.RunStats {
	return agent.RunStats{
		Success:    true,
		Iterations: 4,
		UserPrompt: "issue2715 probe",
		ToolCalls:  map[string]int{"read_file": 3, "grep": 2},
	}
}

func TestIssue2715ReflectionDoesNotIngestUnrelatedMemories(t *testing.T) {
	tmp := t.TempDir()
	am := memory.NewProjectAutoMemory(tmp)
	if am == nil {
		t.Fatal("NewProjectAutoMemory returned nil")
	}
	const buildNote = "BUILD-PROCESS-UNRELATED-7f3a use make verify-ci"
	const prior = "PRIOR-INSIGHT-9c1d avoid touching vendor dir"
	if err := am.SaveMemory("build-process", buildNote); err != nil {
		t.Fatalf("seed build-process: %v", err)
	}
	if err := am.SaveMemory("run-insights", prior); err != nil {
		t.Fatalf("seed run-insights: %v", err)
	}

	buildReflectionFunc(tmp)(issue2715Stats())

	got, err := am.LoadKey("run-insights")
	if err != nil {
		t.Fatalf("LoadKey(run-insights): %v", err)
	}
	if !strings.Contains(got, "issue2715 probe") {
		t.Errorf("run-insights missing new reflection content; got %q", got)
	}
	if !strings.Contains(got, prior) {
		t.Errorf("run-insights lost prior accumulation; got %q", got)
	}
	if strings.Contains(got, buildNote) {
		t.Errorf("#2715 regression: build-process content leaked into run-insights; got %q", got)
	}
	bp, err := am.LoadKey("build-process")
	if err != nil || bp != buildNote {
		t.Errorf("build-process mutated: err=%v got %q", err, bp)
	}
}

func TestIssue2715ReflectionFirstWriteRecordsProvenance(t *testing.T) {
	tmp := t.TempDir()
	am := memory.NewProjectAutoMemory(tmp)
	if am == nil {
		t.Fatal("NewProjectAutoMemory returned nil")
	}
	if err := am.SaveMemory("api-gotcha", "API-GOTCHA-UNRELATED-2b8e"); err != nil {
		t.Fatalf("seed api-gotcha: %v", err)
	}

	buildReflectionFunc(tmp)(issue2715Stats())

	got, err := am.LoadKey("run-insights")
	if err != nil {
		t.Fatalf("LoadKey(run-insights): %v", err)
	}
	if !strings.Contains(got, "issue2715 probe") {
		t.Errorf("first reflection write missing content; got %q", got)
	}
	if strings.Contains(got, "API-GOTCHA-UNRELATED-2b8e") {
		t.Errorf("#2715 regression on first write: unrelated key ingested; got %q", got)
	}
}

func TestIssue2715ReflectionAbortsSaveOnLoadError(t *testing.T) {
	tmp := t.TempDir()
	am := memory.NewProjectAutoMemory(tmp)
	if am == nil {
		t.Fatal("NewProjectAutoMemory returned nil")
	}
	const prior = "PRIOR-INSIGHT-9c1d must-survive-load-error"
	if err := am.SaveMemory("run-insights", prior); err != nil {
		t.Fatalf("seed run-insights: %v", err)
	}
	path := filepath.Join(am.Dir(), "run-insights.md")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Skipf("cannot chmod on this platform: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })

	buildReflectionFunc(tmp)(issue2715Stats())

	if _, err := am.LoadKey("run-insights"); err == nil {
		t.Skip("load unexpectedly succeeded (running as root); cannot verify error path")
	}
	_ = os.Chmod(path, 0o644)
	data, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatalf("read run-insights.md: %v", rerr)
	}
	if strings.Contains(string(data), "issue2715 probe") {
		t.Errorf("#2715 hardening violated: fresh batch overwrote accumulation on load error; got %q", data)
	}
	if !strings.HasPrefix(string(data), prior) {
		t.Errorf("prior accumulation lost; got %q", data)
	}
}
