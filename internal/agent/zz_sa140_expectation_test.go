package agent

// sa-140 (TVAE, arXiv:2604.05477): structured expectation declaration,
// deterministic per-step verification, asymmetric immediate feedback.

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestParseExpectDeclarations(t *testing.T) {
	text := "Let me run the build.\nEXPECT: run_command exit=0\nand check the grep.\nEXPECT: >=1_match\n"
	decls := parseExpectDeclarations(text)
	if len(decls) != 2 {
		t.Fatalf("want 2 declarations, got %d: %+v", len(decls), decls)
	}
	if decls[0].toolName != "run_command" || decls[0].check != "exit=0" {
		t.Fatalf("decl0 = %+v", decls[0])
	}
	if decls[1].toolName != "" || decls[1].check != ">=1_match" {
		t.Fatalf("decl1 = %+v", decls[1])
	}
	if parseExpectDeclarations("EXPECT: not a real check") != nil {
		t.Fatal("non-whitelisted check must not parse")
	}
}

func TestEvaluateExpectCheck(t *testing.T) {
	cases := []struct {
		check   string
		result  string
		isError bool
		pass    bool
	}{
		{"exit=0", "ok\nexit code 0", false, true},
		{"exit=0", "exit code 1", false, false},
		{"exit=0", "boom", true, false},
		{"exit!=0", "ok", false, false},
		{"exit!=0", "exit status 2", false, true},
		{"tests_pass", "ok  \tpkg\t1.0s", false, true},
		{"tests_pass", "--- FAIL: TestX\nFAIL", false, false},
		{">=1_match", "found it", false, true},
		{">=1_match", "no matches found", false, false},
		{"applied>=1", "Replaced 1 occurrence", false, true},
		{"applied>=1", "old_text not found", true, false},
		{"success", "done", false, true},
		{"success", "panic: boom", false, false},
		{"fail", "exit code 1", false, true},
	}
	for _, c := range cases {
		passed, detail := evaluateExpectCheck(c.check, c.result, c.isError)
		if passed != c.pass {
			t.Errorf("evaluate(%s, err=%v) = %v (detail %q), want %v", c.check, c.isError, passed, detail, c.pass)
		}
	}
}

func mkTC(name string) provider.ToolCallDelta { return provider.ToolCallDelta{Name: name} }

func TestRecordPredictionPreferStructuredDeclaration(t *testing.T) {
	s := newForesightCalibrateState()
	// Multi-tool turn WITH tool-attributed declaration: must be recorded
	// (legacy path skips multi-tool turns entirely).
	s.recordPrediction("checking now\nEXPECT: grep >=1_match", []provider.ToolCallDelta{mkTC("read_file"), mkTC("grep")}, 3)
	if len(s.predictions) != 1 {
		t.Fatalf("want 1 attributed declaration, got %d", len(s.predictions))
	}
	p := s.predictions[0]
	if p.toolName != "grep" || p.expectCheck != ">=1_match" || p.iteration != 3 {
		t.Fatalf("prediction = %+v", p)
	}
	// Consumption: grep result (empty) violates >=1_match -> immediate warning.
	hint := s.checkCalibration("read_file", "file contents here", false, 3)
	if hint != "" {
		t.Fatalf("read_file must not consume grep-attributed declaration, got %q", hint)
	}
	hint = s.checkCalibration("grep", "no matches found", false, 3)
	if hint == "" || !strings.Contains(hint, ">=1_match") {
		t.Fatalf("expected immediate asymmetric warning, got %q", hint)
	}
}

func TestAsymmetricNegativeDeclarationSilent(t *testing.T) {
	s := newForesightCalibrateState()
	// Declared failure but succeeded: false alarm -- counted, NOT injected.
	s.recordPrediction("EXPECT: run_command fail", []provider.ToolCallDelta{mkTC("run_command")}, 1)
	hint := s.checkCalibration("run_command", "ok\nexit code 0", false, 1)
	if hint != "" {
		t.Fatalf("declared-fail-but-passed must stay silent, got %q", hint)
	}
	if len(s.predictions) != 0 {
		t.Fatal("declaration must be consumed")
	}
}

func TestLegacyRegexFallbackIntact(t *testing.T) {
	s := newForesightCalibrateState()
	// No EXPECT marker: legacy free-text prediction path still works.
	s.recordPrediction("The test should pass now.", []provider.ToolCallDelta{mkTC("run_command")}, 2)
	if len(s.predictions) != 1 {
		t.Fatalf("legacy prediction not recorded, got %d", len(s.predictions))
	}
	if s.predictions[0].expectCheck != "" {
		t.Fatalf("legacy path must leave expectCheck empty, got %q", s.predictions[0].expectCheck)
	}
	// Legacy path keeps threshold semantics: single mismatch injects nothing.
	if hint := s.checkCalibration("run_command", "exit code 1", false, 2); hint != "" {
		t.Fatalf("legacy path must respect threshold of 3, got %q", hint)
	}
	if s.mismatches != 1 {
		t.Fatalf("mismatch not counted: %d", s.mismatches)
	}
}

func TestExpectProtocolHintOnce(t *testing.T) {
	s := newForesightCalibrateState()
	if h := s.expectProtocolHint([]provider.ToolCallDelta{mkTC("read_file")}); h != "" {
		t.Fatalf("read-only turn must not announce protocol, got %q", h)
	}
	h1 := s.expectProtocolHint([]provider.ToolCallDelta{mkTC("run_command")})
	if h1 == "" || !strings.Contains(h1, "EXPECT:") {
		t.Fatalf("first side-effect turn must announce protocol, got %q", h1)
	}
	if h2 := s.expectProtocolHint([]provider.ToolCallDelta{mkTC("edit_file")}); h2 != "" {
		t.Fatalf("protocol must be announced once per run, got %q", h2)
	}
}
