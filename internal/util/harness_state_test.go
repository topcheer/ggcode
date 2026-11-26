package util

import "testing"

func TestIsHarnessStatePath(t *testing.T) {
	yes := []string{
		".ggcode", ".ggcode/", "./.ggcode", ".ggcode/memory/a.md",
		".ggcode/memory/experience/07913f953e7e.md", ".ggcode/state.json",
		"/Volumes/new/ggai/GGAiAce/.ggcode/agent-rules.json",
		"/Users/x/.ggcode/skills/foo/SKILL.md", "work/.ggcode/x",
		`.ggcode\memory\a.md`, "  .ggcode/usage.json  ",
	}
	no := []string{"", "main.go", ".ggcode-backup/x", "x.ggcode/y", "ggcode/a", "src/ggcode/a", ".ggcode.md"}
	for _, p := range yes {
		if !IsHarnessStatePath(p) {
			t.Errorf("IsHarnessStatePath(%q) = false, want true", p)
		}
	}
	for _, p := range no {
		if IsHarnessStatePath(p) {
			t.Errorf("IsHarnessStatePath(%q) = true, want false", p)
		}
	}
}

func TestPorcelainPath(t *testing.T) {
	cases := map[string]string{
		" M main.go":                  "main.go",
		"D  .ggcode/memory/old.md":    ".ggcode/memory/old.md",
		"?? .ggcode/new.json":         ".ggcode/new.json",
		"R  old.md -> new.md":         "new.md",
		`   a -> b`:                   "b",
		`?? "with space.txt"`:         "with space.txt",
		` M "tab\tname.txt"`:          "tab\tname.txt",
		`A  "oct\303\251.txt"`:        "octé.txt",
		"M  .ggcode/agent-rules.json": ".ggcode/agent-rules.json",
		"":                            "",
		"   ":                         "",
	}
	for in, want := range cases {
		if got := PorcelainPath(in); got != want {
			t.Errorf("PorcelainPath(%q) = %q, want %q", in, got, want)
		}
	}
	if !IsHarnessStatePorcelainLine(" D .ggcode/memory/x.md") {
		t.Error("porcelain line under .ggcode must be harness state")
	}
	if IsHarnessStatePorcelainLine(" D src/x.md") {
		t.Error("src line must not be harness state")
	}
}
