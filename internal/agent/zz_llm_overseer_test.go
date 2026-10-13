package agent

// zz_llm_overseer_test.go -- r489 companion tests: SICA semantic-level
// LLM overseer. Covers verdict parsing robustness, dormancy gates
// (no aux model / short run / cadence / budget), and end-to-end hint
// emission via a mock aux provider.

import (
	"context"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// llmOverseerMockProvider returns a canned text response per call.
type llmOverseerMockProvider struct {
	resp   string
	called int
}

func (m *llmOverseerMockProvider) Chat(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	m.called++
	return &provider.ChatResponse{
		Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: m.resp}}},
	}, nil
}

func (m *llmOverseerMockProvider) Name() string { return "llm-overseer-mock" }

func (m *llmOverseerMockProvider) ChatStream(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent, 1)
	close(ch)
	return ch, nil
}

func (m *llmOverseerMockProvider) CountTokens(ctx context.Context, messages []provider.Message) (int, error) {
	return 0, nil
}

func newLLMOverseerTestAgent(t *testing.T) *Agent {
	t.Helper()
	a := NewAgent(&llmOverseerMockProvider{}, tool.NewRegistry(), "sys", 5)
	resolved := &config.ResolvedEndpoint{
		VendorID:   "openai-compat",
		EndpointID: "e1",
		Model:      "main-model",
		Protocol:   "openai",
		BaseURL:    "http://127.0.0.1:1",
		APIKey:     "k",
	}
	a.SetAuxModel(resolved, "cheap-model")
	if a.auxResolved == nil {
		t.Fatal("SetAuxModel must arm aux routing for a distinct model")
	}
	// Minimal overseer so trajectory snapshot works.
	a.overseer = newOverseerState()
	return a
}

func TestLLMOverseerParseVerdict(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    string // expected verdict, "" = expect nil
		wantNil bool
	}{
		{"plain JSON", `{"verdict":"on_track","evidence":"e","suggestion":"s"}`, "on_track", false},
		{"fenced JSON", "```json\n{\"verdict\":\"wrong_approach\",\"evidence\":\"idx 4-9 grep loop\",\"suggestion\":\"switch to targeted read\"}\n```", "wrong_approach", false},
		{"prose wrapped", `Here is my judgment: {"verdict":"wrong_problem","evidence":"e","suggestion":"s"} hope this helps`, "wrong_problem", false},
		{"empty", "", "", true},
		{"no json", "all clear, looks good", "", true},
		{"missing verdict", `{"evidence":"e"}`, "", true},
		{"truncated", `{"verdict":"on_track"`, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := llmOverseerParseVerdict(tc.raw)
			if tc.wantNil {
				if v != nil {
					t.Fatalf("expected nil, got %+v", v)
				}
				return
			}
			if v == nil {
				t.Fatalf("expected verdict %q, got nil", tc.want)
			}
			if v.Verdict != tc.want {
				t.Fatalf("verdict = %q, want %q", v.Verdict, tc.want)
			}
		})
	}
}

func TestLLMOverseerDormancy(t *testing.T) {
	a := newLLMOverseerTestAgent(t)

	// Gate 1: no trajectory -> no call even when iteration qualifies.
	if got := a.llmOverseerCheck(llmOverseerMinIters); got != "" {
		t.Fatalf("empty trajectory must not trigger a call, got %q", got)
	}

	// Gate 2: short run.
	a.overseer.recordToolCall("read_file", false, "a.go", "ok")
	if got := a.llmOverseerCheck(5); got != "" {
		t.Fatalf("short run must be dormant, got %q", got)
	}
	if a.llmOverseer.callsDone != 0 {
		t.Fatalf("short run must not consume budget, callsDone=%d", a.llmOverseer.callsDone)
	}

	// Gate 3: no aux model at all.
	bare := NewAgent(&llmOverseerMockProvider{}, tool.NewRegistry(), "sys", 5)
	bare.overseer = newOverseerState()
	bare.overseer.recordToolCall("read_file", false, "a.go", "ok")
	if got := bare.llmOverseerCheck(llmOverseerMinIters); got != "" {
		t.Fatalf("unarmed agent must be dormant, got %q", got)
	}
	if bare.llmOverseer.callsDone != 0 {
		t.Fatalf("unarmed agent must not consume budget")
	}
}

