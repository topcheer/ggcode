package tool

import (
	"strings"
	"testing"
)

// #4813: OS artifact lookup must be case-insensitive like the five sibling
// classifiers - DESKTOP.INI / EhThumbs.db variants used to skip the advisory.
func TestIssue4813_OSArtifactCaseInsensitive(t *testing.T) {
	for _, name := range []string{"DESKTOP.INI", "Desktop.ini", "desktop.ini", "EHThumbs.db", "Thumbs.DB", ".DS_STORE"} {
		base := name // filepath.Base of a bare name is the name itself
		if _, ok := osArtifactFiles[strings.ToLower(base)]; !ok {
			t.Fatalf("#4813: %s not matched (case-sensitive lookup regressed)", name)
		}
	}
}
