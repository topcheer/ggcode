package im

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

func TestIMEmitterNil(t *testing.T) {
	var e *IMEmitter

	// All methods on nil should be safe no-ops
	_ = e.EmitText("hello")
	e.EmitUserText("hello")
	e.EmitUserTextExcept("hello", "qq")
	e.EmitStatus("thinking")
	e.EmitToolStatus("read_file", `{"path":"/tmp/file.go"}`)
	e.EmitRoundSummary("done", 5, 3, 2)
	e.SetOutputMode("quiet")
	if mode := e.OutputMode(); mode != "verbose" {
		t.Errorf("nil OutputMode = %q, want verbose", mode)
	}
	if mgr := e.Manager(); mgr != nil {
		t.Error("nil Manager should return nil")
	}
	if e.HasTargets() {
		t.Error("nil HasTargets should be false")
	}
	e.EmitKnightReport("report")
	if s := e.FormatAskUserPrompt(`{"title":"test"}`); s != "" {
		t.Error("nil FormatAskUserPrompt should return empty")
	}
}

func TestIMEmitterOutputMode(t *testing.T) {
	e := NewIMEmitter(nil, "en", "")
	if mode := e.OutputMode(); mode != "verbose" {
		t.Errorf("default OutputMode = %q, want verbose", mode)
	}
	e.SetOutputMode("quiet")
	if mode := e.OutputMode(); mode != "quiet" {
		t.Errorf("after SetOutputMode: %q, want quiet", mode)
	}
	e.SetOutputMode("")
	if mode := e.OutputMode(); mode != "verbose" {
		t.Errorf("empty mode should default to verbose: %q", mode)
	}
}

func TestIMEmitterManager(t *testing.T) {
	mgr := NewManager()
	e := NewIMEmitter(mgr, "en", "/tmp")
	if returned := e.Manager(); returned != mgr {
		t.Error("Manager() should return the same manager")
	}
}

func TestIMEmitterHasTargets(t *testing.T) {
	mgr := NewManager()
	e := NewIMEmitter(mgr, "en", "/tmp")

	if e.HasTargets() {
		t.Error("no bindings, HasTargets should be false")
	}

	mgr.currentBindings["qq"] = &ChannelBinding{Adapter: "qq", ChannelID: "ch1"}
	if !e.HasTargets() {
		t.Error("has binding, HasTargets should be true")
	}
}

func TestIMEmitterEmitText(t *testing.T) {
	mgr := NewManager()
	sink := &namedCaptureSink{name: "qq"}
	mgr.RegisterSink(sink)
	mgr.currentBindings["qq"] = &ChannelBinding{Adapter: "qq", ChannelID: "ch1"}

	e := NewIMEmitter(mgr, "en", "/tmp")
	_ = e.EmitText("hello world")

	// Give the async goroutine time to process
	time.Sleep(150 * time.Millisecond)

	events := sink.events()
	if len(events) == 0 {
		t.Fatal("expected at least one event")
	}
	if events[len(events)-1].Text != "hello world" {
		t.Errorf("text = %q, want %q", events[len(events)-1].Text, "hello world")
	}

	// Empty text should not emit
	sink.reset()
	_ = e.EmitText("")
	_ = e.EmitText("   ")
	time.Sleep(50 * time.Millisecond)
	if len(sink.events()) != 0 {
		t.Error("empty text should not emit")
	}
}

func TestIMEmitterEmitUserText(t *testing.T) {
	mgr := NewManager()
	sink := &namedCaptureSink{name: "qq"}
	mgr.RegisterSink(sink)
	mgr.currentBindings["qq"] = &ChannelBinding{Adapter: "qq", ChannelID: "ch1"}

	e := NewIMEmitter(mgr, "en", "/tmp")
	e.EmitUserText("hello")
	time.Sleep(150 * time.Millisecond)

	events := sink.events()
	if len(events) == 0 {
		t.Fatal("expected at least one event")
	}
	if !strings.Contains(events[len(events)-1].Text, "hello") {
		t.Errorf("event text = %q", events[len(events)-1].Text)
	}
	if !strings.Contains(events[len(events)-1].Text, "User") {
		t.Errorf("should contain user marker: %q", events[len(events)-1].Text)
	}
}

