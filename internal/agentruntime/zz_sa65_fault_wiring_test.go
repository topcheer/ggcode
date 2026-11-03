package agentruntime

import (
	"os"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestSA65FaultWrapAnchor(t *testing.T) {
	raw, err := os.ReadFile("model_switch.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	tapeIdx := strings.Index(src, "WrapLLMTapeFromEnv(prov)")
	faultIdx := strings.Index(src, "WrapLLMFaultFromEnv(prov)")
	if tapeIdx < 0 || faultIdx < 0 {
		t.Fatal("both tape and fault wrap calls must exist in ApplyProviderToAgent")
	}
	if faultIdx < tapeIdx {
		t.Fatal("fault wrap must come AFTER the tape wrap (fault outermost: agent receives perturbed stream, tape pristine)")
	}
	window := src[maxSA65(0, faultIdx-500) : faultIdx+40]
	if !strings.Contains(window, "alreadyFaulted := prov.(*provider.FaultProvider)") {
		t.Fatal("fault wrap must be gated on *FaultProvider identity (hot-switch idempotency)")
	}
}

func maxSA65(a, b int) int {
	if a > b {
		return a
	}
	return b
}

var _ = provider.WrapLLMFaultFromEnv
