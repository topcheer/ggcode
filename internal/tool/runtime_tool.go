package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// RuntimeStatusProvider provides runtime status information to the runtime tool.
// Each host (TUI, daemon, desktop) implements this to expose its current state.
type RuntimeStatusProvider interface {
	RuntimeSessionID() string
	RuntimePermissionMode() string
	RuntimeVendor() string
	RuntimeEndpoint() string
	RuntimeModel() string
	RuntimeLanguage() string
	RuntimeIMAdapters() []RuntimeIMAdapterInfo
	RuntimeMobile() RuntimeMobileInfo
}

// RuntimeIMAdapterInfo describes one IM adapter's status.
type RuntimeIMAdapterInfo struct {
	Name     string `json:"name"`
	Platform string `json:"platform"`
	Online   bool   `json:"online"`
	Muted    bool   `json:"muted"`
	Channel  string `json:"channel,omitempty"`
}

// RuntimeMobileInfo describes the mobile tunnel connection status.
type RuntimeMobileInfo struct {
	Connected   bool   `json:"connected"`
	RelayURL    string `json:"relay_url,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	ConnectCode string `json:"connect_code,omitempty"`
}

// RuntimeTool lets the LLM query runtime status: session ID, permission mode,
// IM adapters, mobile connection, and provider configuration.
type RuntimeTool struct {
	Provider RuntimeStatusProvider
}

func (t RuntimeTool) Name() string { return "runtime" }

func (t RuntimeTool) Description() string {
	return "Query runtime status information about the current ggcode instance. " +
		"Returns session ID, permission mode, provider (vendor/endpoint/model), language, " +
		"IM adapter status (platform, online, muted, channel), and mobile tunnel connection status.\n\n" +
		"Use this to understand the current runtime environment, such as:\n" +
		"- Which session you are running in\n" +
		"- What permission mode is active (supervised, plan, auto, bypass, autopilot)\n" +
		"- Whether IM adapters are connected and which channels are bound\n" +
		"- Whether a mobile device is connected via tunnel\n" +
		"- The current LLM provider and model\n\n" +
		"This tool is read-only and always allowed in every permission mode."
}

func (t RuntimeTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {},
		"additionalProperties": false
	}`)
}

func (t RuntimeTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Provider == nil {
		return Result{IsError: true, Content: "Runtime status provider is not configured."}, nil
	}

	var sb fmt.Stringer
	sb = runtimeStatusFormatter{p: t.Provider}

	return Result{Content: sb.String()}, nil
}

type runtimeStatusFormatter struct {
	p RuntimeStatusProvider
}

func (f runtimeStatusFormatter) String() string {
	p := f.p
	var b []byte
	b = append(b, "Runtime Status:\n"...)

	b = append(b, fmt.Sprintf("  Session ID: %s\n", p.RuntimeSessionID())...)
	b = append(b, fmt.Sprintf("  Permission mode: %s\n", p.RuntimePermissionMode())...)
	b = append(b, fmt.Sprintf("  Vendor: %s\n", p.RuntimeVendor())...)
	b = append(b, fmt.Sprintf("  Endpoint: %s\n", p.RuntimeEndpoint())...)
	b = append(b, fmt.Sprintf("  Model: %s\n", p.RuntimeModel())...)
	b = append(b, fmt.Sprintf("  Language: %s\n", p.RuntimeLanguage())...)

	// IM adapters
	adapters := p.RuntimeIMAdapters()
	if len(adapters) == 0 {
		b = append(b, "  IM adapters: none configured\n"...)
	} else {
		b = append(b, fmt.Sprintf("  IM adapters (%d):\n", len(adapters))...)
		for _, a := range adapters {
			status := "offline"
			if a.Online {
				status = "online"
			}
			if a.Muted {
				status = "muted"
			}
			ch := a.Channel
			if ch == "" {
				ch = "(no channel bound)"
			}
			b = append(b, fmt.Sprintf("    - %s [%s]: %s, channel: %s\n", a.Name, a.Platform, status, ch)...)
		}
	}

	// Mobile
	mobile := p.RuntimeMobile()
	if mobile.Connected {
		b = append(b, "  Mobile: connected\n"...)
		if mobile.SessionID != "" {
			b = append(b, fmt.Sprintf("    Session: %s\n", mobile.SessionID)...)
		}
		if mobile.RelayURL != "" {
			b = append(b, fmt.Sprintf("    Relay: %s\n", mobile.RelayURL)...)
		}
		if mobile.ConnectCode != "" {
			b = append(b, fmt.Sprintf("    Connect code: %s\n", mobile.ConnectCode)...)
		}
	} else {
		b = append(b, "  Mobile: not connected\n"...)
	}

	b = append(b, fmt.Sprintf("  Timestamp: %s\n", time.Now().Format(time.RFC3339))...)

	return string(b)
}
