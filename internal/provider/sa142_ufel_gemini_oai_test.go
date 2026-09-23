package provider

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sashabaranov/go-openai"
	"google.golang.org/genai"
)

// ---- UserFacingErrorLang: full branch taxonomy -----------------------------

func TestSA142_UFEL_ServerAndTransportBranches(t *testing.T) {
	cases := []struct {
		name string
		err  error
		zh   string
	}{
		{"503", &sa142StatusErr{code: http.StatusServiceUnavailable}, "服务暂时不可用"},
		{"502", &sa142StatusErr{code: http.StatusBadGateway}, "服务暂时不可用"},
		{"504", &sa142StatusErr{code: http.StatusGatewayTimeout}, "服务暂时不可用"},
		{"generic 5xx", &sa142StatusErr{code: 512}, "服务端错误 (512)"},
		{"cancelled", context.Canceled, "请求已取消"},
		{"cancelled wrapped", fmt.Errorf("call: %w", context.Canceled), "请求已取消"},
		{"deadline", context.DeadlineExceeded, "请求超时"},
		{"net error", &net.DNSError{Err: "no such host", Name: "api.x.com"}, "网络连接失败"},
		{"serialization", errors.New("json: cannot unmarshal into anthropic.MessageParam during MarshalJSON"), "消息格式不兼容"},
		{"context window", errors.New("prompt too long: 90000 tokens > 8192 context length"), "对话内容过长"},
		{"token limit", errors.New("your token limit was exceeded"), "对话内容过长"},
		{"max_tokens overflow cue", errors.New("max_tokens exceeds the model maximum"), "对话内容过长"},
		{"bare max_tokens maps to blind spot (#783)", errors.New("invalid max_tokens: must be greater than 0"), fallbackErrMsgZh},
		{"400", &sa142StatusErr{code: http.StatusBadRequest}, "请求参数错误 (400)"},
		{"402", &sa142StatusErr{code: http.StatusPaymentRequired}, "余额不足 (402)"},
		{"insufficient credits string", errors.New("Provider returned error: insufficient credits"), "余额不足 (402)"},
		{"generic 418", &sa142StatusErr{code: http.StatusTeapot}, "请求失败 (418)"},
		{"connection refused", errors.New("dial tcp: connection refused"), "无法连接到 API 服务器"},
		{"finish length", errors.New("finish_reason=length"), "回复被截断"},
		{"content filter", errors.New("finish_reason=content_filter"), "内容过滤器"},
		{"network stream", errors.New("finish_reason=network_error"), "网络错误导致流式传输中断"},
		{"ctx window exceeded str", errors.New("context_window_exceeded"), "对话内容过长"},
		{"billing cycle string", errors.New("usage limit reached for this billing cycle"), "额度已用完或套餐已过期"},
		{"allocated quota string", errors.New("You have allocated quota exhausted"), "额度已用完或套餐已过期"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := UserFacingErrorLang(tc.err, "zh-CN"); !strings.Contains(got, tc.zh) {
				t.Fatalf("zh = %q, missing %q", got, tc.zh)
			}
			if got := UserFacingErrorLang(tc.err, "en"); got == "" {
				t.Fatal("en message must be non-empty")
			}
		})
	}
	// SDK prefix stripping keeps the recognizable part visible.
	if got := UserFacingErrorLang(errors.New("openai chat: boom"), "zh-CN"); !strings.Contains(got, "请求失败：boom") {
		t.Fatalf("prefix strip: %q", got)
	}
}

func TestSA142_FailureClassString(t *testing.T) {
	if got := FailureNone.String(); got != "" {
		t.Fatalf("FailureNone.String() = %q, want empty", got)
	}
	for _, c := range []FailureClass{FailureQuota, FailureRateLimit, FailureAuth, FailureNetwork} {
		if c.String() == "" {
			t.Fatalf("class %d must have a non-empty name", c)
		}
	}
	unknown := FailureClass(99)
	if got := unknown.String(); got != "" {
		t.Fatalf("unknown class = %q, want empty", got)
	}
}

// ---- Cascade construction edges ---------------------------------------------

