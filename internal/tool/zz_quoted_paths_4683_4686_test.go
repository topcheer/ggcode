package tool

import (
	"strings"
	"testing"
)

// #4686/#4683: quoted diff paths (spaces / non-ASCII, git always quotes)
// must flow through every consumer of the shared header regexes instead of
// silently vanishing files from commit plans.

func TestQuotedHeaderRegexes(t *testing.T) {
	// +++ quoted
	m := diffFileHeader.FindStringSubmatch(`+++ b/"my file.go"`)
	if m == nil {
		t.Fatal("quoted +++ header must match (#4686)")
	}
	if got := diffHeaderPath(m); got != "my file.go" {
		t.Fatalf("quoted +++ path = %q, want my file.go", got)
	}
	// --- quoted
	m = diffOldFileHeader.FindStringSubmatch(`--- a/"my file.go"`)
	if m == nil {
		t.Fatal("quoted --- header must match (#4686)")
	}
	if got := diffHeaderPath(m); got != "my file.go" {
		t.Fatalf("quoted --- path = %q, want my file.go", got)
	}
	// bare forms still work
	m = diffFileHeader.FindStringSubmatch("+++ b/plain.go")
	if m == nil || diffHeaderPath(m) != "plain.go" {
		t.Fatalf("bare +++ must keep working, m=%v", m)
	}
}

// The commit-partition flow must not drop a quoted-path file: git renders
// the FIRST file of a diff with quoted headers when the name has spaces.
func TestQuotedPathSurvivesPartition(t *testing.T) {
	diff := strings.Join([]string{
		`diff --git "a/my file.go" "b/my file.go"`,
		`index 111..222 100644`,
		`--- a/"my file.go"`,
		`+++ b/"my file.go"`,
		`@@ -1,2 +1,3 @@`,
		` package x`,
		`+new line`,
		``,
	}, "\n")
	files := parseFileChanges(diff)
	found := false
	for _, f := range files {
		if strings.Contains(f.Path, "my file.go") && !strings.Contains(f.Path, `"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("quoted-path file must survive partition parsing, got: %+v (#4686)", files)
	}
}

// #4683-C: quoted-path binaries must register (they bypassed the check
// 100% before - the same miss class as #4047).
func TestQuotedBinaryHeaderRegisters(t *testing.T) {
	for _, line := range []string{
		`Binary files "a/my file.png" and "b/my file.png" differ`,
		`Binary files /dev/null and "b/new img.png" differ`,
		`Binary files a/plain.bin and /dev/null differ`,
	} {
		if m := diffBinaryHeader.FindStringSubmatch(line); m == nil {
			t.Errorf("binary header must match quoted form: %q (#4683-C)", line)
		}
	}
}

// #4683-A: a deleted CONTENT line starting with "--- a/" must not poison
// lastOldFile - only a header followed by its +++ counterpart counts.
func TestContentLineOldHeaderGuard(t *testing.T) {
	// Same shape as TestQuotedPathSurvivesPartition plus one fake
	// "--- a/..." content line inside the hunk: the guard (next line must
	// be a +++ header) must reject it, the real file must survive, and no
	// phantom "fake content" entry may appear.
	diff := strings.Join([]string{
		`diff --git "a/my file.go" "b/my file.go"`,
		`index 111..222 100644`,
		`--- a/"my file.go"`,
		`+++ b/"my file.go"`,
		`@@ -1,3 +1,4 @@`,
		` ctx`,
		`--- a/fake content not a header`,
		`+added after fake line`,
		``,
	}, "\n")
	files := parseFileChanges(diff)
	found := false
	for _, f := range files {
		if strings.Contains(f.Path, "fake content") {
			t.Fatalf("content-line fake header must not register a file, got %+v (#4683-A)", files)
		}
		if strings.Contains(f.Path, "my file.go") {
			found = true
		}
	}
	if !found {
		t.Fatalf("real file must survive the fake content line, got %+v (#4683-A)", files)
	}
}
