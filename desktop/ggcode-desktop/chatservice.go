package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// ChatService is the Wails-bound service that exposes chat functionality.
type ChatService struct {
	mu        sync.Mutex
	cfg       *config.Config
	agent     *agent.Agent
	app       *application.App
	eventName string // unique event name for this instance
}

// NewChatService creates a new ChatService.
func NewChatService() *ChatService {
	cfg := config.DefaultConfig()
	return &ChatService{
		cfg:       cfg,
		eventName: "ggcode:chat:stream",
	}
}

// SetApp stores the Wails app reference (called after initialization).
func (c *ChatService) SetApp(app *application.App) {
	c.app = app
}

// Initialize sets up the agent with tools and provider.
func (c *ChatService) Initialize() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.agent != nil {
		return nil
	}

	// Resolve the active provider from config.
	resolved, err := c.cfg.ResolveActiveEndpoint()
	if err != nil {
		return fmt.Errorf("resolve provider: %w", err)
	}

	prov, err := provider.NewProvider(resolved)
	if err != nil {
		return fmt.Errorf("create provider: %w", err)
	}

	// Create tool registry with default tools.
	registry := tool.NewRegistry()

	// Create agent.
	a := agent.NewAgent(prov, registry, "You are ggcode, an AI coding assistant. Be helpful and concise.", 10)
	probeKey := provider.MakeProbeKey(resolved.VendorID, resolved.BaseURL, resolved.Model)
	a.SetProbeKey(probeKey)

	c.agent = a
	log.Printf("[desktop] Agent initialized: vendor=%s model=%s", resolved.VendorID, resolved.Model)
	return nil
}

// SendMessage sends a user message and streams the response via Wails events.
func (c *ChatService) SendMessage(text string) error {
	if err := c.Initialize(); err != nil {
		return err
	}

	c.mu.Lock()
	a := c.agent
	c.mu.Unlock()

	if a == nil {
		return fmt.Errorf("agent not initialized")
	}

	go func() {
		ctx := context.Background()
		content := []provider.ContentBlock{{Type: "text", Text: text}}

		err := a.RunStreamWithContent(ctx, content, func(event provider.StreamEvent) {
			c.emitStreamEvent(event)
		})

		if err != nil {
			c.emitStreamEvent(provider.StreamEvent{
				Type:  provider.StreamEventError,
				Error: err,
			})
		}
	}()

	return nil
}

// GetVendors returns all configured vendor info.
func (c *ChatService) GetVendors() []VendorInfo {
	vendors := make([]VendorInfo, 0, len(c.cfg.Vendors))
	for name, vc := range c.cfg.Vendors {
		endpoints := make([]EndpointInfo, 0, len(vc.Endpoints))
		for epName, ep := range vc.Endpoints {
			endpoints = append(endpoints, EndpointInfo{
				Name:          epName,
				DisplayName:   ep.DisplayName,
				Models:        ep.Models,
				SelectedModel: ep.SelectedModel,
				Protocol:      ep.Protocol,
			})
		}
		vendors = append(vendors, VendorInfo{
			Name:        name,
			DisplayName: vc.DisplayName,
			Endpoints:   endpoints,
		})
	}
	return vendors
}

// GetActiveProvider returns the currently active vendor/endpoint/model.
func (c *ChatService) GetActiveProvider() ActiveProviderInfo {
	return ActiveProviderInfo{
		Vendor:   c.cfg.Vendor,
		Endpoint: c.cfg.Endpoint,
		Model:    c.cfg.Model,
	}
}

// SetActiveProvider changes the active vendor/endpoint/model and reinitializes.
func (c *ChatService) SetActiveProvider(vendor, endpoint, model string) error {
	c.cfg.Vendor = vendor
	c.cfg.Endpoint = endpoint
	if model != "" {
		c.cfg.Model = model
	}

	// Reset agent so next message reinitializes with new provider.
	c.mu.Lock()
	c.agent = nil
	c.mu.Unlock()

	return nil
}