func TestIMEmitterEmitStatus(t *testing.T) {
	mgr := NewManager()
	sink := &namedCaptureSink{name: "qq"}
	mgr.RegisterSink(sink)
	mgr.currentBindings["qq"] = &ChannelBinding{Adapter: "qq", ChannelID: "ch1"}

	e := NewIMEmitter(mgr, "en", "/tmp")
	e.EmitStatus("thinking")
	time.Sleep(150 * time.Millisecond)

	events := sink.events()
	if len(events) == 0 {
		t.Fatal("expected status event")
	}
	last := events[len(events)-1]
	if last.Kind != OutboundEventStatus {
		t.Errorf("kind = %q, want %q", last.Kind, OutboundEventStatus)
	}
	if last.Status != "thinking" {
		t.Errorf("status = %q, want %q", last.Status, "thinking")
	}

	// Duplicate should be suppressed
	sink.reset()
	e.EmitStatus("thinking")
	time.Sleep(50 * time.Millisecond)
	if len(sink.events()) != 0 {
		t.Error("duplicate status should be suppressed")
	}

	// Different status should emit
	e.EmitStatus("writing")
	time.Sleep(50 * time.Millisecond)
	if len(sink.events()) == 0 {
		t.Error("new status should emit")
	}
}

func TestIMEmitterEmitRoundSummary(t *testing.T) {
	mgr := NewManager()
	sink := &namedCaptureSink{name: "qq"}
	mgr.RegisterSink(sink)
	mgr.currentBindings["qq"] = &ChannelBinding{Adapter: "qq", ChannelID: "ch1"}

	e := NewIMEmitter(mgr, "en", "/tmp")
	e.EmitRoundSummary("completed", 5, 3, 2)
	time.Sleep(150 * time.Millisecond)

	events := sink.events()
	if len(events) == 0 {
		t.Fatal("expected round summary event")
	}
	last := events[len(events)-1]
	if last.Kind != OutboundEventText {
		t.Errorf("kind = %q, want %q", last.Kind, OutboundEventText)
	}
	if !strings.Contains(last.Text, "completed") {
		t.Errorf("text should contain 'completed': %q", last.Text)
	}
}

func TestIMEmitterEmitKnightReport(t *testing.T) {
	mgr := NewManager()
	sink := &namedCaptureSink{name: "qq"}
	mgr.RegisterSink(sink)
	mgr.currentBindings["qq"] = &ChannelBinding{Adapter: "qq", ChannelID: "ch1"}

	e := NewIMEmitter(mgr, "en", "/tmp")
	e.EmitKnightReport("daily scan: 3 issues found")
	time.Sleep(150 * time.Millisecond)

	events := sink.events()
	if len(events) == 0 {
		t.Fatal("expected knight report event")
	}
	last := events[len(events)-1]
	if !strings.Contains(last.Text, "daily scan") {
		t.Errorf("text = %q", last.Text)
	}

	// Empty report should not emit
	sink.reset()
	e.EmitKnightReport("")
	time.Sleep(50 * time.Millisecond)
	if len(sink.events()) != 0 {
		t.Error("empty knight report should not emit")
	}
}

func TestIMEmitterFormatAskUserPrompt(t *testing.T) {
	e := NewIMEmitter(NewManager(), "en", "/tmp")

	// Valid JSON with title
	result := e.FormatAskUserPrompt(`{"title":"Choose option","questions":[{"id":"q1","title":"Pick one","kind":"single","choices":[{"id":"a","label":"Option A"}]}]}`)
	if !strings.Contains(result, "Choose option") {
		t.Errorf("result = %q", result)
	}

	// Invalid JSON but has title-like structure
	result2 := e.FormatAskUserPrompt(`{"title":"My Title"}`)
	if !strings.Contains(result2, "My Title") {
		t.Errorf("result2 = %q", result2)
	}

	// Empty
	if s := e.FormatAskUserPrompt(""); s != "" {
		t.Errorf("empty should return empty: %q", s)
	}
}

// namedCaptureSink captures all sent events for test assertions.
type namedCaptureSink struct {
	name       string
	mu         sync.Mutex
	eventsList []OutboundEvent
}

func (s *namedCaptureSink) Name() string { return s.name }

func (s *namedCaptureSink) Send(_ context.Context, _ ChannelBinding, event OutboundEvent) error {
	s.mu.Lock()
	s.eventsList = append(s.eventsList, event)
	s.mu.Unlock()
	return nil
}

