package tool

import (
	"strings"
	"testing"
)

// #1704 case 1: binary-add diffs ("Binary files /dev/null and b/app.exe
// differ", no +++ lines) and pure renames must still contribute their
// paths via the `diff --git` header.
func TestStagingQualityBinaryAndRenamePaths1704(t *testing.T) {
	diff := "diff --git a/old.txt b/bin/app.exe\nnew file mode 100644\nBinary files /dev/null and b/bin/app.exe differ\n" +
		"diff --git a/oldname.go b/newname.go\nsimilarity index 100%\nrename from oldname.go\nrename to newname.go\n"
	got := AnalyzeStagingQuality(diff)
	if !strings.Contains(got, "bin/app.exe") {
		t.Fatalf("binary add path missing from advisory: %q", got)
	}
}

// #1704 case 4: substring patterns must not match longer file names
// (docs/my.project.md is NOT .project; Underwire.swift is NOT Wire.swift).
func TestStagingQualityNoSubstringFalsePositives1704(t *testing.T) {
	diff := "+++ b/docs/my.project.md\n+++ b/some/Underwire.swift\n+++ b/src/config.min.json\n"
	// config.min.json IS a .min.js-family? .min.json is not .min.js — none of the three should fire the dotfile/binary checks; assert no crash + no ".project"/"Wire.swift" advisories.
	got := AnalyzeStagingQuality(diff)
	if strings.Contains(got, ".project") || strings.Contains(got, "Wire.swift") {
		t.Fatalf("substring false positive fired: %q", got)
	}
}
