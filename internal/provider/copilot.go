package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/sashabaranov/go-openai"
	"github.com/topcheer/ggcode/internal/util"
)

type CopilotProvider struct {
	*OpenAIProvider
}

func NewCopilotProvider(apiKey, model string, maxTokens int, baseURL string) *CopilotProvider {
	config := openai.DefaultConfig(apiKey)
	config.BaseURL = strings.TrimSpace(baseURL)
	baseTransport := newProviderHTTPTransport()
	config.HTTPClient = &http.Client{
		Transport: &copilotHeaderRoundTripper{
			base:  baseTransport,
			token: strings.TrimSpace(apiKey),
		},
	}
	return &CopilotProvider{
		OpenAIProvider: NewOpenAIProviderWithConfig(config, apiKey, model, maxTokens, "github-copilot"),
	}
}

type copilotHeaderRoundTripper struct {
	base           http.RoundTripper
	token          string
	uaMu           sync.RWMutex
	impersonatedUA string // when non-empty, overrides the default "ggcode" UA
}

func (rt *copilotHeaderRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	base := rt.base
	if base == nil {
		base = util.WrapTransport(nil)
	}
	clone := req.Clone(req.Context())
	if req.Body != nil {
		body, err := util.ReadAll(req.Body, util.ReadLimitAPI)
		if err != nil {
			return nil, err
		}
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(body))
		clone.Body = io.NopCloser(bytes.NewReader(body))

		isAgent, isVision := inspectCopilotRequest(body)
		if isAgent {
			clone.Header.Set("x-initiator", "agent")
		} else {
			clone.Header.Set("x-initiator", "user")
		}
		if isVision {
			clone.Header.Set("Copilot-Vision-Request", "true")
		}
	}
	if strings.TrimSpace(rt.token) != "" {
		clone.Header.Set("Authorization", "Bearer "+strings.TrimSpace(rt.token))
	}
	clone.Header.Set("Openai-Intent", "conversation-edits")
	rt.uaMu.RLock()
	ua := rt.impersonatedUA
	rt.uaMu.RUnlock()
	if ua == "" {
		ua = "ggcode"
	}
	clone.Header.Set("User-Agent", ua)
	return base.RoundTrip(clone)
}

type copilotRequestEnvelope struct {
	Messages []copilotRequestMessage `json:"messages"`
}

type copilotRequestMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type copilotContentPart struct {
	Type string `json:"type"`
}

func inspectCopilotRequest(body []byte) (isAgent bool, isVision bool) {
	var envelope copilotRequestEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return false, false
	}
	if len(envelope.Messages) > 0 {
		last := envelope.Messages[len(envelope.Messages)-1]
		isAgent = strings.TrimSpace(strings.ToLower(last.Role)) != "user"
	}
	for _, msg := range envelope.Messages {
		var parts []copilotContentPart
		if err := json.Unmarshal(msg.Content, &parts); err == nil {
			for _, part := range parts {
				if part.Type == "image_url" {
					isVision = true
					return isAgent, true
				}
			}
		}
	}
	return isAgent, false
}

func (p *CopilotProvider) Name() string {
	return "github-copilot"
}

// SetImpersonatedUA sets the impersonated User-Agent for the copilot transport.
func (p *CopilotProvider) SetImpersonatedUA(ua string) {
	if p.OpenAIProvider == nil || p.OpenAIProvider.transport == nil {
		return
	}
	// The transport chain is: headerInjectingTransport -> copilotHeaderRoundTripper -> DefaultTransport
	if copilotRT, ok := p.OpenAIProvider.transport.base.(*copilotHeaderRoundTripper); ok {
		copilotRT.uaMu.Lock()
		copilotRT.impersonatedUA = ua
		copilotRT.uaMu.Unlock()
	}
}

func validateCopilotResolved(baseURL, apiKey string) error {
	if strings.TrimSpace(baseURL) == "" {
		return fmt.Errorf("github copilot base URL is not configured")
	}
	if strings.TrimSpace(apiKey) == "" {
		return fmt.Errorf("github copilot is not logged in")
	}
	return nil
}
