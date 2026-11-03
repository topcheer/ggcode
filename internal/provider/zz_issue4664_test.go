package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIssue4664_NonStreamingCompletionOnlyBackfillsInput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		io.WriteString(w, `{"id":"r1","object":"chat.completion","choices":[{"index":0,`+
			`"message":{"role":"assistant","content":"summary text"},`+
			`"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":0,"completion_tokens":42,"total_tokens":42}}`)
	}))
	defer srv.Close()

	p := NewOpenAIProviderWithBaseURL("sk-test", "gpt-test", 1024, srv.URL)
	resp, err := p.Chat(context.Background(), []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("summarize this conversation which is long enough to count tokens")}},
	}, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Usage.InputTokens == 0 {
		t.Fatal("completion-only usage must backfill InputTokens via CountTokens (#4664), got 0")
	}
	if resp.Usage.OutputTokens != 42 {
		t.Errorf("OutputTokens = %d, want 42 (relay-provided, must pass through)", resp.Usage.OutputTokens)
	}
	if resp.Usage.PromptTokensTotal == 0 {
		t.Fatal("PromptTokensTotal must be backfilled alongside InputTokens")
	}
}

func TestIssue4664_ToolCallOnlyResponseEstimationCountsTools(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		io.WriteString(w, `{"id":"r2","object":"chat.completion","choices":[{"index":0,`+
			`"message":{"role":"assistant","content":"",`+
			`"tool_calls":[{"id":"c1","type":"function","function":{`+
			`"name":"search_code","arguments":"{\"query\":\"worker pool deadlock\",\"scope\":\"internal/\"}"}}]},`+
			`"finish_reason":"tool_calls"}]}`)
	}))
	defer srv.Close()

	p := NewOpenAIProviderWithBaseURL("sk-test", "gpt-test", 1024, srv.URL)
	resp, err := p.Chat(context.Background(), []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("find the deadlock")}},
	}, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Usage.OutputTokens == 0 {
		t.Fatal("tool-call-only response must estimate OutputTokens from tool name+arguments (#4664)")
	}
}
