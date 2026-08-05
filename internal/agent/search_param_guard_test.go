package agent

import (
	"encoding/json"
	"testing"
)

func TestSearchParamGuard_Reset(t *testing.T) {
	g := newSearchParamGuard()
	g.fires = 5
	g.reset()
	if g.fires != 0 {
		t.Fatalf("expected fires=0 after reset, got %d", g.fires)
	}
}

func TestSearchParamGuard_MaxFires(t *testing.T) {
	g := newSearchParamGuard()
	// Fire 3 times
	for i := 0; i < 3; i++ {
		args, _ := json.Marshal(map[string]string{"pattern": ".*"})
		hint := g.checkParamQuality("grep", args)
		if i < 3 && hint == "" {
			t.Fatalf("expected hint on call %d", i)
		}
	}
	// 4th call should be suppressed
	args, _ := json.Marshal(map[string]string{"pattern": ".*"})
	hint := g.checkParamQuality("grep", args)
	if hint != "" {
		t.Fatalf("expected empty hint after max fires, got: %s", hint)
	}
}

func TestSearchParamGuard_GrepWildcardsOnly(t *testing.T) {
	g := newSearchParamGuard()
	args, _ := json.Marshal(map[string]string{"pattern": ".*"})
	hint := g.checkParamQuality("grep", args)
	if hint == "" {
		t.Fatal("expected hint for wildcard-only pattern .*")
	}
}

func TestSearchParamGuard_GrepSingleChar(t *testing.T) {
	g := newSearchParamGuard()
	args, _ := json.Marshal(map[string]string{"pattern": "a"})
	hint := g.checkParamQuality("grep", args)
	if hint == "" {
		t.Fatal("expected hint for single char pattern")
	}
}

func TestSearchParamGuard_GrepSpecificPattern(t *testing.T) {
	g := newSearchParamGuard()
	args, _ := json.Marshal(map[string]string{"pattern": "func.*Handler"})
	hint := g.checkParamQuality("grep", args)
	if hint != "" {
		t.Fatalf("expected no hint for specific pattern, got: %s", hint)
	}
}

func TestSearchParamGuard_SearchFilesEmpty(t *testing.T) {
	g := newSearchParamGuard()
	args, _ := json.Marshal(map[string]string{"pattern": ""})
	hint := g.checkParamQuality("search_files", args)
	if hint == "" {
		t.Fatal("expected hint for empty search pattern")
	}
}

func TestSearchParamGuard_SearchFilesSingleChar(t *testing.T) {
	g := newSearchParamGuard()
	args, _ := json.Marshal(map[string]string{"pattern": "x"})
	hint := g.checkParamQuality("search_files", args)
	if hint == "" {
		t.Fatal("expected hint for single char search")
	}
}

func TestSearchParamGuard_GlobAllFiles(t *testing.T) {
	for _, p := range []string{"*", "**/*", "**", "*.*"} {
		g := newSearchParamGuard() // fresh guard per pattern to avoid max-fires
		args, _ := json.Marshal(map[string]string{"pattern": p})
		hint := g.checkParamQuality("glob", args)
		if hint == "" {
			t.Fatalf("expected hint for glob pattern %s", p)
		}
	}
}

func TestSearchParamGuard_GlobSpecific(t *testing.T) {
	g := newSearchParamGuard()
	args, _ := json.Marshal(map[string]string{"pattern": "**/*.go"})
	hint := g.checkParamQuality("glob", args)
	if hint != "" {
		t.Fatalf("expected no hint for specific glob, got: %s", hint)
	}
}

func TestSearchParamGuard_CodeSearchShort(t *testing.T) {
	g := newSearchParamGuard()
	args, _ := json.Marshal(map[string]string{"query": "ab"})
	hint := g.checkParamQuality("code_search", args)
	if hint == "" {
		t.Fatal("expected hint for short code_search query")
	}
}

func TestSearchParamGuard_CodeSearchDescriptive(t *testing.T) {
	g := newSearchParamGuard()
	args, _ := json.Marshal(map[string]string{"query": "authentication token refresh logic"})
	hint := g.checkParamQuality("code_search", args)
	if hint != "" {
		t.Fatalf("expected no hint for descriptive query, got: %s", hint)
	}
}

func TestSearchParamGuard_UnknownTool(t *testing.T) {
	g := newSearchParamGuard()
	args, _ := json.Marshal(map[string]string{"pattern": ".*"})
	hint := g.checkParamQuality("unknown_tool", args)
	if hint != "" {
		t.Fatalf("expected no hint for unknown tool, got: %s", hint)
	}
}
