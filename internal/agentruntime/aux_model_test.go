package agentruntime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
)

func TestResolveAuxModel(t *testing.T) {
	// nil config is safe
	if got := ResolveAuxModel(nil); got != "" {
		t.Fatalf("ResolveAuxModel(nil) = %q, want empty", got)
	}
	// explicit config wins over vendor default
	cfg := &config.Config{Vendor: "xiaomi-mimo", AuxModel: "my-cheap-model"}
	if got := ResolveAuxModel(cfg); got != "my-cheap-model" {
		t.Fatalf("explicit aux_model: got %q", got)
	}
	// vendor small-model default kicks in (xiaomi-mimo ships MiMo-V2.5)
	cfg = &config.Config{Vendor: "xiaomi-mimo"}
	if got := ResolveAuxModel(cfg); got != "MiMo-V2.5" {
		t.Fatalf("vendor default: got %q, want MiMo-V2.5", got)
	}
	// vendors without a small-model default keep aux routing off
	cfg = &config.Config{Vendor: "no-such-vendor"}
	if got := ResolveAuxModel(cfg); got != "" {
		t.Fatalf("unknown vendor: got %q, want empty", got)
	}
	// whitespace-only config value is treated as unset
	cfg = &config.Config{Vendor: "xiaomi-mimo", AuxModel: "   "}
	if got := ResolveAuxModel(cfg); got != "MiMo-V2.5" {
		t.Fatalf("whitespace aux_model: got %q", got)
	}
}

func TestCleanLLMTitle(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"  Fix login timeout  ", "Fix login timeout"},
		{"\"Fix login timeout\"", "Fix login timeout"},
		{"Title: Fix login timeout", "Fix login timeout"},
		{"标题：修复登录超时", "修复登录超时"},
		{"Fix login timeout\nSome extra explanation line", "Fix login timeout"},
		{"ab", ""}, // too short
		{"", ""},
	}
	for _, c := range cases {
		if got := CleanLLMTitle(c.in); got != c.want {
			t.Errorf("CleanLLMTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// long output is capped at the same 60-rune budget as heuristics
	// (truncateTitle appends a trailing ellipsis, so 61 runes is the cap)
	long := strings.Repeat("字", 100)
	got := CleanLLMTitle(long)
	if got == "" || len([]rune(got)) > 61 {
		t.Errorf("long title not capped: %d runes", len([]rune(got)))
	}
	// punctuation-only output is rejected
	if got := CleanLLMTitle("..."); got != "" {
		t.Errorf("punctuation-only output should be rejected, got %q", got)
	}
}

func TestNeedsLLMTitleUpgrade(t *testing.T) {
	// empty/placeholder titles upgrade
	if !NeedsLLMTitleUpgrade("", "Fix the login bug") {
		t.Error("empty title should upgrade")
	}
	if !NeedsLLMTitleUpgrade("New session", "Fix the login bug") {
		t.Error("placeholder should upgrade")
	}
	// generic titles upgrade
	if !NeedsLLMTitleUpgrade("hi", "hi") {
		t.Error("generic title should upgrade")
	}
	// naive store truncation is machine-set and may upgrade
	first := "Fix the authentication bypass in the OAuth2 callback handler under heavy load"
	naive := first
	if len([]rune(naive)) > 60 {
		naive = string([]rune(naive)[:57]) + "..."
	}
	if !NeedsLLMTitleUpgrade(naive, first) {
		t.Errorf("naive truncation %q should upgrade", naive)
	}
	// user-set titles never upgrade
	if NeedsLLMTitleUpgrade("My Custom Title", first) {
		t.Error("user title must not upgrade")
	}
	// empty first message with existing non-generic title: no upgrade
	if NeedsLLMTitleUpgrade("Some Title", "") {
		t.Error("empty first message should not upgrade titled session")
	}
}

type fakeAuxProvider struct {
	err      error
	text     string
	override *provider.SamplingOverride
	maxSeen  int // highest MaxTokens ever applied via SetSamplingOverride
}

func (f *fakeAuxProvider) Name() string { return "fake-aux" }
func (f *fakeAuxProvider) Chat(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &provider.ChatResponse{
		Message: provider.Message{Content: []provider.ContentBlock{provider.TextBlock(f.text)}},
	}, nil
}
func (f *fakeAuxProvider) ChatStream(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeAuxProvider) CountTokens(ctx context.Context, messages []provider.Message) (int, error) {
	return 0, errors.New("not implemented")
}
func (f *fakeAuxProvider) SetSamplingOverride(o *provider.SamplingOverride) {
	f.override = o
	if o != nil && o.MaxTokens > f.maxSeen {
		f.maxSeen = o.MaxTokens
	}
}
func (f *fakeAuxProvider) SamplingOverride() *provider.SamplingOverride { return f.override }

func TestGenerateLLMTitleSuccess(t *testing.T) {
	p := &fakeAuxProvider{text: "\"Fix login timeout\"\nextra line"}
	got, err := GenerateLLMTitle(context.Background(), p, "help me fix the login timeout")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "Fix login timeout" {
		t.Fatalf("got %q", got)
	}
	// output budget was applied via the sampling override (restored to
	// nil afterwards, so observe the peak)
	if p.maxSeen != 64 {
		t.Errorf("sampling override not applied: maxSeen=%d", p.maxSeen)
	}
}

func TestGenerateLLMTitleErrors(t *testing.T) {
	if _, err := GenerateLLMTitle(context.Background(), nil, "x"); err == nil {
		t.Error("nil provider should error")
	}
	p := &fakeAuxProvider{err: errors.New("relay down")}
	if _, err := GenerateLLMTitle(context.Background(), p, "x"); err == nil {
		t.Error("chat error should propagate")
	}
	p2 := &fakeAuxProvider{text: "..."}
	if _, err := GenerateLLMTitle(context.Background(), p2, "x"); err == nil {
		t.Error("empty output should error")
	}
}
