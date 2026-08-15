package agent

// Empirical repro tests for wasted-explore path normalization bug.
// Formats below are copied verbatim from actual tool implementations:
//   - grep (rg path, default path="."):      "./internal/agent/foo.go"   (verified: rg 15.1.0 emits ./ prefix)
//   - grep (Go fallback):                      "internal/agent/foo.go"    (grep.go formatFilesWithMatches → filepath.Rel)
//   - lsp_references/lsp_workspace_symbols:    "/abs/path/foo.go:12:8"   (lsp.go:574 → uriToPath → filepath.Clean → absolute)
//   - code_search:                             "1. path (relevance: 87%)" (code_search.go:289)
//   - read_file args: what the LLM passes (schema asks absolute; relative also works via os.ReadFile)
import (
	"encoding/json"
	"testing"
)

func wexpArgs(t *testing.T, m map[string]interface{}) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Scenario A (the primary false positive): grep with default path (rg emits
// "./" prefix), then the agent DOES read the file with an absolute path.
// Expect: warning is STILL emitted even though the file was consumed.
func TestWastedExploreRepro_GrepDotPrefixVsAbsolutePath(t *testing.T) {
	s := newWastedExploreState()
	// iteration 1: grep returns rg-style output with ./ prefix (path arg omitted → ".")
	s.recordSearchToolCall("grep", wexpArgs(t, map[string]interface{}{"pattern": "wastedExploreState"}),
		"./internal/agent/wasted_explore.go\n1 file(s) matched", 1)
	if len(s.pendingSearches) != 1 {
		t.Fatalf("expected 1 pending search, got %d", len(s.pendingSearches))
	}
	// iteration 2: agent reads the very same file with an absolute path
	s.recordConsumptionToolCall("read_file",
		wexpArgs(t, map[string]interface{}{"path": "/Volumes/new/ggai/ggcode/internal/agent/wasted_explore.go"}),
		"", 2)
	for _, info := range s.pendingSearches {
		if !info.Consumed {
			t.Errorf("#482 regression: ./-prefixed rg output must match the absolute read path after normalization")
		}
	}
	// iteration 3: window elapsed → NO warning (the read consumed it)
	msg := s.checkWastedSearches(3)
	if msg != "" {
		t.Fatalf("#482 regression: false-positive warning despite the read: %q", msg)
	}
}

// Scenario B: rg path (./-prefix) vs Go-fallback path (no prefix) also mismatch.
func TestWastedExploreRepro_GrepRgVsGoFallbackFormatMismatch(t *testing.T) {
	s := newWastedExploreState()
	s.recordSearchToolCall("grep", wexpArgs(t, map[string]interface{}{"pattern": "x"}),
		"./internal/tool/grep.go\n1 file(s) matched", 1)
	// consumption via a second grep that hit the Go fallback (rel path, no ./)
	s.recordConsumptionToolCall("grep", wexpArgs(t, map[string]interface{}{"pattern": "y", "path": "."}),
		"internal/tool/grep.go\n1 file(s) matched", 2)
	for _, info := range s.pendingSearches {
		if !info.Consumed {
			t.Errorf("#482 regression: rg ./-prefix must match Go-fallback rel path after normalization")
		}
	}
}

// Scenario C (control): both sides absolute → matching DOES work, proving the
// bug is specifically the missing normalization, not a broken detector.
func TestWastedExploreRepro_Control_AbsoluteBothSides(t *testing.T) {
	s := newWastedExploreState()
	s.recordSearchToolCall("grep", wexpArgs(t, map[string]interface{}{"pattern": "x", "path": "/Volumes/new/ggai/ggcode"}),
		"/Volumes/new/ggai/ggcode/internal/agent/wasted_explore.go\n1 file(s) matched", 1)
	s.recordConsumptionToolCall("read_file",
		wexpArgs(t, map[string]interface{}{"path": "/Volumes/new/ggai/ggcode/internal/agent/wasted_explore.go"}),
		"", 2)
	for _, info := range s.pendingSearches {
		if !info.Consumed {
			t.Errorf("control failed: identical absolute paths should match")
		}
	}
	if msg := s.checkWastedSearches(3); msg != "" {
		t.Errorf("control: no warning expected, got %q", wexpFirstLine(msg))
	}
}

// Scenario D: lsp_* search results are absolute (uriToPath→filepath.Clean);
// if the agent then reads with a RELATIVE path (works fine via os.ReadFile),
// no match → false positive.
func TestWastedExploreRepro_LspAbsoluteVsRelativeRead(t *testing.T) {
	s := newWastedExploreState()
	// lsp_references output format per tool/lsp.go:574 "%s:%d:%d" with loc.Path absolute
	s.recordSearchToolCall("lsp_references", wexpArgs(t, map[string]interface{}{"path": "/Volumes/new/ggai/ggcode/internal/agent/agent.go"}),
		"/Volumes/new/ggai/ggcode/internal/tool/lsp.go:574:10\n/Volumes/new/ggai/ggcode/internal/agent/wasted_explore.go:239:6", 1)
	s.recordConsumptionToolCall("read_file",
		wexpArgs(t, map[string]interface{}{"path": "internal/agent/wasted_explore.go"}), // relative read works
		"", 2)
	for _, info := range s.pendingSearches {
		if !info.Consumed {
			t.Errorf("#482 regression: lsp absolute FoundPath must match relative read arg after normalization")
		}
	}
}

// Scenario E (opposite failure mode — false negative): code_search results are
// formatted "N. path (relevance: X%)"; extractPathFromLine stops at the first
// space and yields "N." which fails looksLikeFilePath → the search is never
// tracked at all, so genuinely wasted code_search calls are invisible.
func TestWastedExploreRepro_CodeSearchNeverTracked(t *testing.T) {
	s := newWastedExploreState()
	res := `Semantic search: "authentication"
Ranked 3 file(s) by relevance (of 500 searched):

1. internal/auth/oauth.go (relevance: 87%)
2. internal/auth/token.go (relevance: 64%)
3. internal/middleware/auth.go (relevance: 51%)

Use read_file or grep to inspect these files in detail.
`
	s.recordSearchToolCall("code_search", wexpArgs(t, map[string]interface{}{"query": "authentication"}), res, 1)
	if len(s.pendingSearches) == 0 {
		t.Fatalf("#482 regression: code_search 'N. path (relevance: X%%)' results must be tracked")
	}
	var tracked []string
	for _, info := range s.pendingSearches {
		tracked = append(tracked, info.FoundPaths...)
	}
	for _, want := range []string{"internal/auth/oauth.go", "internal/auth/token.go", "internal/middleware/auth.go"} {
		found := false
		for _, tp := range tracked {
			if weNormalizePath(tp) == want {
				found = true
			}
		}
		if !found {
			t.Errorf("#482 regression: code_search result missing path %s (tracked: %v)", want, tracked)
		}
	}
}

// Scenario F: glob dual-format — walkGlob (**) returns absolute paths while
// the plain-Glob branch returns relative paths; either way a mismatching read
// format on the other side never consumes it.
func TestWastedExploreRepro_GlobWalkGlobAbsolute(t *testing.T) {
	s := newWastedExploreState()
	// walkGlob branch output (tool/glob.go:180-181 filepath.Abs)
	s.recordSearchToolCall("glob", wexpArgs(t, map[string]interface{}{"pattern": "**/*.pen"}),
		"/Volumes/new/ggai/fluui/design/a.pen\n/Volumes/new/ggai/fluui/design/b.pen", 1)
	if len(s.pendingSearches) != 1 {
		t.Fatalf("expected glob search tracked, got %d", len(s.pendingSearches))
	}
	s.recordConsumptionToolCall("read_file",
		wexpArgs(t, map[string]interface{}{"path": "/Volumes/new/ggai/fluui/design/a.pen"}), "", 2)
	// absolute on both sides → should consume; but the sibling scenario with the
	// plain-Glob branch (relative "design/a.pen") vs absolute read would not.
	for id, info := range s.pendingSearches {
		if !info.Consumed {
			t.Errorf("walkGlob abs path vs abs read should match; id=%d", id)
		}
	}
}

func wexpContains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

func wexpFirstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}