func TestSA142_NewCascadeProviderEdges(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("empty chain must panic")
		}
	}()
	// Default description when empty.
	single := NewCascadeProvider([]Provider{&mockProvider{name: "only"}}, "")
	if single.Description() != "cascade(1)" {
		t.Fatalf("default description = %q", single.Description())
	}
	NewCascadeProvider(nil, "x")
}

// ---- Gemini paths -----------------------------------------------------------

func newSA142Gemini(t *testing.T, handler http.HandlerFunc) *GeminiProvider {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	p, err := NewGeminiProviderWithBaseURL("dummy", "gemini-sa142", 256, server.URL)
	if err != nil {
		t.Fatalf("NewGeminiProviderWithBaseURL: %v", err)
	}
	return p
}

func TestSA142_GeminiChatTextAndToolCall(t *testing.T) {
	var sawTool bool
	_ = sawTool
	p := newSA142Gemini(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "generateContent") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"candidates":[{"content":{"parts":[{"text":"hallo"}]},"finishReason":"STOP"}],`+
			`"usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":4,"cachedContentTokenCount":2}}`)
	})
	p.SetTemperature(0.3)
	p.SetTopP(0.8)
	p.SetMaxTokens(321)
	if p.Temperature() != 0.3 || p.TopP() != 0.8 {
		t.Fatalf("sampling accessors = %v/%v", p.Temperature(), p.TopP())
	}
	if got := p.ModelName(); got != "gemini-sa142" {
		t.Fatalf("ModelName = %q", got)
	}
	// Sampling override roundtrip.
	p.SetSamplingOverride(&SamplingOverride{MaxTokens: 9, Temperature: 0.1})
	if o := p.SamplingOverride(); o == nil || o.MaxTokens != 9 {
		t.Fatalf("SamplingOverride = %+v", p.SamplingOverride())
	}
	p.SetSamplingOverride(nil)

	// Effort setter normalizes case and rejects unknown levels.
	p.SetReasoningEffort("HIGH")
	if p.ReasoningEffort() != "high" {
		t.Fatalf("ReasoningEffort = %q", p.ReasoningEffort())
	}
	p.SetReasoningEffort("bogus")
	if p.ReasoningEffort() != "high" {
		t.Fatal("unknown effort level must be ignored")
	}
	p.SetReasoningEffort("")

	// Session ID + runtime headers. UpdateRuntimeHeaders REPLACES the
	// injected header set, so it must run BEFORE SetSessionID or the
	// session header is clobbered.
	h := http.Header{}
	h.Set("X-G", "1")
	p.UpdateRuntimeHeaders(h)
	p.SetSessionID("sess-g")
	snap := p.transport.snapshotHeaders()
	if snap.Get("X-G") != "1" || snap.Get("GGCode-SessionID") != "sess-g" {
		t.Fatalf("gemini headers = %v", snap)
	}

	resp, err := p.Chat(context.Background(), []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}}, nil)
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if len(resp.Message.Content) != 1 || resp.Message.Content[0].Text != "hallo" {
		t.Fatalf("content = %+v", resp.Message.Content)
	}
	if resp.StopReason != "end_turn" {
		t.Fatalf("StopReason = %q, want end_turn", resp.StopReason)
	}
	if resp.Usage.InputTokens != 12 || resp.Usage.OutputTokens != 4 || resp.Usage.CacheRead != 2 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
	_ = sawTool
}

func TestSA142_GeminiChatToolUseAndFinishReasons(t *testing.T) {
	// Function-call response.
	p := newSA142Gemini(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"get_weather","args":{"city":"SF"},"id":"fc_1"}}]},"finishReason":"STOP"}]}`)
	})
	resp, err := p.Chat(context.Background(), []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "w?"}}}}, nil)
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	var toolBlk *ContentBlock
	for i := range resp.Message.Content {
		if resp.Message.Content[i].Type == "tool_use" {
			toolBlk = &resp.Message.Content[i]
		}
	}
	if toolBlk == nil || toolBlk.ToolName != "get_weather" {
		t.Fatalf("tool block missing: %+v", resp.Message.Content)
	}

	// finish_reason normalization + error mapping.
	if got := normalizeGeminiFinishReason("MAX_TOKENS"); got != "max_tokens" {
		t.Fatalf("MAX_TOKENS = %q", got)
	}
	if got := normalizeGeminiFinishReason("STOP"); got != "end_turn" {
		t.Fatalf("STOP = %q", got)
	}
	if got := normalizeGeminiFinishReason("SAFETY"); got != "refusal" {
		t.Fatalf("SAFETY = %q", got)
	}
	if got := normalizeGeminiFinishReason("OTHER_WEIRD"); got != "other_weird" {
		t.Fatalf("OTHER = %q", got)
	}
	if got := normalizeGeminiFinishReason(""); got != "" {
		t.Fatalf("empty = %q", got)
	}
	if geminiFinishReasonError("") != nil || geminiFinishReasonError("STOP") != nil {
		t.Fatal("normal finishes must not error")
	}
	for _, reason := range []genai.FinishReason{"MAX_TOKENS", "SAFETY", "RECITATION", "PROHIBITED_CONTENT", "BLOCKLIST", "MALFORMED_FUNCTION_CALL", "SUPER_STRANGE"} {
		if geminiFinishReasonError(reason) == nil {
			t.Fatalf("finish %s must error", reason)
		}
	}
}

