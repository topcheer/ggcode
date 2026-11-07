package main

import (
	"os"
	"strings"
	"testing"
)

// #2857 guarded the GLM vision heuristic: bare Contains(m,"v") misreports
// plain-text GLM bases (novita/glm-4.6, glm-4.6-preview, glm-4.6-voice) as
// vision-capable, so the generator used a strict version-suffix regex
// (glm-[0-9.]+v). #5190 removed the heuristic entirely: inferVisionSupport
// is now table-only (caps-table hit is authoritative incl. false; unknown
// models fall back to the gemini protocol default), so a false positive on
// non-vision GLM bases is impossible BY CONSTRUCTION - the template can no
// longer carry any keyword heuristics at all.
//
// This pin now guards the NEW shape: the emitted inferVisionSupport must be
// table-only, with no keyword-switch remnants. The behavioral side (v rows
// vision-capable, bases not) lives in internal/config's
// TestInferVisionSupportCapabilityTableOnly, which fires if any future
// template/commit diverges (#5190 Face-A drift guard).
func TestIssue2857GLMVisionTableOnlyTemplate(t *testing.T) {
	src, err := os.ReadFile("sync-model-caps.go")
	if err != nil {
		t.Fatalf("read generator: %v", err)
	}
	code := string(src)

	// Unchanged from the original pin: the bare-suffix bug must never return.
	if strings.Contains(code, `strings.Contains(m, "v")`) {
		t.Errorf("#2857 bare Contains(m,\"v\") still present in generator")
	}

	// #5190: no stale heuristic remnants may survive anywhere in the
	// generator (old regex var or old keyword switch cases).
	for _, remnant := range []string{"glmVisionSuffixRe", "kimi-vl", "kimi-2.5"} {
		if strings.Contains(code, remnant) {
			t.Errorf("#2857/#5190 stale vision-keyword remnant %q present in generator", remnant)
		}
	}

	// The emitted inferVisionSupport must be the table-only shape:
	// authoritative caps-table hit, then gemini protocol fallback.
	if !strings.Contains(code, "return cap.SupportsVision") {
		t.Errorf("#5190 table-only vision shape missing from generator (return cap.SupportsVision)")
	}
	if !strings.Contains(code, "strings.EqualFold(strings.TrimSpace(protocol), \"gemini\")") {
		t.Errorf("#5190 gemini protocol fallback missing from generator vision path")
	}

	// The vision function must not contain ANY keyword Contains heuristics:
	// scope the emitted function text and reject Contains(m, ...) entirely.
	start := strings.Index(code, "func inferVisionSupport")
	if start < 0 {
		t.Fatalf("#5190 inferVisionSupport not found in generator template")
	}
	end := strings.Index(code[start:], "\nfunc ")
	if end < 0 {
		end = len(code) - start
	}
	fnBody := code[start : start+end]
	if strings.Contains(fnBody, "strings.Contains(m,") {
		t.Errorf("#2857/#5190 vision template still carries keyword heuristics - table-only shape violated")
	}
}
