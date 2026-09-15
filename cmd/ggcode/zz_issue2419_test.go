package main

// #2419: the llm-probe --model flag was declared, threaded through
// runLLMProbe's signature, and documented ("Override model for all
// endpoints (skips ListModels)") - but never consumed. Every probe ran
// the endpoint's configured model while the user believed they were
// validating a different one. These pins hold the fix: the override is
// applied after resolution and before the NO_MODEL bail-out, and the
// leftover dead shells from the never-finished original implementation
// (empty if-block, orphan fetchFirstModel comment) stay gone.

import (
	"os"
	"strings"
	"testing"
)

func TestIssue2419ModelOverrideIsApplied(t *testing.T) {
	b, err := os.ReadFile("llm_probe.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "cfg.ResolveEndpoint(ref.vendor, ref.endpoint)")
	if i < 0 {
		t.Fatal("resolve call not found in probe loop")
	}
	// The override must sit between resolution and the NO_MODEL branch,
	// so it also rescues endpoints with no configured model.
	j := strings.Index(src[i:], "no model configured and no default for protocol")
	if j < 0 {
		t.Fatal("NO_MODEL branch not found")
	}
	span := src[i : i+j]
	if !strings.Contains(span, `if modelOverride != "" {`) ||
		!strings.Contains(span, "resolved.Model = modelOverride") {
		t.Fatal("#2419: --model must be applied to resolved.Model after resolution, before the NO_MODEL bail-out")
	}
}

func TestIssue2419DeadShellsStayGone(t *testing.T) {
	b, err := os.ReadFile("llm_probe.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if strings.Contains(src, "if resolved.Model == \"\" {\n\t\t}") {
		t.Fatal("#2419: the empty `if resolved.Model == \"\" {}` shell is back")
	}
	if strings.Contains(src, "fetchFirstModel") {
		t.Fatal("#2419: the orphan fetchFirstModel comment (function body was removed long ago) is back")
	}
}
