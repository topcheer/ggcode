package tool

import "testing"

// #1708 case 2: equal-distance suggestions must not depend on tool
// REGISTRATION order - ties resolve to the lexically smaller name.
// "greb" is distance-1 from "grep" and (via prefix/lexical rules)
// ambiguous inputs like "glob2" tie across glob/grep at distance 2;
// assert stability across repeated calls and reverse registration.
func TestToolSuggestTieDeterministic1708(t *testing.T) {
	reg := setupTestRegistry()
	got := SuggestToolName(reg, "greb")
	if got != "grep" {
		t.Fatalf("greb: want grep, got %q", got)
	}
	for i := 0; i < 8; i++ {
		if again := SuggestToolName(reg, "greb"); again != got {
			t.Fatalf("nondeterministic suggestion: %q vs %q", got, again)
		}
	}
	// Reverse registration order must not change a TIED result:
	// "glob" vs "grep" both distance 2 from "grXb"-style inputs.
	reg2 := NewRegistry()
	_ = reg2.Register(Grep{})
	_ = reg2.Register(Glob{})
	_ = reg2.Register(ReadFile{})
	_ = reg2.Register(EditFile{WorkingDir: "/tmp"})
	a := SuggestToolName(reg2, "grbb")
	reg3 := NewRegistry()
	_ = reg3.Register(EditFile{WorkingDir: "/tmp"})
	_ = reg3.Register(ReadFile{})
	_ = reg3.Register(Glob{})
	_ = reg3.Register(Grep{})
	b := SuggestToolName(reg3, "grbb")
	if a != b {
		t.Fatalf("tie winner depends on registration order: %q vs %q", a, b)
	}
}
