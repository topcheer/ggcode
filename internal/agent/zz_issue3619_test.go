package agent

// #3619 probe: detectGoBuildTags must resolve Makefile variable
// references. The repo's real form is `TAGS := goolm` plus
// `-tags "$(TAGS)"`; the old Fields-token scan returned the literal
// "$(TAGS)" as the tag set, so tag-aware test tooling ran untagged.

import (
	"os"
	"path/filepath"
	"testing"
)

func writeMake3619(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestIssue3619_VarFormResolves(t *testing.T) {
	dir := writeMake3619(t, "TAGS := goolm\n\nbuild:\n\tgo build -tags \"$(TAGS)\" -o bin/app ./cmd/app\n")
	got := detectGoBuildTags(dir)
	if len(got) != 1 || got[0] != "goolm" {
		t.Fatalf("variable form must resolve to [goolm], got %v", got)
	}
}

func TestIssue3619_VarChainAndLiteralForms(t *testing.T) {
	// Chained assignment resolves.
	dir := writeMake3619(t, "BASE := goolm\nTAGS := $(BASE)\nall:\n\tgo test -tags '$(TAGS)' ./...\n")
	if got := detectGoBuildTags(dir); len(got) != 1 || got[0] != "goolm" {
		t.Fatalf("chained variables must resolve, got %v", got)
	}
	// Literal forms keep working.
	dir = writeMake3619(t, "build:\n\tgo build -tags goolm ./...\n")
	if got := detectGoBuildTags(dir); len(got) != 1 || got[0] != "goolm" {
		t.Fatalf("literal space form regressed: %v", got)
	}
	dir = writeMake3619(t, "build:\n\tgo build -tags=goolm,extra ./...\n")
	if got := detectGoBuildTags(dir); len(got) != 2 || got[0] != "goolm" || got[1] != "extra" {
		t.Fatalf("= form with list regressed: %v", got)
	}
	// Unresolvable reference must not leak a literal $(...) tag.
	dir = writeMake3619(t, "build:\n\tgo build -tags \"$(UNSET)\" ./...\n")
	if got := detectGoBuildTags(dir); len(got) != 0 {
		t.Fatalf("unresolvable var must yield no tags, got %v", got)
	}
}
