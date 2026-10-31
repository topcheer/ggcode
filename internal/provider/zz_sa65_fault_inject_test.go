package provider

// sa-65 companion tests: GGCODE_LLM_FAULT deterministic fault injection
// (AgentChaos taxonomy: crash / omission / value × content / tool-call).
// Each test pins one observable behavior, including the
// "verify fault triggered" contract (Fired only records faults the
// agent could actually observe) and the no-mutation contract for shared
// replay-tape entries.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type sa65FakeLLM struct {
	calls  int
	resp   *ChatResponse
	events []StreamEvent
}

func (f *sa65FakeLLM) Name() string { return "sa65fake" }

func (f *sa65FakeLLM) CountTokens(ctx context.Context, messages []Message) (int, error) {
	return 1, nil
}

func (f *sa65FakeLLM) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResponse, error) {
	f.calls++
	cp := *f.resp
	return &cp, nil
}

func (f *sa65FakeLLM) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	f.calls++
	out := make(chan StreamEvent, len(f.events))
	for _, ev := range f.events {
		out <- ev
	}
	close(out)
	return out, nil
}

func sa65ChatFake() *sa65FakeLLM {
	return &sa65FakeLLM{resp: &ChatResponse{Message: Message{Role: "assistant", Content: []ContentBlock{
		{Type: "text", Text: "hello world from model"},
		{Type: "tool_use", ToolName: "edit_file", ToolID: "t1", Input: json.RawMessage(`{"file_path":"/tmp/a.go"}`)},
	}}}}
}

func sa65Wrap(t *testing.T, fake *sa65FakeLLM, spec string) *FaultProvider {
	t.Helper()
	t.Setenv(llmFaultEnv, spec)
	w := WrapLLMFaultFromEnv(fake)
	fp, ok := w.(*FaultProvider)
	if !ok {
		t.Fatalf("spec %q must wrap into *FaultProvider, got %T", spec, w)
	}
	return fp
}

func TestSA65FaultSpecParsingSkipsInvalid(t *testing.T) {
	fake := sa65ChatFake()
	fp := sa65Wrap(t, fake, "crash@2, bogus@1, crash@zero, value-text@1")
	if len(fp.plan) != 2 {
		t.Fatalf("want 2 valid directives, got %d: %+v", len(fp.plan), fp.plan)
	}
	// Entirely-invalid spec must not wrap at all (fail-open, like the tape).
	t.Setenv(llmFaultEnv, "nope")
	if w := WrapLLMFaultFromEnv(fake); w != Provider(fake) {
		t.Fatal("invalid-only spec must return the provider unchanged")
	}
}

func TestSA65FaultCrashChat(t *testing.T) {
	fake := sa65ChatFake()
	fp := sa65Wrap(t, fake, "crash@1")
	resp, err := fp.Chat(context.Background(), nil, nil)
	if err == nil || resp != nil {
		t.Fatalf("crash@1 must fail the call, got resp=%v err=%v", resp, err)
	}
	if !strings.Contains(err.Error(), llmFaultEnv) {
		t.Fatalf("error must name the injector, got %v", err)
	}
	if fake.calls != 0 {
		t.Fatal("crash must fire BEFORE the inner call (no wasted live tokens)")
	}
	if got := fp.Fired(); len(got) != 1 || got[0] != "crash@1" {
		t.Fatalf("Fired() = %v", got)
	}
}

func TestSA65FaultCrashStream(t *testing.T) {
	fake := sa65ChatFake()
	fp := sa65Wrap(t, fake, "crash@1")
	src, err := fp.ChatStream(context.Background(), nil, nil)
	if err == nil || src != nil {
		t.Fatalf("crash@1 stream must fail the call, got src=%v err=%v", src, err)
	}
}

func TestSA65FaultOmitToolChat(t *testing.T) {
	fake := sa65ChatFake()
	fp := sa65Wrap(t, fake, "omit-tool@1")
	resp, err := fp.Chat(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Message.Content) != 1 || resp.Message.Content[0].Type != "text" {
		t.Fatalf("omit-tool must leave only text, got %+v", resp.Message.Content)
	}
	// No-mutation contract: the original response (a stand-in for a shared
	// replay-tape entry) must keep its tool_use block.
	if len(fake.resp.Message.Content) != 2 || fake.resp.Message.Content[1].Type != "tool_use" {
		t.Fatalf("original response was mutated: %+v", fake.resp.Message.Content)
	}
	if got := fp.Fired(); len(got) != 1 || got[0] != "omit-tool@1" {
		t.Fatalf("Fired() = %v", got)
	}
}

func TestSA65FaultOmitContentChat(t *testing.T) {
	fake := sa65ChatFake()
	fp := sa65Wrap(t, fake, "omit-content@1")
	resp, err := fp.Chat(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Message.Content) != 1 || resp.Message.Content[0].Type != "tool_use" {
		t.Fatalf("omit-content must leave only tool_use, got %+v", resp.Message.Content)
	}
}

