package agent

import (
	"strings"
	"testing"
)

func TestDocDriftAdvisory_FiresOnCodeHeavyRunWithoutDocs(t *testing.T) {
	files := []string{
		"internal/tool/browser.go",
		"internal/tool/command_gate.go",
		"internal/agent/agent.go",
	}
	msg := docDriftAdvisory(files)
	if msg == "" {
		t.Fatal("expected advisory for 3 Go files with zero docs")
	}
	for _, want := range []string{"doc-drift", "documentation-update", "dep_graph", "leaf-to-root"} {
		if !strings.Contains(msg, want) {
			t.Errorf("advisory should mention %q: %q", want, msg)
		}
	}
	if !strings.Contains(msg, "2 package(s)") {
		t.Errorf("advisory should count 2 packages: %q", msg)
	}
}

func TestDocDriftAdvisory_SilentWhenDocsTouched(t *testing.T) {
	files := []string{
		"internal/tool/browser.go",
		"internal/tool/command_gate.go",
		"internal/agent/agent.go",
		"docs/guide/tools.md",
	}
	if msg := docDriftAdvisory(files); msg != "" {
		t.Errorf("expected silence when a doc was touched, got %q", msg)
	}
}

func TestDocDriftAdvisory_SilentBelowThreshold(t *testing.T) {
	files := []string{"internal/tool/browser.go", "internal/tool/command_gate.go"}
	if msg := docDriftAdvisory(files); msg != "" {
		t.Errorf("expected silence below 3 Go files, got %q", msg)
	}
}

func TestDocDriftAdvisory_TestFilesExcluded(t *testing.T) {
	files := []string{
		"a.go",
		"b_test.go",
		"c_test.go",
		"d_test.go",
	}
	// Only a.go counts (tests excluded) -> below threshold -> silence.
	if msg := docDriftAdvisory(files); msg != "" {
		t.Errorf("test files must not count toward the threshold, got %q", msg)
	}
}

func TestDocDriftAdvisory_EmptyList(t *testing.T) {
	if msg := docDriftAdvisory(nil); msg != "" {
		t.Errorf("expected silence for empty edit list, got %q", msg)
	}
}

func TestDocDriftAdvisory_AbsoluteWindowsPaths(t *testing.T) {
	files := []string{
		`C:\work\ggcode\internal\tool\browser.go`,
		`C:\work\ggcode\internal\tool\gate.go`,
		`C:\work\ggcode\internal\agent\agent.go`,
	}
	msg := docDriftAdvisory(files)
	if msg == "" {
		t.Fatal("expected advisory for absolute windows-style paths")
	}
	if !strings.Contains(msg, "2 package(s)") {
		t.Errorf("advisory should count packages from windows paths: %q", msg)
	}
}

func TestCheckDocDriftGate_OneShotPerRun(t *testing.T) {
	a := &Agent{docDrift: newDocDriftState()}
	stats := &RunStats{FilesEdited: []string{
		"internal/tool/a.go", "internal/tool/b.go", "internal/agent/c.go",
	}}
	if msg := a.checkDocDriftGate(stats); msg == "" {
		t.Fatal("first call should fire")
	}
	if msg := a.checkDocDriftGate(stats); msg != "" {
		t.Errorf("second call must be silent (one shot per run), got %q", msg)
	}
}

func TestCheckDocDriftGate_NilStateSafe(t *testing.T) {
	a := &Agent{}
	if msg := a.checkDocDriftGate(&RunStats{FilesEdited: []string{"a.go", "b.go", "c.go"}}); msg != "" {
		t.Errorf("nil state must be safe, got %q", msg)
	}
}
