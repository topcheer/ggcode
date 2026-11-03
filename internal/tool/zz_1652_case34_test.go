package tool

import (
	"strings"
	"testing"
)

func Test1652Case3_CapFullSaysAtLeast(t *testing.T) {

	body := strings.Repeat("<<<<<<< HEAD\na\n=======\nb\n>>>>>>> x\n", maxConflictRegions)
	got := CheckContentForConflicts(body)
	if !strings.Contains(got, "at least 5") {
		t.Fatalf("cap-full summary must say 'at least 5 (list truncated)', got: %s", got)
	}

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
