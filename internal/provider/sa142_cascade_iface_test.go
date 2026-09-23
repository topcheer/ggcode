package provider

import (
	"context"
	"testing"
	"time"
)

// sa142RichProvider implements the Provider core plus every optional
// capability interface that FallbackProvider forwards (ModelName,
// ReasoningEffort, TextVerbosity, ServiceTier, SessionID, ToolChoice,
// SamplingConfig, RateLimit, ClonableWithModel). It lets us assert that the
// cascade wrapper neither drops nor mis-routes optional capability calls.
type sa142RichProvider struct {
	name string

	model      string
	modelClone string // non-empty after CloneWithModel

	effort    string
	verbosity string
	tier      string
	sessionID string
	toolChoce string
	temp      float64
	topP      float64
	rl        RateLimitInfo
}

func (m *sa142RichProvider) Name() string { return m.name }
func (m *sa142RichProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResponse, error) {
	return &ChatResponse{Message: Message{Role: "assistant"}}, nil
}
func (m *sa142RichProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent)
	close(ch)
	return ch, nil
}
func (m *sa142RichProvider) CountTokens(ctx context.Context, messages []Message) (int, error) {
	return len(messages) * 7, nil
}

func (m *sa142RichProvider) ModelName() string {
	if m.modelClone != "" {
		return m.modelClone
	}
	return m.model
}
func (m *sa142RichProvider) SetReasoningEffort(e string)  { m.effort = e }
func (m *sa142RichProvider) ReasoningEffort() string      { return m.effort }
func (m *sa142RichProvider) SetTextVerbosity(v string)    { m.verbosity = v }
func (m *sa142RichProvider) TextVerbosity() string        { return m.verbosity }
func (m *sa142RichProvider) SetServiceTier(t string)      { m.tier = t }
func (m *sa142RichProvider) ServiceTier() string          { return m.tier }
func (m *sa142RichProvider) SetSessionID(s string)        { m.sessionID = s }
func (m *sa142RichProvider) SetToolChoice(c string)       { m.toolChoce = c }
func (m *sa142RichProvider) ToolChoice() string           { return m.toolChoce }
func (m *sa142RichProvider) SetTemperature(f float64)     { m.temp = f }
func (m *sa142RichProvider) Temperature() float64         { return m.temp }
func (m *sa142RichProvider) SetTopP(f float64)            { m.topP = f }
func (m *sa142RichProvider) TopP() float64                { return m.topP }
func (m *sa142RichProvider) RateLimitInfo() RateLimitInfo { return m.rl }

func (m *sa142RichProvider) CloneWithModel(model string) Provider {
	clone := *m
	clone.modelClone = model
	return &clone
}

// TestSA142_CascadeIfaceDelegation: getters forward to the ACTIVE provider
// and setters fan out to the whole chain.
func TestSA142_CascadeIfaceDelegation(t *testing.T) {
	l0 := &sa142RichProvider{name: "rich0", model: "model-a", effort: "high", verbosity: "medium", tier: "priority", toolChoce: "auto", temp: 0.5, topP: 0.9, rl: RateLimitInfo{RemainingRequests: 5}}
	l1 := &sa142RichProvider{name: "rich1", model: "model-b"}
	fp := NewCascadeProvider([]Provider{l0, l1}, "sa142-cascade")

	if got := fp.ModelName(); got != "model-a" {
		t.Fatalf("ModelName = %q, want model-a (active level 0)", got)
	}
	if got := fp.ReasoningEffort(); got != "high" {
		t.Fatalf("ReasoningEffort = %q, want high", got)
	}
	if got := fp.TextVerbosity(); got != "medium" {
		t.Fatalf("TextVerbosity = %q, want medium", got)
	}
	if got := fp.ServiceTier(); got != "priority" {
		t.Fatalf("ServiceTier = %q, want priority", got)
	}
	if got := fp.ToolChoice(); got != "auto" {
		t.Fatalf("ToolChoice = %q, want auto", got)
	}
	if got := fp.Temperature(); got != 0.5 {
		t.Fatalf("Temperature = %v, want 0.5", got)
	}
	if got := fp.TopP(); got != 0.9 {
		t.Fatalf("TopP = %v, want 0.9", got)
	}
	if got := fp.RateLimitInfo(); got.RemainingRequests != 5 {
		t.Fatalf("RateLimitInfo.RemainingRequests = %d, want 5", got.RemainingRequests)
	}
	if got := fp.Description(); got != "sa142-cascade" {
		t.Fatalf("Description = %q, want sa142-cascade", got)
	}
	s := fp.String()
	if s == "" || fp.Name() != "rich0" {
		t.Fatalf("String()/Name() broken: %q / %q", s, fp.Name())
	}
	n, err := fp.CountTokens(context.Background(), []Message{{Role: "user"}})
	if err != nil || n != 7 {
		t.Fatalf("CountTokens = %d, %v; want 7, nil", n, err)
	}

	// Setters fan out to BOTH chain members.
	fp.SetReasoningEffort("low")
	fp.SetTextVerbosity("short")
	fp.SetServiceTier("default")
	fp.SetSessionID("sess-42")
	fp.SetToolChoice("required")
	fp.SetTemperature(0.2)
	fp.SetTopP(0.1)
	for i, p := range []*sa142RichProvider{l0, l1} {
		if p.effort != "low" || p.verbosity != "short" || p.tier != "default" || p.sessionID != "sess-42" || p.toolChoce != "required" || p.temp != 0.2 || p.topP != 0.1 {
			t.Fatalf("level %d did not receive all forwarded setters: %+v", i, p)
		}
	}
	// Getter now reflects the still-active level 0.
	if got := fp.ReasoningEffort(); got != "low" {
		t.Fatalf("ReasoningEffort after set = %q, want low", got)
	}
}

