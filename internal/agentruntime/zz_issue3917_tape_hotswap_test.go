package agentruntime

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// fakeBase is a minimal Provider for hot-swap simulation (never called:
// replay serves from tape; record paths in this test only exercise wrap
// mechanics, no live calls).
type tapeFakeBase struct{ n int }

func (f *tapeFakeBase) Name() string { return "fake" }
func (f *tapeFakeBase) Chat(ctx context.Context, m []provider.Message, t []provider.ToolDefinition) (*provider.ChatResponse, error) {
	return nil, nil
}
func (f *tapeFakeBase) ChatStream(ctx context.Context, m []provider.Message, t []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	return nil, nil
}
func (f *tapeFakeBase) CountTokens(ctx context.Context, m []provider.Message) (int, error) {
	return 0, nil
}

// #3917: hot swaps through ApplyProviderToAgent must NOT re-wrap a fresh
// TapeProvider when the env spec is unchanged — the replay cursor must
// survive (no stale-response replay from position 0) and the record fd
// must be reused (no leak). A changed spec closes the old tape and wraps
// fresh.
func TestApplyProviderToAgentTapeIdempotent(t *testing.T) {
	dir := t.TempDir()
	tapePath := filepath.Join(dir, "t.jsonl")
	// Minimal replay tape: two chat entries so cursor advance is observable.
	tapeJSONL := `{"k":"key1","kind":"chat","resp":{"Message":{"content":[{"type":"text","text":"r1"}]}}}
{"k":"key2","kind":"chat","resp":{"Message":{"content":[{"type":"text","text":"r2"}]}}}
`
	if err := os.WriteFile(tapePath, []byte(tapeJSONL), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GGCODE_LLM_TAPE", "replay:"+tapePath)
	ag := agent.NewAgent(nil, tool.NewRegistry(), "", 10)
	defer ag.Close()

	resolved := &config.ResolvedEndpoint{VendorID: "v", BaseURL: "https://example.invalid", Model: "m"}
	ApplyProviderToAgent(ag, &tapeFakeBase{n: 1}, resolved)

	tp1, ok := ag.Provider().(*provider.TapeProvider)
	if !ok {
		t.Fatalf("first apply should wrap TapeProvider, got %T", ag.Provider())
	}
	// Advance the cursor: consume one FIFO entry.
	msgs := []provider.Message{{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hi"}}}}
	if _, err := tp1.Chat(context.Background(), msgs, nil); err != nil {
		t.Fatalf("first chat from tape: %v", err)
	}

	// Hot swap with unchanged spec: same wrapper instance, same state.
	ApplyProviderToAgent(ag, &tapeFakeBase{n: 2}, resolved)
	if got := ag.Provider(); got != provider.Provider(tp1) {
		t.Fatalf("unchanged-spec hot swap must reuse the TapeProvider instance, got new %T", got)
	}
	// The next FIFO entry must be r2 (cursor survived), not r1 (reset).
	resp, err := tp1.Chat(context.Background(), msgs, nil)
	if err != nil {
		t.Fatalf("second chat from tape: %v", err)
	}
	if txt := resp.Message.Content[0].Text; txt != "r2" {
		t.Fatalf("cursor was reset: got %q, want %q (stale replay = the #3917 bug)", txt, "r2")
	}

	// Changed spec: old tape closed, fresh wrap (different instance).
	other := filepath.Join(dir, "t2.jsonl")
	if err := os.WriteFile(other, []byte(tapeJSONL), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GGCODE_LLM_TAPE", "replay:"+other)
	ApplyProviderToAgent(ag, &tapeFakeBase{n: 3}, resolved)
	tp2, ok := ag.Provider().(*provider.TapeProvider)
	if !ok || tp2 == tp1 {
		t.Fatalf("changed-spec hot swap must wrap a fresh TapeProvider, got %T (same=%v)", ag.Provider(), tp2 == tp1)
	}

	// Spec removed entirely: agent gets the bare provider.
	t.Setenv("GGCODE_LLM_TAPE", "")
	ApplyProviderToAgent(ag, &tapeFakeBase{n: 4}, resolved)
	if _, isTape := ag.Provider().(*provider.TapeProvider); isTape {
		t.Fatalf("env removed: agent must hold bare provider, got TapeProvider")
	}
}

// TapeProviderSpec / Rebase / Close unit checks.
func TestTapeProviderSpecRebaseClose(t *testing.T) {
	bare := &tapeFakeBase{}
	if s := provider.TapeProviderSpec(bare); s != "" {
		t.Fatalf("bare provider spec = %q, want empty", s)
	}

	dir := t.TempDir()
	rec := filepath.Join(dir, "rec.jsonl")
	t.Setenv("GGCODE_LLM_TAPE", "record:"+rec)
	wrapped := provider.WrapLLMTapeFromEnv(bare)
	if s := provider.TapeProviderSpec(wrapped); s != "record:"+rec {
		t.Fatalf("spec = %q", s)
	}
	tp := wrapped.(*provider.TapeProvider)

	// Rebase swaps inner, keeps file handle (same fd, no reopen/leak).
	tp.Rebase(&tapeFakeBase{n: 9})
	if tp.Name() != "fake" {
		t.Fatalf("rebase lost inner: Name=%q", tp.Name())
	}
	if err := tp.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := tp.Close(); err != nil { // double-close safe
		t.Fatalf("second close: %v", err)
	}
}
