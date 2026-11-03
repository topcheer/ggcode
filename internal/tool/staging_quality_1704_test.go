package tool

import (
	"strings"
	"testing"
)

func TestStagingQualityBinaryAndRenamePaths1704(t *testing.T) {
	diff := "diff --git a/old.txt b/bin/app.exe\nnew file mode 100644\nBinary files /dev/null and b/bin/app.exe differ\n" +
		"diff --git a/oldname.go b/newname.go\nsimilarity index 100%\nrename from oldname.go\nrename to newname.go\n"
	got := AnalyzeStagingQuality(diff)
	if !strings.Contains(got, "bin/app.exe") {
		t.Fatalf("binary add path missing from advisory: %q", got)
	}
}

func TestStagingQualityNoSubstringFalsePositives1704(t *testing.T) {
	diff := "+++ b/docs/my.project.md\n+++ b/some/Underwire.swift\n+++ b/src/config.min.json\n"
	if got := AnalyzeStagingQuality(diff); got != "" {
		t.Fatalf("substring false positive fired: %q", got)
	}
}

func TestStagingQualityGeneratedAnchoredMatch(t *testing.T) {

	for _, path := range []string{"config.min.json", "src/config.min.json", "some/Underwire.swift"} {
		if got := AnalyzeStagingQuality("+++ b/" + path + "\n"); got != "" {
			t.Errorf("%s: expected no advisory, got %q", path, got)
		}
	}

	for _, path := range []string{"app.min.js", "dist/app.min.js", "api.pb.go", "v1/api.pb.validate.go", "svc_generated.go", "models.g.dart"} {
		got := AnalyzeStagingQuality("+++ b/" + path + "\n")
		if !strings.Contains(got, "generated") {
			t.Errorf("%s: expected generated advisory, got %q", path, got)
		}
	}

	if got := AnalyzeStagingQuality("+++ b/sources/Wire.swift\n"); !strings.Contains(got, "generated") {
		t.Errorf("sources/Wire.swift: expected generated advisory, got %q", got)
	}
}
