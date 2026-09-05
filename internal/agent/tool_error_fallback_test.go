package agent

import (
	"strings"
	"testing"
)

func TestToolFallbackState_Reset(t *testing.T) {
	s := newToolFallbackState()
	s.fired["grep"] = true
	s.fired["lsp_definition"] = true

	s.reset()

	if len(s.fired) != 0 {
		t.Fatalf("expected fired map to be empty after reset, got %d entries", len(s.fired))
	}
}

func TestToolFallbackState_GrepNoMatches(t *testing.T) {
	s := newToolFallbackState()
	hint := s.maybeFallbackSuggestion("grep", "no matches found")
	if hint == "" {
		t.Fatal("expected fallback for grep no matches")
	}
	if !strings.Contains(hint, "search_files") {
		t.Errorf("expected grep fallback to mention search_files, got: %s", hint)
	}
	if !strings.Contains(hint, "code_search") {
		t.Errorf("expected grep fallback to mention code_search, got: %s", hint)
	}
}

func TestToolFallbackState_GrepInvalidRegex(t *testing.T) {
	s := newToolFallbackState()
	hint := s.maybeFallbackSuggestion("grep", "error: invalid regex pattern")
	if hint == "" {
		t.Fatal("expected fallback for grep invalid regex")
	}
	if !strings.Contains(hint, "escape") {
		t.Errorf("expected grep regex fallback to mention escaping, got: %s", hint)
	}
}

func TestToolFallbackState_LspDefinitionNotFound(t *testing.T) {
	s := newToolFallbackState()
	hint := s.maybeFallbackSuggestion("lsp_definition", "no definition found")
	if hint == "" {
		t.Fatal("expected fallback for lsp_definition not found")
	}
	if !strings.Contains(hint, "lsp_workspace_symbols") {
		t.Errorf("expected lsp_definition fallback to mention lsp_workspace_symbols, got: %s", hint)
	}
}

func TestToolFallbackState_LspNotReady(t *testing.T) {
	s := newToolFallbackState()
	hint := s.maybeFallbackSuggestion("lsp_definition", "language server not ready, indexing")
	if hint == "" {
		t.Fatal("expected fallback for lsp_definition server not ready")
	}
	if !strings.Contains(hint, "grep") {
		t.Errorf("expected lsp not-ready fallback to mention grep, got: %s", hint)
	}
}

func TestToolFallbackState_WebFetchTimeout(t *testing.T) {
	s := newToolFallbackState()
	hint := s.maybeFallbackSuggestion("web_fetch", "request timed out")
	if hint == "" {
		t.Fatal("expected fallback for web_fetch timeout")
	}
	if !strings.Contains(hint, "browser") {
		t.Errorf("expected web_fetch timeout fallback to mention browser, got: %s", hint)
	}
}

func TestToolFallbackState_WebFetch403(t *testing.T) {
	s := newToolFallbackState()
	hint := s.maybeFallbackSuggestion("web_fetch", "403 forbidden")
	if hint == "" {
		t.Fatal("expected fallback for web_fetch 403")
	}
	if !strings.Contains(hint, "browser") {
		t.Errorf("expected web_fetch 403 fallback to mention browser, got: %s", hint)
	}
}

func TestToolFallbackState_EditFileNotUnique(t *testing.T) {
	s := newToolFallbackState()
	hint := s.maybeFallbackSuggestion("edit_file", "old_text is not unique in the file")
	if hint == "" {
		t.Fatal("expected fallback for edit_file not unique")
	}
	if !strings.Contains(hint, "anchor") {
		t.Errorf("expected edit_file fallback to mention anchoring, got: %s", hint)
	}
}

func TestToolFallbackState_EditFileNotFound(t *testing.T) {
	s := newToolFallbackState()
	hint := s.maybeFallbackSuggestion("edit_file", "old_text does not match any content")
	if hint == "" {
		t.Fatal("expected fallback for edit_file not found")
	}
	if !strings.Contains(hint, "re-read") || !strings.Contains(hint, "read_file") {
		t.Errorf("expected edit_file not-found fallback to mention re-read/read_file, got: %s", hint)
	}
}

func TestToolFallbackState_ReadFileNotFound(t *testing.T) {
	s := newToolFallbackState()
	hint := s.maybeFallbackSuggestion("read_file", "no such file or directory")
	if hint == "" {
		t.Fatal("expected fallback for read_file not found")
	}
	if !strings.Contains(hint, "glob") {
		t.Errorf("expected read_file fallback to mention glob, got: %s", hint)
	}
}

func TestToolFallbackState_CodeSearchNoResults(t *testing.T) {
	s := newToolFallbackState()
	hint := s.maybeFallbackSuggestion("code_search", "no relevant files found")
	if hint == "" {
		t.Fatal("expected fallback for code_search no results")
	}
	if !strings.Contains(hint, "grep") {
		t.Errorf("expected code_search fallback to mention grep, got: %s", hint)
	}
}

