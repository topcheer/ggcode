package tool

import (
	"strings"
	"testing"
)

// Issue #2710 probe: #1704's single-segment equality made every
// slash-containing ideArtifactFiles pattern dead code - ".vscode/settings.json"
// can never equal a single path segment.
func TestIssue2710SlashPatternsAlive(t *testing.T) {
	for _, path := range []string{
		".vscode/settings.json",
		"web/.vscode/settings.json",
		".vscode/launch.json",
		".idea/workspace.xml",
		"" + "monorepo/pkg/.idea/workspace.xml",
		".idea/usage.statistics.xml",
		".idea/shelf/patch.xml",
		".settings/org.eclipse.jdt.core.prefs",
		"backend/.settings/foo.prefs",
	} {
		if got := AnalyzeStagingQuality("diff --git a/" + path + " b/" + path + "\nnew file mode 100644\n"); !strings.Contains(got, "ide-artifact") {
			t.Errorf("path %q: advisory lost (got %q)", path, got)
		}
	}
	// #1704 regressions must stay suppressed.
	for _, path := range []string{
		"docs/my.project.md",
		"config.min.json",
		"my.vscode/settings.json", // boundary: no / before .vscode
	} {
		if got := AnalyzeStagingQuality("diff --git a/" + path + " b/" + path + "\nnew file mode 100644\n"); strings.Contains(got, "ide-artifact") {
			t.Errorf("path %q: false positive resurfaced (got %q)", path, got)
		}
	}
}
