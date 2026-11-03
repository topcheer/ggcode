package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/memory"
)

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
		{[]string{"read_file"}, false},
		{[]string{"read_file", "edit_file"}, false},
		{[]string{}, false},
		{[]string{"a", "b", "c", "d", "e"}, false},
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

func TestToolflowConsolidationAdvice(t *testing.T) {

	hot := &memory.ToolFlowPattern{
		Prefix:     []string{"edit_file", "run_command"},
		Next:       "git_add",
		Count:      toolflowConsolidateMinSupport,
		Confidence: 0.9,
	}
	a := &Agent{}
	if got := a.toolflowConsolidationAdvice(hot); !strings.Contains(got, "cmd_snippet save") {
		t.Errorf("high-support run_command pattern must suggest cmd_snippet save, got:\n%s", got)
	}

	cool := &memory.ToolFlowPattern{
		Prefix:     []string{"edit_file", "run_command"},
		Next:       "git_add",
		Count:      toolflowConsolidateMinSupport - 1,
		Confidence: 0.9,
	}
	if got := a.toolflowConsolidationAdvice(cool); got != "" {
		t.Errorf("below-bar support must not advise consolidation, got:\n%s", got)
	}

	read := &memory.ToolFlowPattern{
		Prefix:     []string{"grep", "read_file"},
		Next:       "edit_file",
		Count:      500,
		Confidence: 0.95,
	}
	if got := a.toolflowConsolidationAdvice(read); got != "" {
		t.Errorf("read-only pattern must not advise consolidation, got:\n%s", got)
	}
}

func TestMaybeToolflowSuggestion_ConsolidationEndToEnd(t *testing.T) {
	dir := t.TempDir()
	seq := []string{"read_file", "run_command", "git_add"}
	var repeated []string
	for i := 0; i < toolflowConsolidateMinSupport+2; i++ {
		repeated = append(repeated, seq...)
	}
	buildSessionFile(t, dir, "s1.jsonl", repeated...)
	t.Setenv("HOME", strings.TrimSuffix(dir, "/.ggcode/sessions"))
	resetToolflowCacheForTest()
	defer resetToolflowCacheForTest()
	a := &Agent{}
	got := a.maybeToolflowSuggestion([]string{"grep", "read_file", "run_command"})
	if got == "" {
		t.Fatalf("expected a hint, got empty")
	}
	if !strings.Contains(got, "cmd_snippet save") {
		t.Errorf("run_command workflow at support %d must carry the consolidation advice, got:\n%s", toolflowConsolidateMinSupport+2, got)
	}
	if !strings.Contains(got, "not an instruction") {
		t.Errorf("consolidation advice must stay reference-level, got:\n%s", got)
	}
}
