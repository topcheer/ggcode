package provider

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/topcheer/ggcode/internal/debug"
)

func (t *headerInjectingTransport) setPromptCacheKey(key string) {
	t.mu.Lock()
	t.promptCacheKey = key
	t.mu.Unlock()
}

func (t *headerInjectingTransport) currentPromptCacheKey() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.promptCacheKey
}

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

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

func (p *OpenAIProvider) SetPromptCacheKey(key string) {
	if p.transport == nil {
		debug.Log("provider", "SetPromptCacheKey: no transport, ignoring key")
		return
	}
	p.transport.setPromptCacheKey(key)
}

func (p *OpenAIResponsesProvider) SetPromptCacheKey(key string) {
	p.promptCacheKey = key
}

func (p *OpenAIProvider) PromptCacheKey() string {
	if p.transport == nil {
		return ""
	}
	return p.transport.currentPromptCacheKey()
}

func (p *OpenAIResponsesProvider) PromptCacheKey() string { return p.promptCacheKey }
