package tool

import (
	"strings"
	"testing"
)

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

	for _, path := range []string{
		"docs/my.project.md",
		"config.min.json",
		"my.vscode/settings.json",
	} {
		if got := AnalyzeStagingQuality("diff --git a/" + path + " b/" + path + "\nnew file mode 100644\n"); strings.Contains(got, "ide-artifact") {
			t.Errorf("path %q: false positive resurfaced (got %q)", path, got)
		}
	}
}