func TestSA65FaultValueArgsChat(t *testing.T) {
	fake := sa65ChatFake()
	fp := sa65Wrap(t, fake, "value-args@1")
	resp, err := fp.Chat(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var probe any
	if uerr := json.Unmarshal(resp.Message.Content[1].Input, &probe); uerr == nil {
		t.Fatalf("value-args must yield invalid JSON, got %s", resp.Message.Content[1].Input)
	}
	if got := fp.Fired(); len(got) != 1 || got[0] != "value-args@1" {
		t.Fatalf("Fired() = %v", got)
	}
}

func TestSA65FaultValueTextChat(t *testing.T) {
	fake := sa65ChatFake()
	fp := sa65Wrap(t, fake, "value-text@1")
	resp, err := fp.Chat(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := resp.Message.Content[0].Text
	if got == "" || got == "hello world from model" || !strings.HasPrefix("hello world from model", got) {
		t.Fatalf("value-text must return a proper truncation prefix, got %q", got)
	}
}

func sa65StreamFake() *sa65FakeLLM {
	return &sa65FakeLLM{events: []StreamEvent{
		{Text: "hello world"},
		{Tool: ToolCallDelta{ID: "t1", Name: "edit_file", Arguments: json.RawMessage(`{"a":1}`)}},
		{Usage: &TokenUsage{}},
	}}
}

func collectSA65(t *testing.T, src <-chan StreamEvent) []StreamEvent {
	t.Helper()
	var out []StreamEvent
	for ev := range src {
		out = append(out, ev)
	}
	return out
}

func TestSA65FaultStreamOmitTool(t *testing.T) {
	fake := sa65StreamFake()
	fp := sa65Wrap(t, fake, "omit-tool@1")
	src, err := fp.ChatStream(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	evs := collectSA65(t, src)
	if len(evs) != 2 || evs[0].Text != "hello world" || evs[1].Usage == nil {
		t.Fatalf("omit-tool stream must keep text+usage and drop the tool event, got %+v", evs)
	}
	if got := fp.Fired(); len(got) != 1 || got[0] != "omit-tool@1" {
		t.Fatalf("Fired() = %v", got)
	}
}

func TestSA65FaultStreamValueArgs(t *testing.T) {
	fake := sa65StreamFake()
	fp := sa65Wrap(t, fake, "value-args@1")
	src, err := fp.ChatStream(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	evs := collectSA65(t, src)
	var probe any
	if uerr := json.Unmarshal(evs[1].Tool.Arguments, &probe); uerr == nil {
		t.Fatalf("stream value-args must corrupt arguments, got %s", evs[1].Tool.Arguments)
	}
}

func TestSA65FaultIndexIsolation(t *testing.T) {
	fake := sa65ChatFake()
	fp := sa65Wrap(t, fake, "value-text@2")
	r1, err := fp.Chat(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Message.Content[0].Text != "hello world from model" {
		t.Fatal("call 1 is unplanned and must pass through untouched")
	}
	r2, err := fp.Chat(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Message.Content[0].Text == "hello world from model" {
		t.Fatal("call 2 must be truncated")
	}
	if fake.calls != 2 {
		t.Fatalf("inner must see both calls, got %d", fake.calls)
	}
}

func TestSA65FaultPassThroughUnplanned(t *testing.T) {
	fake := sa65ChatFake()
	fp := sa65Wrap(t, fake, "crash@5")
	for i := 0; i < 2; i++ {
		if _, err := fp.Chat(context.Background(), nil, nil); err != nil {
			t.Fatalf("unplanned call %d must be clean, got %v", i+1, err)
		}
	}
	if len(fp.Fired()) != 0 {
		t.Fatalf("nothing should fire, got %v", fp.Fired())
	}
}

func TestSA65FaultFiredOnlyWhenObservable(t *testing.T) {
	// omit-tool on a text-only response: nothing observable happened, so
	// Fired must stay empty (AgentChaos verify-triggered semantics).
	fake := &sa65FakeLLM{resp: &ChatResponse{Message: Message{Role: "assistant",
		Content: []ContentBlock{{Type: "text", Text: "just text"}}}}}
	fp := sa65Wrap(t, fake, "omit-tool@1")
	resp, err := fp.Chat(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Message.Content) != 1 || resp.Message.Content[0].Text != "just text" {
		t.Fatal("response must be returned unchanged")
	}
	if len(fp.Fired()) != 0 {
		t.Fatalf("unobservable fault must not fire, got %v", fp.Fired())
	}
}

func TestSA65FaultChatAndStreamShareCallNumbering(t *testing.T) {
	fake := sa65ChatFake()
	fake.events = sa65StreamFake().events
	fp := sa65Wrap(t, fake, "crash@2")
	if _, err := fp.Chat(context.Background(), nil, nil); err != nil {
		t.Fatalf("call 1 (chat) must be clean: %v", err)
	}
	if _, err := fp.ChatStream(context.Background(), nil, nil); err == nil {
		t.Fatal("call 2 (stream) must hit crash@2")
	}
}
