package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// The main-goroutine panic containment in RunStreamWithContent (registered
// as the first defer, so it unwinds last) must convert a panicking run into
// a returned error - not kill the process - and write a crash log.
func TestRunStreamWithContent_PanicBecomesError(t *testing.T) {
	// Redirect HOME so the crash journal never touches the real ~/.ggcode.
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
				// Panic mid-stream: fires inside the run loop's main
				// goroutine, the exact containment target.
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
