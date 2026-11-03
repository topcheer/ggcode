package tool

import (
	"testing"
)

func TestIssue4047BinaryHeaderAllThreeForms(t *testing.T) {
	modify := "Binary files a/x.png and b/x.png differ"
	added := "Binary files /dev/null and b/new.png differ"
	deleted := "Binary files a/old.png and /dev/null differ"

	if m := diffBinaryHeader.FindStringSubmatch(modify); m == nil || len(m) < 5 || m[1] != "x.png" || m[3] != "x.png" {
		t.Fatalf("modify form must match with both sides: %v", m)
	}
	m := diffBinaryHeader.FindStringSubmatch(added)
	if m == nil || len(m) < 5 || m[1] != "" || m[3] != "new.png" {
		t.Fatalf("added form (a-side /dev/null) must match with b-side name: %v", m)
	}
	m = diffBinaryHeader.FindStringSubmatch(deleted)
	if m == nil || len(m) < 5 || m[1] != "old.png" || m[3] != "" {
		t.Fatalf("deleted form (b-side /dev/null) must match with a-side name: %v", m)
	}

	if m := diffBinaryHeader.FindStringSubmatch(`Binary files "a/my file.png" and "b/my file.png" differ`); m == nil || m[2] != "my file.png" || m[4] != "my file.png" {
		t.Fatalf("quoted modify form must capture both sides (#4683-C): %v", m)
	}
	if diffBinaryHeader.MatchString("Binary files a/x.png and b/y.png differences") {
		t.Fatal("truncated marker must not match")
	}
}

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