func TestToolFallbackState_CodeSearchIndexNotReady(t *testing.T) {
	s := newToolFallbackState()
	hint := s.maybeFallbackSuggestion("code_search", "index is not ready, still building")
	if hint == "" {
		t.Fatal("expected fallback for code_search index not ready")
	}
	if !strings.Contains(hint, "grep") {
		t.Errorf("expected code_search index fallback to mention grep, got: %s", hint)
	}
}

func TestToolFallbackState_DefaultForUnknownTool(t *testing.T) {
	s := newToolFallbackState()
	hint := s.maybeFallbackSuggestion("some_unknown_tool", "error happened")
	if hint == "" {
		t.Fatal("expected default fallback for unknown tool")
	}
	if !strings.Contains(hint, "alternative") {
		t.Errorf("expected default fallback to mention alternative, got: %s", hint)
	}
}

func TestToolFallbackState_FiresOncePerTool(t *testing.T) {
	s := newToolFallbackState()

	// First failure should produce a suggestion.
	hint1 := s.maybeFallbackSuggestion("grep", "no matches found")
	if hint1 == "" {
		t.Fatal("expected first grep failure to produce fallback")
	}

	// Second failure for the same tool should NOT produce a suggestion.
	hint2 := s.maybeFallbackSuggestion("grep", "no matches found again")
	if hint2 != "" {
		t.Fatalf("expected no fallback on second grep failure, got: %s", hint2)
	}

	// A different tool should still get a suggestion.
	hint3 := s.maybeFallbackSuggestion("lsp_definition", "no definition found")
	if hint3 == "" {
		t.Fatal("expected first lsp_definition failure to produce fallback")
	}
}

func TestToolFallbackState_EmptyErrorContent(t *testing.T) {
	s := newToolFallbackState()
	hint := s.maybeFallbackSuggestion("grep", "")
	if hint != "" {
		t.Fatalf("expected no fallback for empty error content, got: %s", hint)
	}
}

func TestToolFallbackState_RunCommandNotFound(t *testing.T) {
	s := newToolFallbackState()
	hint := s.maybeFallbackSuggestion("run_command", "command not found: foobar")
	if hint == "" {
		t.Fatal("expected fallback for run_command not found")
	}
	if !strings.Contains(hint, "spelling") || !strings.Contains(hint, "which") {
		t.Errorf("expected run_command fallback to mention spelling/which, got: %s", hint)
	}
}

func TestToolFallbackState_GlobNoMatches(t *testing.T) {
	s := newToolFallbackState()
	hint := s.maybeFallbackSuggestion("glob", "no files matching pattern")
	if hint == "" {
		t.Fatal("expected fallback for glob no matches")
	}
	if !strings.Contains(hint, "list_directory") {
		t.Errorf("expected glob fallback to mention list_directory, got: %s", hint)
	}
}

// --- Agent integration tests ---

func TestAgentToolFallbackCheck(t *testing.T) {
	a := &Agent{toolFallback: newToolFallbackState()}

	hint := a.toolFallbackCheck("grep", "no matches found")
	if hint == "" {
		t.Fatal("expected fallback hint from agent method")
	}

	// Second call for same tool should not fire.
	hint2 := a.toolFallbackCheck("grep", "no matches found again")
	if hint2 != "" {
		t.Fatalf("expected no second fallback, got: %s", hint2)
	}
}

func TestAgentToolFallbackCheckNilState(t *testing.T) {
	a := &Agent{toolFallback: nil}
	hint := a.toolFallbackCheck("grep", "error")
	if hint != "" {
		t.Fatalf("expected no hint with nil state, got: %s", hint)
	}
}

func TestAgentResetToolFallback(t *testing.T) {
	a := &Agent{toolFallback: newToolFallbackState()}
	a.toolFallback.fired["grep"] = true

	a.resetToolFallback()

	if len(a.toolFallback.fired) != 0 {
		t.Fatalf("expected fired map to be empty after reset")
	}
}

// Regression for #1508: fired was marked BEFORE rule evaluation, so an
// error matching no rule still burned the tool's single per-run slot.
// After the fix, fired is set only when a suggestion is actually returned.
func TestMaybeFallbackSuggestionFiresOnlyOnMatch(t *testing.T) {
	tf := newToolFallbackState()
	if tf.fired["grep"] {
		t.Fatal("fired must start unset")
	}
	// "no match" is grep's first rule - must fire AND mark fired.
	if s := tf.maybeFallbackSuggestion("grep", "grep: no match in file"); s == "" {
		t.Fatal("expected a fallback suggestion for the no-match rule")
	}
	if !tf.fired["grep"] {
		t.Fatal("fired must be set after an actual suggestion")
	}
}
