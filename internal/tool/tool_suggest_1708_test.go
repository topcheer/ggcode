package tool

import "testing"

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
