package agent

import "testing"

// zz_issue3736_test.go - regression tests for tunnel-vision breadth
// counting on bare-path search output shapes (#3736).
//
// #476 made search-tool output count toward exploration breadth, but
// extractSearchResultPaths only recognized the "path.ext:N" (grep content /
// search_files / LSP) shape. grep's DEFAULT output_mode is files_with_matches
// (one bare path per line) and code_search prints "N. path (relevance: %d%%)"
// - both silently yielded zero counted files, so the typical broad grep sweep
// still triggered the bogus "Scope narrowness" warning that #476 was meant to
// eliminate.

func TestIssue3736GrepFilesWithMatchesCounted(t *testing.T) {
	// Shape: grep.go formatFilesWithMatches - one bare path per line.
	out := `internal/agent/agent.go
internal/agent/tunnel_vision.go
internal/tool/grep.go
internal/tool/code_search.go
cmd/ggcode/main.go
`
	paths := extractSearchResultPaths("grep", out)
	if len(paths) < 5 {
		t.Fatalf("files_with_matches output: expected 5 counted paths, got %d (%v)", len(paths), paths)
	}
	want := "internal/agent/tunnel_vision.go"
	found := false
	for _, p := range paths {
		if p == want {
			found = true
		}
	}
	if !found {
		t.Errorf("bare-path line %q not extracted; got %v", want, paths)
	}
}

func TestIssue3736CodeSearchRankedListCounted(t *testing.T) {
	// Shape: code_search.go - "N. path (relevance: %d%%)" ranked list.
	out := `Found 3 files matching "authentication error handling":

1. internal/auth/oauth.go (relevance: 92%)
2. internal/auth/token.go (relevance: 87%)
3. internal/im/adapter.go (relevance: 71%)

Use read_file or grep to inspect these files in detail.
`
	paths := extractSearchResultPaths("code_search", out)
	if len(paths) < 3 {
		t.Fatalf("code_search ranked output: expected >=3 counted paths, got %d (%v)", len(paths), paths)
	}
	found := false
	for _, p := range paths {
		if p == "internal/auth/oauth.go" {
			found = true
		}
	}
	if !found {
		t.Errorf("ranked entry internal/auth/oauth.go not extracted; got %v", paths)
	}
}

func TestIssue3736ContentModeStillCounted(t *testing.T) {
	// Shape: grep content / search_files - "path:line:content" (pre-existing
	// #476 behavior must not regress).
	out := `internal/agent/agent.go:4099:for _, p := range extractSearchResultPaths(
internal/tool/grep.go:31:Defaults to files_with_matches
`
	paths := extractSearchResultPaths("grep", out)
	if len(paths) != 2 {
		t.Fatalf("content-mode output: expected 2 counted paths, got %d (%v)", len(paths), paths)
	}
}

func TestIssue3736NoPathInflationOnProse(t *testing.T) {
	// Non-bare-path tools (e.g. a content-mode result carrying prose that
	// merely mentions files) must not inflate breadth via pathOnlyRe beyond
	// what the strict ":N" shape already finds.
	out := `2 files had matches. Consider looking at README.md and docs/guide.
internal/agent/agent.go:12:func main()
`
	paths := extractSearchResultPaths("search_files", out)
	for _, p := range paths {
		if p == "README.md" || p == "docs/guide" {
			t.Errorf("prose mention %q incorrectly counted as searched file; got %v", p, paths)
		}
	}
}

func TestIssue3736EndToEndNoBogusNarrownessWarning(t *testing.T) {
	// Issue's trigger scenario: 16 iterations, 2 read_file's, and 3 default-
	// mode greps each hitting 10+ files. Before the fix searchedFiles was
	// empty, ratio 16/2=8.0 >= 4.0 fired the warning. After it, the breadth
	// from bare-path results suppresses the warning.
	s := newTunnelVisionState()
	s.baseDir = "/repo"
	s.recordFile("/repo/a.go")
	s.recordFile("/repo/b.go")
	for i := 0; i < 3; i++ {
		for j := 0; j < 10; j++ {
			s.recordSearched(extractSearchResultPaths("grep", "internal/pkg/file"+string(rune('a'+j))+".go\n")[0])
		}
	}
	if msg := s.check(16); msg != "" {
		t.Fatalf("bogus narrowness warning fired despite broad bare-path search breadth: %q", msg)
	}
}