func TestSA142_GeminiProbes(t *testing.T) {
	// probeModelsAPI: models endpoint returns the token limit.
	p := newSA142Gemini(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/models/") && r.Method == http.MethodGet {
			fmt.Fprint(w, `{"name":"models/gemini-sa142","inputTokenLimit":123456,"displayName":"SA142"}`)
			return
		}
		// generateContent (probeChat): always fails with 500.
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":{"code":500,"message":"boom"}}`)
	})
	if got := p.probeModelsAPI(context.Background(), "gemini-sa142"); got != 123456 {
		t.Fatalf("probeModelsAPI = %d, want 123456", got)
	}
	if err := p.probeChat(context.Background(), []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}}); err == nil {
		t.Fatal("probeChat must surface the 500")
	}
	// tryModelsAPI dispatch: gemini provider routes to probeModelsAPI; the
	// mock provider is skipped.
	if got := tryModelsAPI(context.Background(), p); got != 123456 {
		t.Fatalf("tryModelsAPI(gemini) = %d", got)
	}
	if got := tryModelsAPI(context.Background(), &mockProvider{name: "m"}); got != 0 {
		t.Fatalf("tryModelsAPI(mock) = %d, want 0", got)
	}
}

// sa142ProbeProvider implements Provider + probeChat for probe-phase tests.
type sa142ProbeProvider struct {
	name     string
	probeErr error
	probes   int
}

func (m *sa142ProbeProvider) Name() string { return m.name }
func (m *sa142ProbeProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResponse, error) {
	return nil, m.probeErr
}
func (m *sa142ProbeProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent)
	close(ch)
	return ch, nil
}
func (m *sa142ProbeProvider) CountTokens(ctx context.Context, messages []Message) (int, error) {
	return 1, nil
}
func (m *sa142ProbeProvider) probeChat(ctx context.Context, messages []Message) error {
	m.probes++
	return m.probeErr
}

