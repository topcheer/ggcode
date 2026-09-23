package commands

import (
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
