//go:build goolm

package wailskit

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// --- displayReasoningEffort ---

func TestDisplayReasoningEffort_EmptyReturnsAuto(t *testing.T) {
	if got := displayReasoningEffort(""); got != "auto" {
		t.Fatalf("expected 'auto', got %q", got)
	}
}

func TestDisplayReasoningEffort_WhitespaceReturnsAuto(t *testing.T) {
	if got := displayReasoningEffort("  "); got != "auto" {
		t.Fatalf("expected 'auto' for whitespace, got %q", got)
	}
}

func TestDisplayReasoningEffort_NonEmptyReturnsValue(t *testing.T) {
	if got := displayReasoningEffort("high"); got != "high" {
		t.Fatalf("expected 'high', got %q", got)
	}
}

func TestDisplayReasoningEffort_PreservesWhitespaceAroundValue(t *testing.T) {
	if got := displayReasoningEffort("  low  "); got != "low" {
		t.Fatalf("expected 'low' (trimmed), got %q", got)
	}
}

// --- nextReasoningEffort ---

func TestNextReasoningEffort_Cycle(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", "low"},
		{"low", "medium"},
		{"medium", "high"},
		{"high", ""},       // wraps back to empty
		{"LOW", "medium"},  // case-insensitive
		{"unknown", "low"}, // unknown → first non-empty
	}
	for _, tc := range tests {
		got := nextReasoningEffort(tc.input)
		if got != tc.expected {
			t.Errorf("nextReasoningEffort(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

// --- maskAPIKey ---

func TestMaskAPIKey_EmptyReturnsEmpty(t *testing.T) {
	if got := maskAPIKey(""); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestMaskAPIKey_ShortKeyReturnsStars(t *testing.T) {
	if got := maskAPIKey("abc"); got != "***" {
		t.Fatalf("expected ***, got %q", got)
	}
}

func TestMaskAPIKey_ExactBoundary8CharsReturnsStars(t *testing.T) {
	if got := maskAPIKey("12345678"); got != "***" {
		t.Fatalf("expected *** for 8-char key, got %q", got)
	}
}

func TestMaskAPIKey_LongKeyShowsStartEnd(t *testing.T) {
	key := "sk-abcdef1234567890xyz"
	got := maskAPIKey(key)
	if got != "sk-***xyz" {
		t.Fatalf("expected 'sk-***xyz', got %q", got)
	}
}

// --- resolveAPIKey ---

func TestResolveAPIKey_EndpointKeyTakesPriority(t *testing.T) {
	cfg := &config.Config{
		Vendors: map[string]config.VendorConfig{
			"test": {
				APIKey: "vendor-key",
				Endpoints: map[string]config.EndpointConfig{
					"ep1": {APIKey: "endpoint-key"},
				},
			},
		},
	}
	got := resolveAPIKey(cfg, "test", "ep1")
	if got != "endpoint-key" {
		t.Fatalf("expected endpoint-key, got %q", got)
	}
}

func TestResolveAPIKey_FallsBackToVendorKey(t *testing.T) {
	cfg := &config.Config{
		Vendors: map[string]config.VendorConfig{
			"test": {
				APIKey: "vendor-key",
				Endpoints: map[string]config.EndpointConfig{
					"ep1": {}, // no endpoint key
				},
			},
		},
	}
	got := resolveAPIKey(cfg, "test", "ep1")
	if got != "vendor-key" {
		t.Fatalf("expected vendor-key, got %q", got)
	}
}

func TestResolveAPIKey_VendorNotFound(t *testing.T) {
	cfg := &config.Config{Vendors: map[string]config.VendorConfig{}}
	got := resolveAPIKey(cfg, "nonexistent", "ep1")
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestResolveAPIKey_EndpointNotFound(t *testing.T) {
	cfg := &config.Config{
		Vendors: map[string]config.VendorConfig{
			"test": {Endpoints: map[string]config.EndpointConfig{}},
		},
	}
	got := resolveAPIKey(cfg, "test", "nonexistent")
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

// --- parseA2ATimeout ---

func TestParseA2ATimeout_EmptyString(t *testing.T) {
	if got := parseA2ATimeout(""); got != 5*time.Minute {
		t.Fatalf("expected 5m default, got %v", got)
	}
}

func TestParseA2ATimeout_InvalidString(t *testing.T) {
	if got := parseA2ATimeout("not-a-duration"); got != 5*time.Minute {
		t.Fatalf("expected 5m fallback, got %v", got)
	}
}

func TestParseA2ATimeout_ValidSeconds(t *testing.T) {
	if got := parseA2ATimeout("30s"); got != 30*time.Second {
		t.Fatalf("expected 30s, got %v", got)
	}
}

func TestParseA2ATimeout_ValidMinutes(t *testing.T) {
	if got := parseA2ATimeout("10m"); got != 10*time.Minute {
		t.Fatalf("expected 10m, got %v", got)
	}
}

// --- tunnelSubagentTextID / tunnelSubagentReasoningID ---

func TestTunnelSubagentTextID(t *testing.T) {
	got := tunnelSubagentTextID("agent-123")
	if got != "sa-agent-123" {
		t.Fatalf("expected 'sa-agent-123', got %q", got)
	}
}

func TestTunnelSubagentReasoningID(t *testing.T) {
	got := tunnelSubagentReasoningID("agent-456")
	if got != "sa-agent-456-reasoning" {
		t.Fatalf("expected 'sa-agent-456-reasoning', got %q", got)
	}
}
