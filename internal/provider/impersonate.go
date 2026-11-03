package provider

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/version"
)

type ImpersonationPreset struct {
	ID             string
	DisplayName    string
	UATemplate     string
	ExtraHeaders   map[string]string
	DefaultVersion string
}

var (
	impersonationMu     sync.RWMutex
	activeImpersonation *ImpersonationPreset
	activeVersion       string
	activeCustomHeaders map[string]string
)

func DefaultImpersonationPresets() []ImpersonationPreset {
	return []ImpersonationPreset{
		{
			ID:             "none",
			DisplayName:    "None (ggcode)",
			UATemplate:     "ggcode/{version}",
			DefaultVersion: version.Version,
		},
		{
			ID:             "claude-cli",
			DisplayName:    "Claude CLI",
			UATemplate:     "claude-cli/{version} (individual, cli)",
			DefaultVersion: "2.1.209",
			ExtraHeaders: map[string]string{
				"x-app":             "cli",
				"anthropic-version": "2023-06-01",
			},
		},
		{
			ID:             "codex-cli",
			DisplayName:    "Codex CLI",
			UATemplate:     "codex-cli/{version}",
			DefaultVersion: "0.144.4",
			ExtraHeaders: map[string]string{
				"anthropic-version": "2023-06-01",
			},
		},
		{
			ID:             "gemini-cli",
			DisplayName:    "Gemini CLI",
			UATemplate:     "gemini-cli/{version}",
			DefaultVersion: "0.50.0",
		},
		{
			ID:             "opencode",
			DisplayName:    "OpenCode",
			UATemplate:     "opencode/{version}",
			DefaultVersion: "1.17.20",
			ExtraHeaders: map[string]string{

				"HTTP-Referer": "https://opencode.ai/",
				"X-Title":      "opencode",
			},
		},
		{
			ID:             "copilot",
			DisplayName:    "GitHub Copilot",
			UATemplate:     "GithubCopilot/{version}",
			DefaultVersion: "1.388.0",
			ExtraHeaders: map[string]string{
				"Openai-Intent": "conversation-edits",
				"x-initiator":   "agent",
			},
		},
		{
			ID:             "cline",
			DisplayName:    "Cline",
			UATemplate:     "Cline/{version}",
			DefaultVersion: "4.0.8",
		},
		{
			ID:             "aider",
			DisplayName:    "Aider",
			UATemplate:     "aider/{version}",
			DefaultVersion: "0.86.2",
			ExtraHeaders: map[string]string{
				"Editor-Version":         "aider/{version}",
				"Copilot-Integration-Id": "vscode-chat",
			},
		},
		{
			ID:             "cursor",
			DisplayName:    "Cursor",
			UATemplate:     "cursor/{version}",
			DefaultVersion: "3.11.19",
		},
		{
			ID:             "roo-code",
			DisplayName:    "Roo Code",
			UATemplate:     "roo-cline/{version}",
			DefaultVersion: "3.54.0",
		},
		{
			ID:             "kilocode",
			DisplayName:    "KiloCode",
			UATemplate:     "kilocode/{version}",
			DefaultVersion: "7.4.7",
		},
		{
			ID:             "openclaw",
			DisplayName:    "OpenClaw",
			UATemplate:     "openclaw/{version}",
			DefaultVersion: "2026.7.1",
		},
	}
}

func FindPresetByID(id string) *ImpersonationPreset {
	for _, p := range DefaultImpersonationPresets() {
		if p.ID == id {
			return &p
		}
	}
	return nil
}

func SetActiveImpersonation(preset *ImpersonationPreset, version string, customHeaders map[string]string) {
	impersonationMu.Lock()
	defer impersonationMu.Unlock()
	activeImpersonation = preset
	activeVersion = version
	activeCustomHeaders = customHeaders
}

func GetActiveImpersonation() (preset *ImpersonationPreset, version string, customHeaders map[string]string) {
	impersonationMu.RLock()
	defer impersonationMu.RUnlock()
	return activeImpersonation, activeVersion, activeCustomHeaders
}

func ResolveImpersonationHeaders() http.Header {
	impersonationMu.RLock()
	preset := activeImpersonation
	version := activeVersion
	custom := activeCustomHeaders
	impersonationMu.RUnlock()

	if preset == nil {

		if len(custom) == 0 {
			return nil
		}
		h := make(http.Header, len(custom))
		for k, v := range custom {
			h.Set(k, v)
		}
		return h
	}

	h := make(http.Header, 4)

	v := strings.TrimSpace(version)
	if v == "" {
		v = preset.DefaultVersion
	}
	ua := strings.ReplaceAll(preset.UATemplate, "{version}", v)
	h.Set("User-Agent", ua)

	for k, hv := range preset.ExtraHeaders {

		val := strings.ReplaceAll(hv, "{version}", v)
		h.Set(k, val)
	}

	for k, v := range custom {
		h.Set(k, v)
	}

	return h
}

func DefaultHeadersForProtocol(protocol string) http.Header {
	h := make(http.Header, 4)
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "anthropic", "openai":
		h.Set("User-Agent", fmt.Sprintf("claude-cli/%s (individual, cli)", claudeCLIVersion))
		h.Set("x-app", "cli")
		h.Set("anthropic-version", "2023-06-01")
	case "gemini":
		h.Set("User-Agent", "gemini-cli/0.23.0")
	case "copilot":

		h.Set("User-Agent", "ggcode")
	default:
		h.Set("User-Agent", fmt.Sprintf("claude-cli/%s (individual, cli)", claudeCLIVersion))
		h.Set("x-app", "cli")
		h.Set("anthropic-version", "2023-06-01")
	}
	return h
}

func BuildHeadersForProvider(protocol string) http.Header {
	if h := ResolveImpersonationHeaders(); h != nil {
		return h
	}
	return DefaultHeadersForProtocol(protocol)
}

type HeaderMutable interface {
	UpdateRuntimeHeaders(headers http.Header)
}

type SessionIDSetter interface {
	SetSessionID(sessionID string)
}
