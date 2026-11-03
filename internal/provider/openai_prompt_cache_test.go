package provider

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestInjectPromptCacheKeyChatBody(t *testing.T) {
	body := `{"model":"gpt-5","messages":[{"role":"user","content":"hi"}]}`
	req, _ := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/chat/completions",
		io.NopCloser(strings.NewReader(body)))
	injectPromptCacheKey(req, "ggcode-s1")
	got, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(got, &payload); err != nil {
		t.Fatalf("body no longer valid JSON: %v", err)
	}
	var key string
	_ = json.Unmarshal(payload["prompt_cache_key"], &key)
	if key != "ggcode-s1" {
		t.Errorf("prompt_cache_key = %q, want ggcode-s1", key)
	}
	if req.ContentLength != int64(len(got)) {
		t.Errorf("ContentLength = %d, want %d", req.ContentLength, len(got))
	}
}

func TestInjectPromptCacheKeyPreservesExistingAndNonJSON(t *testing.T) {

	req, _ := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/chat/completions",
		io.NopCloser(strings.NewReader(`{"prompt_cache_key":"user-key","model":"m"}`)))
	injectPromptCacheKey(req, "ggcode-s1")
	got, _ := io.ReadAll(req.Body)
	if !bytes.Contains(got, []byte(`"user-key"`)) {
		t.Errorf("existing prompt_cache_key overwritten: %s", got)
	}

	raw := "--boundary\r\nbinary\x00data"
	req2, _ := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/audio/transcriptions",
		io.NopCloser(strings.NewReader(raw)))
	injectPromptCacheKey(req2, "ggcode-s1")
	got2, _ := io.ReadAll(req2.Body)
	if string(got2) != raw {
		t.Errorf("non-JSON body mutated: %q", got2)
	}

	req3, _ := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/models", nil)
	injectPromptCacheKey(req3, "ggcode-s1")
}

func TestOpenAIProviderPromptCacheKeyRoundTrip(t *testing.T) {
	p := NewOpenAIProviderWithBaseURL("k", "gpt-5", 0, "http://localhost")
	if p.PromptCacheKey() != "" {
		t.Fatalf("default key should be empty")
	}
	p.SetPromptCacheKey("ggcode-s1")
	if got := p.PromptCacheKey(); got != "ggcode-s1" {
		t.Fatalf("transport round-trip = %q", got)
	}
	p.SetPromptCacheKey("")
	if got := p.PromptCacheKey(); got != "" {
		t.Fatalf("clear failed: %q", got)
	}
}

func TestOpenAIResponsesPromptCacheKeyInRequest(t *testing.T) {
	p := NewOpenAIResponsesProvider("k", "gpt-5", 0, "http://localhost")
	if p.PromptCacheKey() != "" {
		t.Fatalf("default key should be empty")
	}
	p.SetPromptCacheKey("ggcode-s1")
	if got := p.PromptCacheKey(); got != "ggcode-s1" {
		t.Fatalf("round-trip = %q", got)
	}

	req := responsesRequest{ServiceTier: "auto", PromptCacheKey: p.promptCacheKey}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"prompt_cache_key":"ggcode-s1"`)) {
		t.Errorf("request JSON missing prompt_cache_key: %s", b)
	}
	empty, _ := json.Marshal(responsesRequest{})
	if bytes.Contains(empty, []byte(`prompt_cache_key`)) {
		t.Errorf("omitempty failed: %s", empty)
	}
}
