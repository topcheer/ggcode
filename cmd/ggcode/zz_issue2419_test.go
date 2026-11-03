package main

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
