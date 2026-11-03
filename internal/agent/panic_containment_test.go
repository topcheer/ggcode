package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

func TestRunStreamWithContent_PanicBecomesError(t *testing.T) {

	t.Setenv("HOME", t.TempDir())

	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{
				Role: "assistant",
				Content: []provider.ContentBlock{
					{Type: "text", Text: "hello"},
				},
			},
		},
	}
	a := NewAgent(mp, tool.NewRegistry(), "", 1)

	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("RunStreamWithContent leaked its panic: %v", r)
			}
		}()
		err = a.RunStreamWithContent(context.Background(),
			[]provider.ContentBlock{{Type: "text", Text: "hi"}},
			func(event provider.StreamEvent) {

				panic("injected: stream handler blew up")
			})
	}()

	if err == nil {
		t.Fatal("expected a panic-converted error, got nil")
	}
	for _, want := range []string{"agent run panicked", "injected: stream handler blew up", "crash"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}
