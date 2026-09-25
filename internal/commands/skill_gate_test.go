package commands

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestMatchSkillPathGlob(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"**/*.ts", "src/a/b.ts", true},
		{"**/*.ts", "b.ts", true},
		{"**/*.ts", "src/a/b.js", false},
		{"src/**", "src/a/b.go", true},
		{"src/**", "other/a.go", false},
		{"internal/tool/*.go", "internal/tool/x.go", true},
		{"internal/tool/*.go", "internal/tool/sub/x.go", false},
		{"Makefile", "Makefile", true},
		{"Makefile", "src/Makefile", false},
		{"*.md", "docs/readme.md", true}, // slash-less pattern matches base name
		{"*.md", "docs/readme.txt", false},
		{"src/**/*.proto", "/repo/src/a/b.proto", true}, // relative glob vs absolute path
		{"src/**/*.proto", "/repo/other/a.proto", false},
		{"a/*/c.go", "a/b/c.go", true},
		{"", "x.go", false},
		{"x.go", "", false},
	}
	for _, tc := range cases {
		if got := matchSkillPathGlob(tc.pattern, tc.path); got != tc.want {
			t.Errorf("matchSkillPathGlob(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}

func resetGate(t *testing.T) {
	t.Helper()
	ResetSkillGateForTest()
	t.Cleanup(ResetSkillGateForTest)
}

func TestSkillGateLifecycle(t *testing.T) {
	resetGate(t)
	registerGatedSkill("proto-gen", []string{"**/*.proto"})

	if !SkillHiddenByPaths("proto-gen") {
		t.Fatal("gated skill should be hidden before any matching touch")
	}
	if SkillHiddenByPaths("unknown-skill") {
		t.Fatal("unregistered skill must never be hidden")
	}

	NoteTouchedPaths("internal/agent/agent.go")
	if !SkillHiddenByPaths("proto-gen") {
		t.Fatal("non-matching touch must not activate the gated skill")
	}

	NoteTouchedPaths("api/v1/user.proto")
	if SkillHiddenByPaths("proto-gen") {
		t.Fatal("matching touch must activate the gated skill")
	}

	// Monotonic: stays activated for the session.
	registerGatedSkill("proto-gen", []string{"**/*.proto"})
	if SkillHiddenByPaths("proto-gen") {
		t.Fatal("activation must be monotonic within a session")
	}
}

func TestSkillGateRegistersWithPreTouchedPaths(t *testing.T) {
	resetGate(t)
	NoteTouchedPaths("src/gen/api.pb.go")
	registerGatedSkill("codegen", []string{"src/**/*.pb.go"})
	if SkillHiddenByPaths("codegen") {
		t.Fatal("skill registered after the condition was met must activate immediately")
	}
}

func TestSkillGateUnregister(t *testing.T) {
	resetGate(t)
	registerGatedSkill("a", []string{"*.md"})
	registerGatedSkill("b", []string{"*.md"})
	NoteTouchedPaths("notes.md")
	registerGatedSkill("a", nil) // empty patterns unregister
	if SkillHiddenByPaths("a") {
		t.Fatal("unregistered skill must not be hidden")
	}
	if SkillHiddenByPaths("b") {
		t.Fatal("b was activated by the touch; must stay visible")
	}
}

func TestNoteTouchedPathsNoPatternsNoop(t *testing.T) {
	resetGate(t)
	NoteTouchedPaths("a.go", "b.go") // must not panic or allocate state
	if SkillHiddenByPaths("anything") {
		t.Fatal("nothing is registered; nothing can be hidden")
	}
}

func TestSkillGateConcurrentAccess(t *testing.T) {
	resetGate(t)
	registerGatedSkill("hot", []string{"**/*.go"})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			NoteTouchedPaths(filepath.Join("pkg", string(rune('a'+i)), "f.go"))
			_ = SkillHiddenByPaths("hot")
			registerGatedSkill("hot2", []string{"**/*.ts"})
		}(i)
	}
	wg.Wait()
	if SkillHiddenByPaths("hot") {
		t.Fatal("concurrent touches should have activated the gated skill")
	}
}

// TestManagerSkillNamesHidesGatedSkill covers the discovery surface end to
// end: a skill declaring `paths:` in frontmatter is absent from SkillNames
// until the agent touches a matching file, then appears.
func TestManagerSkillNamesHidesGatedSkill(t *testing.T) {
	resetGate(t)
	dir := t.TempDir()
	skillDir := filepath.Join(dir, ".ggcode", "skills", "proto-helper")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\n" +
		"name: proto-helper\n" +
		"description: Regenerate protobuf bindings\n" +
		"paths:\n" +
		"  - \"**/*.proto\"\n" +
		"---\n\n" +
		"Run protoc with the project plugins."
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(dir)
	if names := m.SkillNames(); containsName(names, "proto-helper") {
		t.Fatalf("gated skill must be hidden from SkillNames before touch, got %v", names)
	}

	NoteTouchedPaths("proto/api.proto")
	if names := m.SkillNames(); !containsName(names, "proto-helper") {
		t.Fatalf("gated skill must appear in SkillNames after matching touch, got %v", names)
	}

	// Direct invocation is never blocked, gated or not.
	if cmd, ok := m.Get("proto-helper"); !ok || cmd == nil {
		t.Fatal("Get must return the gated skill regardless of activation state")
	}
}

// TestLoaderParsesPathsFrontmatter pins the YAML key and its propagation.
func TestLoaderParsesPathsFrontmatter(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, ".ggcode", "skills", "gated")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: gated\ndescription: Gated skill\npaths:\n  - \"**/*.proto\"\n  - \"src/api/**\"\n---\n\nBody"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cmds := NewLoader(dir).Load()
	cmd, ok := cmds["gated"]
	if !ok {
		t.Fatal("expected gated skill to be loaded")
	}
	if len(cmd.Paths) != 2 || cmd.Paths[0] != "**/*.proto" || cmd.Paths[1] != "src/api/**" {
		t.Errorf("unexpected paths: %v", cmd.Paths)
	}
}

func containsName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}