func TestSA142_ProbePhases(t *testing.T) {
	// Simple probe parses the window straight out of the error.
	overflow := &sa142ProbeProvider{name: "p", probeErr: errors.New("request too large: maximum context length is 48000 tokens")}
	if got := trySimpleProbe(context.Background(), overflow); got != 48000 {
		t.Fatalf("trySimpleProbe = %d, want 48000", got)
	}
	// Context error without a parseable number: inconclusive (0), not abort.
	vague := &sa142ProbeProvider{name: "p", probeErr: errors.New("conversation context exceeded")}
	if got := trySimpleProbe(context.Background(), vague); got != 0 {
		t.Fatalf("vague context probe = %d, want 0", got)
	}
	// Auth error aborts (-1).
	auth := &sa142ProbeProvider{name: "p", probeErr: &openai.APIError{HTTPStatusCode: 401, Message: "invalid key"}}
	if got := trySimpleProbe(context.Background(), auth); got != -1 {
		t.Fatalf("auth probe = %d, want -1", got)
	}
	// 429 is inconclusive (falls through to tiers), not an abort (#1788).
	limited := &sa142ProbeProvider{name: "p", probeErr: &openai.APIError{HTTPStatusCode: 429, Message: "slow down"}}
	if got := trySimpleProbe(context.Background(), limited); got != 0 {
		t.Fatalf("429 probe = %d, want 0", got)
	}
	// Tier probe: success returns the tier itself.
	ok := &sa142ProbeProvider{name: "p"}
	if got := tryTierProbe(context.Background(), ok, 64000); got != 64000 {
		t.Fatalf("tier success = %d, want 64000", got)
	}
	// Tier probe: overflow error yields the parsed window.
	if got := tryTierProbe(context.Background(), overflow, 128000); got != 48000 {
		t.Fatalf("tier overflow = %d, want 48000", got)
	}
	// chatNoRetry with a provider lacking probeChat errors cleanly.
	if err := chatNoRetry(context.Background(), &mockProvider{name: "x"}, nil); err == nil {
		t.Fatal("chatNoRetry must reject providers without probeChat")
	}
	if got := parseContextWindowFromError(nil); got != 0 {
		t.Fatalf("parseContextWindowFromError(nil) = %d", got)
	}
	if got := parseContextWindowFromError(errors.New("nothing here")); got != 0 {
		t.Fatalf("parse plain = %d", got)
	}
}

// ---- OpenAI accessors -------------------------------------------------------

func TestSA142_OpenAIAccessors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"1","object":"chat.completion","created":0,"model":"m",`+
			`"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":3,"completion_tokens":2}}`)
	}))
	defer server.Close()

	p := NewOpenAIProviderWithBaseURL("k", "oai-m", 100, server.URL+"/v1")
	if got := p.ModelName(); got != "oai-m" {
		t.Fatalf("ModelName = %q", got)
	}
	p.SetMaxTokens(55)
	if p.maxTokens != 55 {
		t.Fatalf("SetMaxTokens = %d", p.maxTokens)
	}
	p.SetMaxTokens(0)
	if p.maxTokens != 55 {
		t.Fatal("SetMaxTokens(0) must be ignored")
	}
	p.SetTemperature(0.4)
	p.SetTopP(0.6)
	if p.Temperature() != 0.4 || p.TopP() != 0.6 {
		t.Fatalf("sampling = %v/%v", p.Temperature(), p.TopP())
	}
	p.SetReasoningEffort("medium")
	if p.ReasoningEffort() != "medium" {
		t.Fatalf("ReasoningEffort = %q", p.ReasoningEffort())
	}
	p.SetServiceTier("flex")
	if p.ServiceTier() != "flex" {
		t.Fatalf("ServiceTier = %q", p.ServiceTier())
	}
	p.SetToolChoice("required")
	if p.ToolChoice() != "required" {
		t.Fatalf("ToolChoice = %q", p.ToolChoice())
	}
	p.SetStrictTools(map[string]bool{"t": true})
	p.SetSamplingOverride(&SamplingOverride{MaxTokens: 7})
	if o := p.SamplingOverride(); o == nil || o.MaxTokens != 7 {
		t.Fatalf("SamplingOverride = %+v", p.SamplingOverride())
	}
	p.SetSessionID("sess-oai")
	h := http.Header{}
	h.Set("X-OAI", "1")
	p.UpdateRuntimeHeaders(h)
	if snap := p.transport.snapshotHeaders(); snap.Get("X-OAI") != "1" {
		t.Fatalf("openai headers = %v", snap)
	}
	_ = p.RateLimitInfo()
	// Happy-path Chat still works after all the configuration churn.
	resp, err := p.Chat(context.Background(), []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}}, nil)
	if err != nil || resp == nil {
		t.Fatalf("Chat = %v, %v", resp, err)
	}
	if resp.StopReason != "end_turn" {
		t.Fatalf("StopReason = %q", resp.StopReason)
	}
}
