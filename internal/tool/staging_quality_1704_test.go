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
// (docs/my.project.md is NOT .project; Underwire.swift is NOT Wire.swift;
// config.min.json is not .min.js). The advisory must be empty ENTIRELY --
// the previous assertion only checked two literal substrings, which the
// false-positive output happened not to contain ("Underwire.swift" renders
// with a lowercase w, and the config.min.json firing had no assertion at
// all), so the test stayed green while the bug shipped.
func TestStagingQualityNoSubstringFalsePositives1704(t *testing.T) {
	diff := "+++ b/docs/my.project.md\n+++ b/some/Underwire.swift\n+++ b/src/config.min.json\n"
	if got := AnalyzeStagingQuality(diff); got != "" {
		t.Fatalf("substring false positive fired: %q", got)
	}
}

// Regression for the #1704 case 4 FOLLOW-UP: the generated branch kept
// bare Contains after the ide branch was fixed. Extension-like generated
// patterns must anchor to the file-name suffix; bare names must match the
// file name exactly. True positives must survive the anchoring.
func TestStagingQualityGeneratedAnchoredMatch(t *testing.T) {
	// False positives: no advisory at all.
	for _, path := range []string{"config.min.json", "src/config.min.json", "some/Underwire.swift"} {
		if got := AnalyzeStagingQuality("+++ b/" + path + "\n"); got != "" {
			t.Errorf("%s: expected no advisory, got %q", path, got)
		}
	}
	// True positives still detected (suffix-anchored).
	for _, path := range []string{"app.min.js", "dist/app.min.js", "api.pb.go", "v1/api.pb.validate.go", "svc_generated.go", "models.g.dart"} {
		got := AnalyzeStagingQuality("+++ b/" + path + "\n")
		if !strings.Contains(got, "generated") {
			t.Errorf("%s: expected generated advisory, got %q", path, got)
		}
	}
	// Bare-name pattern: exact file-name match only.
	if got := AnalyzeStagingQuality("+++ b/sources/Wire.swift\n"); !strings.Contains(got, "generated") {
		t.Errorf("sources/Wire.swift: expected generated advisory, got %q", got)
	}
}
