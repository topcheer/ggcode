package agent

// r484 probes: harness-side toolflow next-step hint.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// buildSessionFile writes a fake session JSONL under <dir>/.ggcode/sessions/
// (where AnalyzeToolFlows scans): each tool call is one line carrying a
// "tool_name":"x" field (the format ExtractToolSequence mines).
func buildSessionFile(t *testing.T, dir, name string, tools ...string) {
	t.Helper()
	sessions := filepath.Join(dir, ".ggcode", "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	for _, tool := range tools {
		sb.WriteString(`{"ts":"2026-01-01T00:00:00Z","tool_name":"` + tool + `"}` + "\n")
	}
	if err := os.WriteFile(filepath.Join(sessions, name), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestToolflowPrefixMatchesSuffix(t *testing.T) {
	seq := []string{"read_file", "edit_file", "run_command", "git_add"}
	cases := []struct {
		prefix []string
		want   bool
	}{
		{[]string{"run_command", "git_add"}, true},
		{[]string{"git_add"}, true},
		{[]string{"edit_file", "run_command", "git_add"}, true},
		{[]string{"read_file"}, false}, // present but not a suffix
		{[]string{"read_file", "edit_file"}, false},
		{[]string{}, false},
		{[]string{"a", "b", "c", "d", "e"}, false}, // longer than seq
	}
	for _, c := range cases {
		if got := toolflowPrefixMatchesSuffix(c.prefix, seq); got != c.want {
			t.Errorf("prefix %v on seq %v = %v, want %v", c.prefix, seq, got, c.want)
		}
	}
}

func TestMaybeToolflowSuggestion_OneShotGate(t *testing.T) {
	a := &Agent{toolflowHintFired: true}
	if got := a.maybeToolflowSuggestion([]string{"read_file", "edit_file"}); got != "" {
		t.Errorf("fired gate must suppress, got %q", got)
	}
	// Short history (below min calls) also returns empty without firing.
	b := &Agent{}
	if got := b.maybeToolflowSuggestion([]string{"read_file"}); got != "" {
		t.Errorf("short history must be empty, got %q", got)
	}
	if b.toolflowHintFired {
		t.Errorf("short history must not fire the gate")
	}
}

func resetToolflowCacheForTest() {
	toolflowCacheMu.Lock()
	defer toolflowCacheMu.Unlock()
	toolflowCacheReady = false
	toolflowCacheAt = time.Time{}
	toolflowCachePats = nil
}

func TestMaybeToolflowSuggestion_ColdStore(t *testing.T) {
	// HOME pointed at a dir with no .ggcode/sessions: mining returns no
	// patterns; the call must return "" without firing. The store is
	// re-mined at most once per TTL window per process (#3436) - a later
	// run may pick sessions up after the window expires.
	resetToolflowCacheForTest()
	defer resetToolflowCacheForTest()
	t.Setenv("HOME", t.TempDir())
	a := &Agent{}
	if got := a.maybeToolflowSuggestion([]string{"read_file", "edit_file", "run_command"}); got != "" {
		t.Errorf("cold store must yield empty, got %q", got)
	}
	if a.toolflowHintFired {
		t.Errorf("cold store must not fire the gate")
	}
}

func TestMaybeToolflowSuggestion_MatchAndWording(t *testing.T) {
	// Build a fake sessions dir with one session whose tool sequence repeats
	// read_file -> edit_file -> run_command six times: prefix [read_file,
	// edit_file] -> run_command reaches support 6, confidence 1.0.
	dir := t.TempDir()
	buildSessionFile(t, dir, "s1.jsonl", "read_file", "edit_file", "run_command",
		"read_file", "edit_file", "run_command",
		"read_file", "edit_file", "run_command",
		"read_file", "edit_file", "run_command",
		"read_file", "edit_file", "run_command",
		"read_file", "edit_file", "run_command")
	t.Setenv("HOME", strings.TrimSuffix(dir, "/.ggcode/sessions"))
	resetToolflowCacheForTest()
	defer resetToolflowCacheForTest()
	a := &Agent{}
	got := a.maybeToolflowSuggestion([]string{"grep", "read_file", "edit_file"})
	if got == "" {
		t.Fatalf("expected a hint for matching suffix, got empty")
	}
	if !strings.Contains(got, "run_command") {
		t.Errorf("hint must name the dominant next step, got:\n%s", got)
	}
	if !strings.Contains(got, "reference only") || !strings.Contains(got, "only if it fits the current task") {
		t.Errorf("hint must be reference-level (r483 discipline), got:\n%s", got)
	}
	if !a.toolflowHintFired {
		t.Errorf("successful hint must fire the one-shot gate")
	}
	if again := a.maybeToolflowSuggestion([]string{"grep", "read_file", "edit_file"}); again != "" {
		t.Errorf("second call in same run must be suppressed, got:\n%s", again)
	}
}
