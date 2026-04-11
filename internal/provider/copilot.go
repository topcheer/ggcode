package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/sashabaranov/go-openai"
)

type CopilotProvider struct {
	*OpenAIProvider
}

func NewCopilotProvider(apiKey, model string, maxTokens int, baseURL string) *CopilotProvider {
	config := openai.DefaultConfig(apiKey)
	config.BaseURL = strings.TrimSpace(baseURL)
	baseTransport := http.DefaultTransport
	config.HTTPClient = &http.Client{
		Transport: &copilotHeaderRoundTripper{
			base:  baseTransport,
			token: strings.TrimSpace(apiKey),
		},
	}
	return &CopilotProvider{
		OpenAIProvider: NewOpenAIProviderWithConfig(config, model, maxTokens, "github-copilot"),
	}
}

type copilotHeaderRoundTripper struct {
	base  http.RoundTripper
	token string
}

func (rt *copilotHeaderRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	base := rt.base
	if base == nil {
		base = http.DefaultTransport
	}
	clone := req.Clone(req.Context())
	if req.Body != nil {
		body, err := io.ReadAll(req.Body)
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
	clone.Header.Set("User-Agent", "ggcode")
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

func validateCopilotResolved(baseURL, apiKey string) error {
	if strings.TrimSpace(baseURL) == "" {
		return fmt.Errorf("github copilot base URL is not configured")
	}
	if strings.TrimSpace(apiKey) == "" {
		return fmt.Errorf("github copilot is not logged in")
	}
	return nil
}
