package agent

import (
	"context"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// #5002: the context manager window must be derived from provider model
// metadata (known-model lookup) instead of the hardcoded 128000; models with
// a smaller real window previously let auto-compact fire too late, so runs
// hit the raw overflow error first.
func TestIssue5002InitialContextWindowTracksKnownTable(t *testing.T) {
	fake := &fakeModelProvider{name: "glm-4"}
	known := provider.LookupKnownModelContextWindow("glm-4")
	got := initialContextWindow(fake)
	if known > 0 && got != known {
		t.Fatalf("known model window must be derived from the table: got %d want %d", got, known)
	}
	if known <= 0 && got != defaultContextWindow {
		t.Fatalf("table-miss must fall back to %d, got %d", defaultContextWindow, got)
	}
}

func TestIssue5002InitialContextWindowFallback(t *testing.T) {
	// No model metadata -> 128000 fallback (unchanged legacy behavior).
	if got := initialContextWindow(&fakeModelProvider{name: ""}); got != defaultContextWindow {
		t.Fatalf("missing metadata must fall back to %d, got %d", defaultContextWindow, got)
	}
	// Provider not implementing ModelNameProvider -> fallback.
	if got := initialContextWindow(&bareProvider{}); got != defaultContextWindow {
		t.Fatalf("non-ModelNameProvider must fall back to %d, got %d", defaultContextWindow, got)
	}
	// Unknown model name -> fallback.
	if got := initialContextWindow(&fakeModelProvider{name: "totally-unknown-model-xyz"}); got != defaultContextWindow {
		t.Fatalf("unknown model must fall back to %d, got %d", defaultContextWindow, got)
	}
}

// The full NewAgent construction path with a metadata provider must not
// regress (window wiring flows through initialContextWindow).
func TestIssue5002NewAgentConstructs(t *testing.T) {
	a := NewAgent(&fakeModelProvider{name: "glm-4"}, tool.NewRegistry(), "sys", 3)
	if a == nil || a.contextManager == nil {
		t.Fatal("NewAgent must construct with a context manager")
	}
}

type fakeModelProvider struct {
	name string
}

func (f *fakeModelProvider) Name() string { return "fake" }
func (f *fakeModelProvider) ModelName() string {
	return f.name
}

func (f *fakeModelProvider) Chat(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{}, nil
}

func (f *fakeModelProvider) ChatStream(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent)
	close(ch)
	return ch, nil
}

func (f *fakeModelProvider) CountTokens(ctx context.Context, messages []provider.Message) (int, error) {
	return 0, nil
}

type bareProvider struct{}

func (b *bareProvider) Name() string { return "bare" }

func (b *bareProvider) Chat(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{}, nil
}

func (b *bareProvider) ChatStream(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent)
	close(ch)
	return ch, nil
}

func (b *bareProvider) CountTokens(ctx context.Context, messages []provider.Message) (int, error) {
	return 0, nil
}