// GetMessages returns the current conversation history.
func (c *ChatService) GetMessages() []map[string]interface{} {
	c.mu.Lock()
	a := c.agent
	c.mu.Unlock()

	if a == nil {
		return nil
	}

	msgs := a.Messages()
	result := make([]map[string]interface{}, 0, len(msgs))
	for _, m := range msgs {
		blocks := make([]map[string]interface{}, 0, len(m.Content))
		for _, b := range m.Content {
			block := map[string]interface{}{
				"type": b.Type,
			}
			if b.Text != "" {
				block["text"] = b.Text
			}
			if b.ToolID != "" {
				block["tool_id"] = b.ToolID
			}
			if b.ToolName != "" {
				block["tool_name"] = b.ToolName
			}
			if b.Input != nil {
				block["input"] = json.RawMessage(b.Input)
			}
			if b.Output != "" {
				block["output"] = b.Output
			}
			if b.IsError {
				block["is_error"] = true
			}
			blocks = append(blocks, block)
		}
		result = append(result, map[string]interface{}{
			"role":    m.Role,
			"content": blocks,
		})
	}
	return result
}

// emitStreamEvent pushes a stream event to the frontend via Wails Events.
func (c *ChatService) emitStreamEvent(event provider.StreamEvent) {
	if c.app == nil {
		return
	}

	payload := map[string]interface{}{
		"type": streamEventTypeString(event.Type),
	}

	switch event.Type {
	case provider.StreamEventText, provider.StreamEventReasoning:
		payload["text"] = event.Text
	case provider.StreamEventToolCallDone:
		payload["tool"] = map[string]interface{}{
			"id":    event.Tool.ID,
			"name":  event.Tool.Name,
			"input": json.RawMessage(event.Tool.Arguments),
		}
	case provider.StreamEventToolResult:
		payload["tool"] = map[string]interface{}{
			"id":   event.Tool.ID,
			"name": event.Tool.Name,
		}
		payload["result"] = event.Result
		payload["is_error"] = event.IsError
	case provider.StreamEventDone:
		if event.Usage != nil {
			payload["usage"] = map[string]interface{}{
				"input_tokens":  event.Usage.InputTokens,
				"output_tokens": event.Usage.OutputTokens,
			}
		}
	case provider.StreamEventError:
		if event.Error != nil {
			payload["error"] = event.Error.Error()
		}
	case provider.StreamEventSystem:
		payload["text"] = event.Text
	}

	data, _ := json.Marshal(payload)
	c.app.Event.Emit(c.eventName, string(data))
}

func streamEventTypeString(t provider.StreamEventType) string {
	switch t {
	case provider.StreamEventText:
		return "text"
	case provider.StreamEventToolCallChunk:
		return "tool_call_chunk"
	case provider.StreamEventToolCallDone:
		return "tool_call_done"
	case provider.StreamEventToolResult:
		return "tool_result"
	case provider.StreamEventDone:
		return "done"
	case provider.StreamEventError:
		return "error"
	case provider.StreamEventReasoning:
		return "reasoning"
	case provider.StreamEventSystem:
		return "system"
	default:
		return "unknown"
	}
}

// --- Data types for frontend ---

type VendorInfo struct {
	Name        string         `json:"name"`
	DisplayName string         `json:"displayName"`
	Endpoints   []EndpointInfo `json:"endpoints"`
}

type EndpointInfo struct {
	Name          string   `json:"name"`
	DisplayName   string   `json:"displayName"`
	Models        []string `json:"models"`
	SelectedModel string   `json:"selectedModel"`
	Protocol      string   `json:"protocol"`
}

type ActiveProviderInfo struct {
	Vendor   string `json:"vendor"`
	Endpoint string `json:"endpoint"`
	Model    string `json:"model"`
}
