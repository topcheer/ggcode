package tool

import (
	"strings"
	"testing"
)

func TestIssue4813_OSArtifactCaseInsensitive(t *testing.T) {
	for _, name := range []string{"DESKTOP.INI", "Desktop.ini", "desktop.ini", "EHThumbs.db", "Thumbs.DB", ".DS_STORE"} {
		base := name
		if _, ok := osArtifactFiles[strings.ToLower(base)]; !ok {
			t.Fatalf("#4813: %s not matched (case-sensitive lookup regressed)", name)
		}
	}
}
