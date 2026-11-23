package agent

import (
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
