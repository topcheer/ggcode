package tool

// #1652 regressions:
//   - 3: with exactly maxConflictRegions (5) regions the summary said
//     "5 unresolved" - a truncated cap reads as exact, sending the agent
//     hunting for exactly 5 when the file holds more.
//   - 4: the bare 'docker-compose.' prefix matched doc files
//     (docker-compose.readme.md) - the #828 Dockerfile sibling fix
//     never reached the compose matcher.

import (
	"strings"
	"testing"
)

func Test1652Case3_CapFullSaysAtLeast(t *testing.T) {
	// exactly cap regions -> "at least" wording
	body := strings.Repeat("<<<<<<< HEAD\na\n=======\nb\n>>>>>>> x\n", maxConflictRegions)
	got := CheckContentForConflicts(body)
	if !strings.Contains(got, "at least 5") {
		t.Fatalf("cap-full summary must say 'at least 5 (list truncated)', got: %s", got)
	}
	// below cap -> precise count, no "at least"
	body2 := strings.Repeat("<<<<<<< HEAD\na\n=======\nb\n>>>>>>> x\n", 3)
	got2 := CheckContentForConflicts(body2)
	if !strings.Contains(got2, "3 unresolved") || strings.Contains(got2, "at least") {
		t.Fatalf("below-cap summary stays precise, got: %s", got2)
	}
}

func Test1652Case4_ComposePrefixWhitelist(t *testing.T) {
	pos := []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml",
		"docker-compose.prod.yml", "docker-compose.override.yaml"}
	neg := []string{"docker-compose.readme.md", "docker-compose.notes", "docker-compose.go"}
	for _, f := range pos {
		if criticalFileWarning(f) == "" {
			t.Errorf("%s must stay flagged", f)
		}
	}
	for _, f := range neg {
		if criticalFileWarning(f) != "" {
			t.Errorf("%s must NOT be flagged (doc/source masquerading)", f)
		}
	}
}
