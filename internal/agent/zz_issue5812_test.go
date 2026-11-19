package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/provider"
)

// TestIssue5812SamplerWiringAtProviderBoundary pins the conversation-loop
// hook at the unit level: manager state (head text, tail bytes, mutation
// sources, non-tail seq) flows into a well-formed ctxstats line, the
// drain is consumed so the next line carries no stale sources, and the
// head hash is byte-stable across two append-only turns.
func TestIssue5812SamplerWiringAtProviderBoundary(t *testing.T) {
	a := NewAgent(nil, nil, "BASE PROMPT", 1)
	dir := t.TempDir()
	t.Setenv("GGCODE_CTX_SAMPLE_JSONL_DIR", dir)
	t.Setenv("GGCODE_CTX_SAMPLE", "1")
	t.Setenv("HOME", t.TempDir())
	cm := a.contextManager.(*ctxpkg.Manager)
	cm.UpdateFirstSystemMessage(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "SESSION HEAD"}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "user question"}}})
	tools := []provider.ToolDefinition{{Name: "read_file", Description: "read a file"}}

	a.samplePromptCacheStats(provider.TokenUsage{InputTokens: 1000, CacheRead: 700, CacheWrite: 300}, tools)
	a.samplePromptCacheStats(provider.TokenUsage{InputTokens: 950, CacheRead: 850, CacheWrite: 100}, tools)

	path := filepath.Join(dir, fmt.Sprintf("ctxstats-%d.jsonl", os.Getpid()))
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ctxstats jsonl missing: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 ctxstats lines, got %d: %s", len(lines), b)
	}
	var first, second map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("line 1 not json: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("line 2 not json: %v", err)
	}
	if first["head_sha256"] != second["head_sha256"] {
		t.Fatal("append-only turns must keep head_sha256 constant")
	}
	if first["drift_event"] != false || second["drift_event"] != false {
		t.Fatal("no drift expected in this fixture")
	}
	if v, ok := first["non_tail_mut_seq"].(float64); !ok || int64(v) != cm.NonTailMutSeq() {
		t.Fatalf("non_tail_mut_seq = %v, want %d", first["non_tail_mut_seq"], cm.NonTailMutSeq())
	}
	if v, ok := first["tail_bytes"].(float64); !ok || int(v) != len("user question") {
		t.Fatalf("tail_bytes = %v, want %d", first["tail_bytes"], len("user question"))
	}
	// NewAgent seeds BASE PROMPT as the head, so replacing it here is the
	// rewrite branch of UpdateFirstSystemMessage.
	srcs, ok := first["mut_sources"].([]any)
	if !ok || len(srcs) != 1 || srcs[0] != ctxpkg.MutSourceSystemRewrite {
		t.Fatalf("line 1 mut_sources = %v, want [system-rewrite]", first["mut_sources"])
	}
	if _, still := second["mut_sources"]; still {
		t.Fatalf("line 2 must not carry stale sources, got %v", second["mut_sources"])
	}
	if v, ok := second["hit_ratio"].(float64); !ok || v != float64(850)/float64(950) {
		t.Fatalf("hit_ratio = %v, want 850/950", second["hit_ratio"])
	}
}
