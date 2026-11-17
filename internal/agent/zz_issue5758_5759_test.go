package agent

import (
	"strings"
	"testing"
)

// #5758: warningsIssued shares the green-build window with distinctFiles; a
// green build must reopen the warning budget.
func Test5758GreenBuildResetsWarningsIssued(t *testing.T) {
	s := &editPropagationState{
		distinctFiles:  map[string]bool{"a.go": true, "b.go": true},
		warningsIssued: propagationMaxWarnings, // budget fully exhausted
	}
	s.recordGreenBuild()
	if len(s.distinctFiles) != 0 {
		t.Fatalf("green build must clear distinctFiles, got %d", len(s.distinctFiles))
	}
	if s.warningsIssued != 0 {
		t.Fatalf("green build must reset warningsIssued (green-build window semantics), got %d", s.warningsIssued)
	}
}

// #5759: "FOO=" (key with empty value) counts as set, mirroring os.Environ().
func Test5759ParseEnvContentEmptyValueCountsAsSet(t *testing.T) {
	got := parseEnvContent("# comment\n\nexport FOO=\nBAR=1\nBAZ=x\n")
	for _, name := range []string{"FOO", "BAR", "BAZ"} {
		if !got[name] {
			t.Fatalf("key %q must be counted as set (empty value mirrors os.Environ), got map %v", name, got)
		}
	}
	if got["QUX"] {
		t.Fatal("absent key must not appear")
	}
	if len(strings.Split("# comment", "\n")) != 1 {
		t.Fatal("sanity")
	}
}