// TestSA142_CascadeGettersNonCapable: when the active provider does not
// implement an optional interface, the wrapper degrades to zero values
// instead of panicking.
func TestSA142_CascadeGettersNonCapable(t *testing.T) {
	fp := NewCascadeProvider([]Provider{&mockProvider{name: "plain"}, &mockProvider{name: "plain2"}}, "sa142-plain")
	if got := fp.ModelName(); got != "" {
		t.Fatalf("ModelName = %q, want empty", got)
	}
	if got := fp.ReasoningEffort(); got != "" {
		t.Fatalf("ReasoningEffort = %q, want empty", got)
	}
	if got := fp.TextVerbosity(); got != "" {
		t.Fatalf("TextVerbosity = %q, want empty", got)
	}
	if got := fp.ServiceTier(); got != "" {
		t.Fatalf("ServiceTier = %q, want empty", got)
	}
	if got := fp.ToolChoice(); got != "" {
		t.Fatalf("ToolChoice = %q, want empty", got)
	}
	if got := fp.Temperature(); got != 0 {
		t.Fatalf("Temperature = %v, want 0", got)
	}
	if got := fp.TopP(); got != 0 {
		t.Fatalf("TopP = %v, want 0", got)
	}
	if got := fp.RateLimitInfo(); got != (RateLimitInfo{}) {
		t.Fatalf("RateLimitInfo = %+v, want zero", got)
	}
	// Setters must be no-ops, not panics, on non-capable chains.
	fp.SetReasoningEffort("high")
	fp.SetTextVerbosity("long")
	fp.SetServiceTier("batch")
	fp.SetSessionID("s")
	fp.SetToolChoice("none")
	fp.SetTemperature(1)
	fp.SetTopP(1)
}

// TestSA142_CascadeCloneWithModel: full-chain cloning preserves the
// description, notify callback, and active-level inheritance (#391/#372).
func TestSA142_CascadeCloneWithModel(t *testing.T) {
	l0 := &sa142RichProvider{name: "r0", model: "m0"}
	l1 := &sa142RichProvider{name: "r1", model: "m1"}
	fp := NewCascadeProvider([]Provider{l0, l1}, "sa142-clone")
	notified := make(chan struct{}, 1)
	fp.SetFailoverNotify(func(FailoverTrigger, error) { notified <- struct{}{} })

	// Advance to level 1 and give the clone a huge probe interval so the
	// inherited recovery prober never fires during the test.
	fp.activeIdx.Store(1)
	fp.probeInterval = time.Hour

	clone, ok := fp.CloneWithModel("strong-model").(*FallbackProvider)
	if !ok {
		t.Fatalf("CloneWithModel did not return *FallbackProvider")
	}
	defer clone.Reset() // stop the inherited recovery prober
	if !clone.HasFailedOver() {
		t.Fatal("clone must inherit the advanced active index")
	}
	if clone.Description() != "sa142-clone" {
		t.Fatalf("clone description = %q", clone.Description())
	}
	if clone.notifySnapshot() == nil {
		t.Fatal("clone lost the failover notify callback")
	}
	if got := clone.ModelName(); got != "strong-model" {
		t.Fatalf("clone ModelName = %q, want strong-model", got)
	}
	// Original wrapper must stay untouched.
	if fp.ModelName() != "m1" {
		t.Fatalf("original mutated by clone: ModelName = %q", fp.ModelName())
	}
	// Clone started at level 1, so the recovery prober must be running.
	clone.mu.RLock()
	running := clone.probeCancel != nil
	clone.mu.RUnlock()
	if !running {
		t.Fatal("clone did not inherit the recovery prober while active != 0")
	}
	// Cloning the ALREADY-cloned wrapper keeps the chain clonable.
	if _, ok := clone.CloneWithModel("x").(*FallbackProvider); !ok {
		t.Fatal("re-cloning a cascade clone lost the FallbackProvider type")
	}
}

// TestSA142_CascadeCloneWithModel_PartialClonable: a single non-clonable
// level must degrade to returning the SAME wrapper (#391), never a wrapper
// sharing un-cloned state.
func TestSA142_CascadeCloneWithModel_PartialClonable(t *testing.T) {
	fp := NewCascadeProvider([]Provider{&sa142RichProvider{name: "r"}, &mockProvider{name: "plain"}}, "sa142-partial")
	got := fp.CloneWithModel("any")
	if got != Provider(fp) {
		t.Fatal("partial clonable chain must return the original wrapper")
	}
}

// TestSA142_CloneProviderWithModel: the package-level helper clones only
// capable providers with a non-empty model override.
func TestSA142_CloneProviderWithModel(t *testing.T) {
	rich := &sa142RichProvider{name: "r", model: "m"}
	got := CloneProviderWithModel(rich, "new")
	if got == Provider(rich) {
		t.Fatal("clonable provider with model override must be cloned")
	}
	mp, ok := got.(ModelNameProvider)
	if !ok {
		t.Fatal("clone must implement ModelNameProvider")
	}
	if got := mp.ModelName(); got != "new" {
		t.Fatalf("clone model = %q, want new", got)
	}
	if got := CloneProviderWithModel(rich, ""); got != Provider(rich) {
		t.Fatal("empty model override must return the original")
	}
	plain := &mockProvider{name: "p"}
	if got := CloneProviderWithModel(plain, "x"); got != Provider(plain) {
		t.Fatal("non-clonable provider must be returned unchanged")
	}
}
