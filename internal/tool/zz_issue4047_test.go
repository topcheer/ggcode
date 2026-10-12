package tool

import (
	"testing"
)

// #4047: the binary-diff header regex only matched the modify form
// (a/x and b/x); git emits /dev/null on one side for added/deleted
// binaries, so the most common accidental-binary-commit shape bypassed
// the #1650 maxFilesPerCommit advisory 100% of the time.
func TestIssue4047BinaryHeaderAllThreeForms(t *testing.T) {
	modify := "Binary files a/x.png and b/x.png differ"
	added := "Binary files /dev/null and b/new.png differ"
	deleted := "Binary files a/old.png and /dev/null differ"

	if m := diffBinaryHeader.FindStringSubmatch(modify); m == nil || len(m) < 3 || m[1] != "x.png" || m[2] != "x.png" {
		t.Fatalf("modify form must match with both sides: %v", m)
	}
	m := diffBinaryHeader.FindStringSubmatch(added)
	if m == nil || len(m) < 3 || m[1] != "" || m[2] != "new.png" {
		t.Fatalf("added form (a-side /dev/null) must match with b-side name: %v", m)
	}
	m = diffBinaryHeader.FindStringSubmatch(deleted)
	if m == nil || len(m) < 3 || m[1] != "old.png" || m[2] != "" {
		t.Fatalf("deleted form (b-side /dev/null) must match with a-side name: %v", m)
	}
	if diffBinaryHeader.MatchString("Binary files a/x.png and b/y.png differences") {
		t.Fatal("truncated marker must not match")
	}
}

// End-to-end through parseDiffStats: an added binary must register the
// file so the advisory threshold counts it (the issue's repro shape).
func TestIssue4047AddedBinaryRegistersInStats(t *testing.T) {
	diff := "diff --git a/new.png b/new.png\n" +
		"new file mode 100644\n" +
		"index 0000000..1234567\n" +
		"Binary files /dev/null and b/new.png differ\n"
	stats := parseDiffStats(diff)
	found := false
	for _, f := range stats.files {
		if f == "new.png" {
			found = true
		}
	}
	if !found {
		t.Fatalf("added binary must be registered in stats.files, got: %v", stats.files)
	}
}
