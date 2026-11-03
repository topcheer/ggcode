package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type slowStreamFake struct {
	events       []StreamEvent
	producerDone chan struct{}
}

func newSlowStreamFake(n int) *slowStreamFake {
	events := make([]StreamEvent, n)
	for i := range events {
		events[i] = StreamEvent{Type: StreamEventText, Text: fmt.Sprintf("chunk-%d", i)}
	}
	return &slowStreamFake{events: events, producerDone: make(chan struct{})}
}

func (s *slowStreamFake) Name() string { return "slowfake" }

func (s *slowStreamFake) CountTokens(ctx context.Context, messages []Message) (int, error) {
	return 1, nil
}

func (s *slowStreamFake) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResponse, error) {
	return &ChatResponse{Message: Message{Role: "assistant", Content: []ContentBlock{{Type: "text", Text: "chat"}}}, StopReason: "end_turn"}, nil
}

func (s *slowStreamFake) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	src := make(chan StreamEvent)
	go func() {
		defer close(src)
		for _, ev := range s.events {
			select {
			case src <- ev:
			case <-time.After(10 * time.Second):
				return
			}
		}
		close(s.producerDone)
	}()
	return src, nil
}

func TestTapeRecordStreamAbandonDrainsAndAligns(t *testing.T) {
	dir := t.TempDir()
	tape := filepath.Join(dir, "traj.llmtape.jsonl")
	fake := newSlowStreamFake(4)

	t.Setenv("GGCODE_LLM_TAPE", "record:"+tape)
	rp := WrapLLMTapeFromEnv(fake)

	ctx, cancel := context.WithCancel(context.Background())
	stream, err := rp.ChatStream(ctx, []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}}, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	<-stream
	cancel()

	select {
	case <-fake.producerDone:
	case <-time.After(8 * time.Second):
		t.Fatal("inner producer never finished: record goroutine leaked on abandoned consumer (src not drained)")
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		if ok := tapeHasStreamEntry(t, tape); ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("tape never gained a stream entry after abandon: replay alignment broken")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func tapeHasStreamEntry(t *testing.T, path string) bool {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var probe struct {
			Kind   string      `json:"kind"`
			Events interface{} `json:"ev"`
		}
		if json.Unmarshal([]byte(line), &probe) != nil {
			continue
		}
		if probe.Kind == "stream" && probe.Events != nil {
			return true
		}
	}
	return false
}

func TestFaultInjectStreamAbandonDrains(t *testing.T) {
	fake := newSlowStreamFake(4)

	t.Setenv(llmFaultEnv, "omit-content@1")
	w := WrapLLMFaultFromEnv(fake)
	fp, ok := w.(*FaultProvider)
	if !ok {
		t.Fatalf("expected *FaultProvider, got %T", w)
	}

	ctx, cancel := context.WithCancel(context.Background())
	stream, err := fp.ChatStream(ctx, []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}}, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	<-stream
	cancel()

	select {
	case <-fake.producerDone:
	case <-time.After(8 * time.Second):
		t.Fatal("inner producer never finished: fault-inject goroutine leaked on abandoned consumer (src not drained)")
	}
}
