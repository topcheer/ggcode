package agentruntime

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
	override *provider.SamplingOverride
}

func (s *stopSeqStubProvider) Name() string { return "stub" }

func (s *stopSeqStubProvider) Chat(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {

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

	}
	if got := stub.StopSequences(); len(got) != 1 || got[0] != "PREV" {
		t.Fatalf("empty request must not touch provider sequences, got %v", got)
	}
}

var _ = json.RawMessage{}
