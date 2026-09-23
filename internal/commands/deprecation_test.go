package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseCommandMarkdownDeprecatedFrontmatter(t *testing.T) {
	content := "---\n" +
		"name: old-flow\n" +
		"description: Old workflow\n" +
		"deprecated: true\n" +
		"replaced_by: new-flow\n" +
		"---\n\n" +
		"Body instructions here.\n"

	_, meta := parseCommandMarkdown(content)
	if !meta.Deprecated {
		t.Fatal("expected deprecated=true to be parsed from frontmatter")
	}
	if meta.ReplacedBy != "new-flow" {
		t.Fatalf("ReplacedBy = %q, want %q", meta.ReplacedBy, "new-flow")
	}
}

func TestParseCommandMarkdownNoDeprecated(t *testing.T) {
	content := "---\nname: fresh\ndescription: Fresh skill\n---\n\nBody.\n"
	_, meta := parseCommandMarkdown(content)
	if meta.Deprecated {
		t.Fatal("expected deprecated=false by default")
	}
	if meta.ReplacedBy != "" {
		t.Fatalf("ReplacedBy = %q, want empty", meta.ReplacedBy)
	}
}

func TestDeprecationTag(t *testing.T) {
	active := &Command{Name: "fresh"}
	if tag := active.DeprecationTag(); tag != "" {
		t.Fatalf("active skill tag = %q, want empty", tag)
	}
	deprecated := &Command{Name: "old-flow", Deprecated: true}
	if got, want := deprecated.DeprecationTag(), "(deprecated)"; got != want {
		t.Fatalf("tag = %q, want %q", got, want)
	}
	withSuccessor := &Command{Name: "old-flow", Deprecated: true, ReplacedBy: "new-flow"}
	if got, want := withSuccessor.DeprecationTag(), "(deprecated; successor: new-flow)"; got != want {
		t.Fatalf("tag = %q, want %q", got, want)
	}
	if got := (*Command)(nil).DeprecationTag(); got != "" {
		t.Fatalf("nil receiver tag = %q, want empty", got)
	}
}

func TestDeprecationAdvisory(t *testing.T) {
	active := &Command{Name: "fresh"}
	if advisory := active.DeprecationAdvisory(); advisory != "" {
		t.Fatalf("active skill advisory = %q, want empty", advisory)
	}
	deprecated := &Command{Name: "old-flow", Deprecated: true, ReplacedBy: "new-flow"}
	advisory := deprecated.DeprecationAdvisory()
	if !strings.Contains(advisory, "old-flow") || !strings.Contains(advisory, "new-flow") {
		t.Fatalf("advisory %q should mention both skill and successor", advisory)
	}
	orphan := &Command{Name: "old-flow", Deprecated: true}
	if orphan.DeprecationAdvisory() == "" {
		t.Fatal("deprecated skill without successor must still produce an advisory")
	}
	if got := (*Command)(nil).DeprecationAdvisory(); got != "" {
		t.Fatalf("nil receiver advisory = %q, want empty", got)
	}
}

func TestLoadCommandFileDeprecatedMapping(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old-flow.md")
	content := "---\nname: old-flow\ndescription: Old workflow\ndeprecated: true\nreplaced_by: new-flow\n---\n\nBody instructions.\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd, ok := loadCommandFile(path, "old-flow", loadTarget{Dir: dir, Source: SourceProject, LoadedFrom: LoadedFromSkills})
	if !ok || cmd == nil {
		t.Fatal("expected command to load")
	}
	if !cmd.Deprecated {
		t.Fatal("expected Command.Deprecated to be mapped from frontmatter")
	}
	if cmd.ReplacedBy != "new-flow" {
		t.Fatalf("Command.ReplacedBy = %q, want %q", cmd.ReplacedBy, "new-flow")
	}
}
