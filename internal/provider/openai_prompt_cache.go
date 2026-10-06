package provider

// sa-231: OpenAI prompt-cache routing control. OpenAI's prompt caching
// (50% discount on >1k-token shared prefixes) only hits when repeated
// requests land on the same cache machine; the optional request-level
// prompt_cache_key gives the router a stable affinity hint. Without it,
// multi-turn agent traffic can be routed away from its own warm cache.
// Anthropic already had explicit cache_control breakpoints; this closes
// the OpenAI-side asymmetry. Key source: the agent session ID (stable
// per conversation, injected by agent.SetSessionID).

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/topcheer/ggcode/internal/debug"
)

// setPromptCacheKey updates the transport-level injection key (sa-231).
// Safe for concurrent use with RoundTrip.
func (t *headerInjectingTransport) setPromptCacheKey(key string) {
	t.mu.Lock()
	t.promptCacheKey = key
	t.mu.Unlock()
}

// currentPromptCacheKey returns the current injection key ("" = disabled).
func (t *headerInjectingTransport) currentPromptCacheKey() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.promptCacheKey
}

// injectPromptCacheKey rewrites a POST JSON request body to carry
// prompt_cache_key. Non-JSON bodies (multipart uploads) and bodies that
// already carry the field are left untouched; parse failures fall back to
// sending the original body so the hint never breaks a request.
func injectPromptCacheKey(req *http.Request, key string) {
	if req.Body == nil || req.Body == http.NoBody || req.Method != http.MethodPost {
		return
	}
	body, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		req.Body = io.NopCloser(bytes.NewReader(body))
		return
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil || payload == nil {
		req.Body = io.NopCloser(bytes.NewReader(body))
		return
	}
	if _, exists := payload["prompt_cache_key"]; exists {
		req.Body = io.NopCloser(bytes.NewReader(body))
		return
	}
	payload["prompt_cache_key"] = []byte(`"` + jsonEscape(key) + `"`)
	newBody, err := json.Marshal(payload)
	if err != nil {
		req.Body = io.NopCloser(bytes.NewReader(body))
		return
	}
	req.Body = io.NopCloser(bytes.NewReader(newBody))
	req.ContentLength = int64(len(newBody))
	debug.Log("provider", "prompt_cache_key injected (len=%d)", len(newBody))
}

// jsonEscape escapes a plain key for embedding in a JSON string literal.
func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

// SetPromptCacheKey arms prompt-cache routing affinity on the Chat
// Completions path. The go-openai SDK (v1.42) has no prompt_cache_key
// field, so the headerInjectingTransport injects it into the request
// body at send time. Empty clears the hint.
func (p *OpenAIProvider) SetPromptCacheKey(key string) {
	if p.transport == nil {
		debug.Log("provider", "SetPromptCacheKey: no transport, ignoring key")
		return
	}
	p.transport.setPromptCacheKey(key)
}

// SetPromptCacheKey arms prompt_cache_key on the Responses path. The
// responsesRequest is assembled in-package, so the key rides the body
// directly (no transport rewrite needed). Empty clears the hint.
func (p *OpenAIResponsesProvider) SetPromptCacheKey(key string) {
	p.promptCacheKey = key
}

// PromptCacheKey returns the currently configured cache-routing key
// ("" = none) for the Chat Completions path.
func (p *OpenAIProvider) PromptCacheKey() string {
	if p.transport == nil {
		return ""
	}
	return p.transport.currentPromptCacheKey()
}

// PromptCacheKey returns the currently configured cache-routing key
// ("" = none) for the Responses path.
func (p *OpenAIResponsesProvider) PromptCacheKey() string { return p.promptCacheKey }
