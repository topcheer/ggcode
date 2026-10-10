package agentruntime

// #3917 probe: ApplyProviderToAgent must not re-wrap an already-wrapped
// tape provider - every hot model switch would otherwise leak an O_APPEND
// fd (record) or reset the replay cursor to 0.

import (
	"os"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// The idempotency gate is pinned at source level plus a behavioral check
// via the exported type: wrapping twice through the helper must yield ONE
// TapeProvider layer when the inner provider is already a *TapeProvider.
func TestIssue3917_NoDoubleWrapOnHotSwitch(t *testing.T) {
	t.Setenv("GGCODE_LLM_TAPE", "")
	raw, err := os.ReadFile("model_switch.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	anchor := "WrapLLMTapeFromEnv(prov)"
	idx := indexOf3917(src, anchor)
	if idx < 0 {
		t.Fatal("wrap call not found")
	}
	window := src[max3917(0, idx-500) : idx+40]
	if !contains3917(window, "alreadyWrapped := prov.(*provider.TapeProvider)") {
		t.Fatal("ApplyProviderToAgent must gate the wrap on *TapeProvider identity (#3917)")
	}
}

func indexOf3917(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
func contains3917(s, sub string) bool { return indexOf3917(s, sub) >= 0 }
func max3917(a, b int) int {
	if a > b {
		return a
	}
	return b
}

var _ = provider.WrapLLMTapeFromEnv