func (s *namedCaptureSink) events() []OutboundEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]OutboundEvent(nil), s.eventsList...)
}
func (s *namedCaptureSink) reset() {
	s.mu.Lock()
	s.eventsList = nil
	s.mu.Unlock()
}

var _ Sink = (*namedCaptureSink)(nil)

// #1299: final assistant replies went out to IM adapters unredacted -
// only tool results were masked. EmitText must redact secrets (this
// covers all adapter paths since everything funnels through EmitEvent).
func TestIMEmitterEmitTextRedactsSecrets(t *testing.T) {
	mgr := NewManager()
	sink := &namedCaptureSink{name: "qq"}
	mgr.RegisterSink(sink)
	mgr.currentBindings["qq"] = &ChannelBinding{Adapter: "qq", ChannelID: "ch1"}

	e := NewIMEmitter(mgr, "en", "/tmp")
	_ = e.EmitText("the key is sk-ant-api03-AbCdEf1234567890abcdef ok")

	time.Sleep(150 * time.Millisecond)

	events := sink.events()
	if len(events) == 0 {
		t.Fatal("expected at least one event")
	}
	got := events[len(events)-1].Text
	if strings.Contains(got, "sk-ant-api03-AbCdEf") {
		t.Errorf("secret leaked to IM sink: %q", got)
	}
	if !strings.Contains(got, "the key is") {
		t.Errorf("non-secret text mangled: %q", got)
	}
}

// #1333: EmitUserTextExcept enqueues directly and bypassed the EmitEvent
// choke point - a secret the user pasted on one channel was forwarded
// verbatim to other bound adapters. Must go through redactOutbound.
func TestIMEmitterEmitUserTextExceptRedactsSecrets(t *testing.T) {
	mgr := NewManager()
	sink := &namedCaptureSink{name: "qq"}
	mgr.RegisterSink(sink)
	mgr.currentBindings["qq"] = &ChannelBinding{Adapter: "qq", ChannelID: "ch1"}

	e := NewIMEmitter(mgr, "en", "/tmp")
	e.EmitUserTextExcept("my token sk-ant-api03-AbCdEf1234567890abcdef use it", "telegram")

	time.Sleep(150 * time.Millisecond)

	events := sink.events()
	if len(events) == 0 {
		t.Fatal("expected at least one echo event")
	}
	got := events[len(events)-1].Text
	if strings.Contains(got, "sk-ant-api03-AbCdEf") {
		t.Errorf("secret leaked via user echo to other adapters: %q", got)
	}
	if !strings.Contains(got, "my token") {
		t.Errorf("non-secret echo text mangled: %q", got)
	}
}

// #1333: EmitAskUserInteractive sends cardText/fallbackText via manager
// paths that bypass EmitEvent. Question text quoting a secret (e.g. after
// read_file on .env) must be redacted before leaving the machine.
func TestIMEmitterAskUserInteractiveRedactsSecrets(t *testing.T) {
	mgr := NewManager()
	sink := &namedCaptureSink{name: "qq"}
	mgr.RegisterSink(sink)
	mgr.currentBindings["qq"] = &ChannelBinding{Adapter: "qq", ChannelID: "ch1"}

	e := NewIMEmitter(mgr, "en", "/tmp")
	q := toolpkg.AskUserQuestion{
		ID:    "q1",
		Title: "Confirm key sk-ant-api03-AbCdEf1234567890abcdef",
		Choices: []toolpkg.AskUserChoice{
			{Label: "Yes"}, {Label: "No"},
		},
	}
	fallback := "Confirm key sk-ant-api03-ZzZzZz0987654321zyxwvu yes?"
	e.EmitAskUserInteractive(q.Title, q, fallback)

	time.Sleep(150 * time.Millisecond)

	events := sink.events()
	if len(events) == 0 {
		t.Fatal("expected fallback event on non-interactive sink")
	}
	got := events[len(events)-1].Text
	if strings.Contains(got, "sk-ant-api03-ZzZzZz") {
		t.Errorf("secret leaked via ask_user fallback: %q", got)
	}
	if !strings.Contains(got, "Confirm key") {
		t.Errorf("non-secret fallback text mangled: %q", got)
	}
	// The helper itself must sanitize title text too.
	if out := e.redactOutbound(q.Title); strings.Contains(out, "sk-ant-api03-AbCdEf") {
		t.Errorf("redactOutbound left secret in ask_user title: %q", out)
	}
}
