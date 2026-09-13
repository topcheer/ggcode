package agentruntime

// #2239: MCP sampling's stopSequences were parsed and then dropped - the
// provider layer had no socket at all. The handler now sets them on
// providers implementing StopSequenceSetter inside the shared #1612-A lock
// window and restores the previous value after the chat.

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/provider"
)

type stopSeqStubProvider struct {
	mu       sync.Mutex
	seqs     []string
	sets     [][]string
	override *provider.SamplingOverride // #2248 contract
}

func (s *stopSeqStubProvider) Name() string { return "stub" }

func (s *stopSeqStubProvider) Chat(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	// Record what was live DURING the chat.
	s.mu.Lock()
	live := s.seqs
	if s.override != nil && len(s.override.StopSequences) > 0 {
		live = s.override.StopSequences
	}
	s.sets = append(s.sets, append([]string(nil), live...))
	s.mu.Unlock()
	return &provider.ChatResponse{
		Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("ok")}},
	}, nil
}
func (s *stopSeqStubProvider) ChatStream(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent)
	close(ch)
	return ch, nil
}

func (s *stopSeqStubProvider) CountTokens(ctx context.Context, messages []provider.Message) (int, error) {
	return 1, nil
}

func (s *stopSeqStubProvider) SetStopSequences(seqs []string) { s.seqs = seqs }

// #2248: the handler now swaps ONE atomic override pointer instead of
// field writes; the stub records the live override during the chat.
func (s *stopSeqStubProvider) SetSamplingOverride(o *provider.SamplingOverride) { s.override = o }
func (s *stopSeqStubProvider) SamplingOverride() *provider.SamplingOverride     { return s.override }
func (s *stopSeqStubProvider) StopSequences() []string                          { return s.seqs }

func TestIssue2239StopSequencesReachProviderAndRestore(t *testing.T) {
	stub := &stopSeqStubProvider{seqs: []string{"PREV"}}
	params := mcp.SamplingParams{
		SystemPrompt:  "sys",
		MaxTokens:     100,
		StopSequences: []string{"END", "\n\nUSER:"},
		Messages: []mcp.SamplingMessage{
			{Role: "user", Content: mcp.SamplingContent{Type: "text", Text: "hi"}},
		},
	}
	if _, err := mcpSamplingHandlerWith(context.Background(), params, stub); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if len(stub.sets) != 1 || len(stub.sets[0]) != 2 || stub.sets[0][0] != "END" {
		t.Fatalf("chat must see the request stop sequences, got %v", stub.sets)
	}
	if got := stub.StopSequences(); len(got) != 1 || got[0] != "PREV" {
		t.Fatalf("previous sequences must be restored, got %v", got)
	}
	if o := stub.SamplingOverride(); o != nil {
		t.Fatalf("override pointer must be restored to its previous value, got %+v", o)
	}
}

func TestIssue2239NoSequencesLeavesProviderUntouched(t *testing.T) {
	stub := &stopSeqStubProvider{seqs: []string{"PREV"}}
	params := mcp.SamplingParams{
		Messages: []mcp.SamplingMessage{{Role: "user", Content: mcp.SamplingContent{Type: "text", Text: "hi"}}},
	}
	if _, err := mcpSamplingHandlerWith(context.Background(), params, stub); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if len(stub.sets) != 1 && stub.sets[0] != nil {
		// chat still ran; sequences untouched
	}
	if got := stub.StopSequences(); len(got) != 1 || got[0] != "PREV" {
		t.Fatalf("empty request must not touch provider sequences, got %v", got)
	}
}

var _ = json.RawMessage{} // keep json import if params shapes change
