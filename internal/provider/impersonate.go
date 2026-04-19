package provider

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// ImpersonationPreset defines a known CLI tool identity that can be used to
// set the User-Agent and other HTTP headers when communicating with LLM APIs.
type ImpersonationPreset struct {
	ID             string            // e.g. "claude-cli", "codex-cli"
	DisplayName    string            // e.g. "Claude CLI"
	UATemplate     string            // User-Agent template; {version} is replaced at runtime
	ExtraHeaders   map[string]string // additional headers
	DefaultVersion string            // default version when no custom version is set
}

// impersonationState holds the global impersonation configuration.
var (
	impersonationMu     sync.RWMutex
	activeImpersonation *ImpersonationPreset
	activeVersion       string
	activeCustomHeaders map[string]string
)

// DefaultImpersonationPresets returns the ordered list of available presets.
func DefaultImpersonationPresets() []ImpersonationPreset {
	return []ImpersonationPreset{
		{
			ID:             "none",
			DisplayName:    "None (ggcode)",
			UATemplate:     "ggcode/{version}",
			DefaultVersion: "1.0",
		},
		{
			ID:             "claude-cli",
			DisplayName:    "Claude CLI",
			UATemplate:     "claude-cli/{version} (individual, cli)",
			DefaultVersion: "2.1.92",
			ExtraHeaders: map[string]string{
				"x-app":             "cli",
				"anthropic-version": "2023-06-01",
			},
		},
		{
			ID:             "codex-cli",
			DisplayName:    "Codex CLI",
			UATemplate:     "codex-cli/{version}",
			DefaultVersion: "1.0",
			ExtraHeaders: map[string]string{
				"anthropic-version": "2023-06-01",
			},
		},
		{
			ID:             "gemini-cli",
			DisplayName:    "Gemini CLI",
			UATemplate:     "gemini-cli/{version}",
			DefaultVersion: "0.23.0",
		},
		{
			ID:             "opencode",
			DisplayName:    "OpenCode",
			UATemplate:     "opencode/{version}",
			DefaultVersion: "1.3.2",
		},
		{
			ID:             "copilot",
			DisplayName:    "GitHub Copilot",
			UATemplate:     "GithubCopilot/{version}",
			DefaultVersion: "1.364.0",
			ExtraHeaders: map[string]string{
				"Openai-Intent": "conversation-edits",
				"x-initiator":   "agent",
			},
		},
		{
			ID:             "cline",
			DisplayName:    "Cline",
			UATemplate:     "Cline/{version}",
			DefaultVersion: "2.5.1",
		},
		{
			ID:             "aider",
			DisplayName:    "Aider",
			UATemplate:     "aider/{version}",
			DefaultVersion: "0.85.0",
			ExtraHeaders: map[string]string{
				"Editor-Version":         "aider/{version}",
				"Copilot-Integration-Id": "vscode-chat",
			},
		},
		{
			ID:             "cursor",
			DisplayName:    "Cursor",
			UATemplate:     "cursor/{version}",
			DefaultVersion: "1.5.9",
		},
		{
			ID:             "roo-code",
			DisplayName:    "Roo Code",
			UATemplate:     "roo-cline/{version}",
			DefaultVersion: "3.0",
		},
		{
			ID:             "kilocode",
			DisplayName:    "KiloCode",
			UATemplate:     "kilocode/{version}",
			DefaultVersion: "0.16.0",
		},
		{
			ID:             "openclaw",
			DisplayName:    "OpenClaw",
			UATemplate:     "openclaw/{version}",
			DefaultVersion: "2026.4.14",
		},
	}
}

// FindPresetByID looks up a preset by its ID. Returns nil if not found.
func FindPresetByID(id string) *ImpersonationPreset {
	for _, p := range DefaultImpersonationPresets() {
		if p.ID == id {
			return &p
		}
	}
	return nil
}

// SetActiveImpersonation configures the global impersonation state.
// Pass nil preset to clear impersonation.
func SetActiveImpersonation(preset *ImpersonationPreset, version string, customHeaders map[string]string) {
	impersonationMu.Lock()
	defer impersonationMu.Unlock()
	activeImpersonation = preset
	activeVersion = version
	activeCustomHeaders = customHeaders
}

// GetActiveImpersonation returns the current impersonation state.
func GetActiveImpersonation() (preset *ImpersonationPreset, version string, customHeaders map[string]string) {
	impersonationMu.RLock()
	defer impersonationMu.RUnlock()
	return activeImpersonation, activeVersion, activeCustomHeaders
}

// ResolveImpersonationHeaders builds the final http.Header set from the current
// impersonation state. Priority: custom headers > preset extra headers.
// Returns a new http.Header each time.
func ResolveImpersonationHeaders() http.Header {
	impersonationMu.RLock()
	preset := activeImpersonation
	version := activeVersion
	custom := activeCustomHeaders
	impersonationMu.RUnlock()

	if preset == nil {
		return nil
	}

	h := make(http.Header, 4)

	// Build User-Agent from template
	v := strings.TrimSpace(version)
	if v == "" {
		v = preset.DefaultVersion
	}
	ua := strings.ReplaceAll(preset.UATemplate, "{version}", v)
	h.Set("User-Agent", ua)

	// Apply preset extra headers
	for k, v := range preset.ExtraHeaders {
		val := strings.ReplaceAll(v, "{version}", strings.TrimSpace(version))
		h.Set(k, val)
	}

	// Apply custom headers (override preset values)
	for k, v := range custom {
		h.Set(k, v)
	}

	return h
}

// DefaultHeadersForProtocol returns the default headers for a given protocol
// when no impersonation is active. These match the original hardcoded behavior.
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
		// Copilot provider manages its own headers internally
		h.Set("User-Agent", "ggcode")
	default:
		h.Set("User-Agent", fmt.Sprintf("claude-cli/%s (individual, cli)", claudeCLIVersion))
		h.Set("x-app", "cli")
		h.Set("anthropic-version", "2023-06-01")
	}
	return h
}

// BuildHeadersForProvider returns the headers to use for a given protocol.
// If impersonation is active, returns impersonation headers.
// Otherwise returns protocol-specific defaults.
func BuildHeadersForProvider(protocol string) http.Header {
	if h := ResolveImpersonationHeaders(); h != nil {
		return h
	}
	return DefaultHeadersForProtocol(protocol)
}

// HeaderMutable is implemented by providers that support runtime header updates.
type HeaderMutable interface {
	UpdateRuntimeHeaders(headers http.Header)
}