func TestLLMOverseerWrongApproachEmitsHint(t *testing.T) {
	a := newLLMOverseerTestAgent(t)
	mock := &llmOverseerMockProvider{resp: `{"verdict":"wrong_approach","evidence":"entries 4-9 repeat the same grep","suggestion":"read the specific file instead"}`}
	a.auxProvider = mock // bypass provider.NewProvider with unreachable endpoint

	for i := 0; i < 10; i++ {
		a.overseer.recordToolCall("grep", false, "pat", "ok")
	}

	hint := a.llmOverseerCheck(llmOverseerMinIters)
	if hint == "" {
		t.Fatal("wrong_approach verdict must emit a hint")
	}
	if !strings.Contains(hint, "[llm-overseer]") || !strings.Contains(hint, "wrong_approach") {
		t.Fatalf("hint missing tag/verdict: %q", hint)
	}
	if !strings.Contains(hint, "read the specific file instead") {
		t.Fatalf("hint missing suggestion: %q", hint)
	}
	if mock.called != 1 {
		t.Fatalf("exactly one aux call expected, got %d", mock.called)
	}

	// Cadence gate: immediate re-check must not call again.
	if got := a.llmOverseerCheck(llmOverseerMinIters + 1); got != "" || mock.called != 1 {
		t.Fatalf("cadence gate violated: hint=%q calls=%d", got, mock.called)
	}
}

func TestLLMOverseerOnTrackSilent(t *testing.T) {
	a := newLLMOverseerTestAgent(t)
	mock := &llmOverseerMockProvider{resp: `{"verdict":"on_track","evidence":"steady progress","suggestion":""}`}
	a.auxProvider = mock
	a.overseer.recordToolCall("edit_file", false, "a.go", "ok")

	if got := a.llmOverseerCheck(llmOverseerMinIters); got != "" {
		t.Fatalf("on_track must stay silent, got %q", got)
	}
	if mock.called != 1 {
		t.Fatalf("one call expected, got %d", mock.called)
	}
}

func TestLLMOverseerBudgetAndFailureSilence(t *testing.T) {
	a := newLLMOverseerTestAgent(t)
	// Malformed reply -> silent "".
	mock := &llmOverseerMockProvider{resp: "not json at all"}
	a.auxProvider = mock
	a.overseer.recordToolCall("grep", false, "x", "ok")

	if got := a.llmOverseerCheck(llmOverseerMinIters); got != "" {
		t.Fatalf("malformed verdict must degrade silently, got %q", got)
	}

	// Budget: two calls consumed -> further due checks stay silent even
	// with a good verdict wired in.
	a.llmOverseer.lastIter = 0
	a.llmOverseer.callsDone = llmOverseerMaxCalls
	mock.resp = `{"verdict":"wrong_approach","evidence":"e","suggestion":"s"}`
	if got := a.llmOverseerCheck(llmOverseerMinIters); got != "" || mock.called != 1 {
		t.Fatalf("budget gate violated: hint=%q calls=%d", got, mock.called)
	}
}

func TestLLMOverseerSnapshotBounds(t *testing.T) {
	a := newLLMOverseerTestAgent(t)
	for i := 0; i < llmOverseerWindow+10; i++ {
		a.overseer.recordToolCall("grep", false, "p", "ok")
	}
	if got := len(a.snapshotTrajectory(llmOverseerWindow)); got != llmOverseerWindow {
		t.Fatalf("snapshot cap = %d, want %d", got, llmOverseerWindow)
	}
	if got := len(a.snapshotTrajectory(5)); got != 5 {
		t.Fatalf("small k = %d, want 5", got)
	}
	bare := NewAgent(&llmOverseerMockProvider{}, tool.NewRegistry(), "sys", 5)
	if got := bare.snapshotTrajectory(llmOverseerWindow); got != nil {
		t.Fatalf("nil overseer snapshot must be nil, got %d entries", len(got))
	}
}
