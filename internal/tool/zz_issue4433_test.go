package tool

import (
	"strings"
	"testing"
)

func TestIssue4433QuotedAndSpacedHeadersParsed(t *testing.T) {

	diff := "diff --git \"a/\\344\\270\\255.go\" \"b/\\344\\270\\255.go\"\n" +
		"index 111..222 100644\n" +
		"--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n"
	files, _, _, _ := parseDiffFacts(diff)
	if _, ok := files["中.go"]; !ok {
		t.Fatalf("quoted non-ASCII basename missing: %v", files)
	}

	spaced := "diff --git \"a/foo bar.go\" \"b/foo bar.go\"\n" +
		"--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n"
	files2, _, _, _ := parseDiffFacts(spaced)
	if _, ok := files2["foo bar.go"]; !ok {
		t.Fatalf("quoted spaced basename missing: %v", files2)
	}

	plain := "diff --git a/plain.go b/plain.go\n"
	files3, _, _, _ := parseDiffFacts(plain)
	if _, ok := files3["plain.go"]; !ok {
		t.Fatalf("plain basename missing: %v", files3)
	}
}

func TestGitUnquotePath(t *testing.T) {
	cases := map[string]string{
		"plain.go":           "plain.go",
		"\\344\\270\\255.go": "中.go",
		"foo\\ bar.go":       "foo\\ bar.go",
		"a\\\\b.go":          "a\\b.go",
		"q\\\"uote.go":       "q\"uote.go",
		"trailing\\":         "trailing\\",
	}
	for in, want := range cases {
		if got := gitUnquotePath(in); got != want {
			t.Errorf("gitUnquotePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIssue4433RenameHeadersCountedOnce(t *testing.T) {
	var sb strings.Builder
	for _, name := range []string{"old1.go new1.go", "old2.go new2.go", "old3.go new3.go"} {
		parts := strings.Fields(name)
		sb.WriteString("diff --git a/" + parts[0] + " b/" + parts[1] + "\n")
		sb.WriteString("similarity index 100%\nrename from " + parts[0] + "\nrename to " + parts[1] + "\n")
	}
	files, _, _, _ := parseDiffFacts(sb.String())
	if len(files) != 3 {
		t.Fatalf("3 renames must yield 3 files, got %d: %v", len(files), files)
	}
	for _, n := range []string{"new1.go", "new2.go", "new3.go"} {
		if _, ok := files[n]; !ok {
			t.Fatalf("new-side basename %q missing: %v", n, files)
		}
	}
}

func TestIssue4433MinusContentNotHeader(t *testing.T) {
	diff := "diff --git a/doc.md b/doc.md\n" +
		"--- a/doc.md\n+++ b/doc.md\n@@ -1,3 +1,2 @@\n" +
		" intro\n" +
		"--- a/example.txt\n" +
		"+++\n" +
		" tail\n"
	changes := parseFileChanges(diff)
	if len(changes) != 1 {
		t.Fatalf("phantom file injected from content-shaped line: %+v", changes)
	}
	if changes[0].Path != "doc.md" {
		t.Fatalf("unexpected file: %+v", changes[0])
	}
	if changes[0].Deletions != 1 {
		t.Fatalf("deleted content line swallowed by header misdetection: del=%d, want 1", changes[0].Deletions)
	}
	if changes[0].Additions != 1 {
		t.Fatalf("bare '+++' content line lost via stale adjacency flag: add=%d, want 1", changes[0].Additions)
	}
}
