package tool

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/commands"
)

func TestCheckRequiredTools_AllPresent(t *testing.T) {
	// "go" and "git" are virtually always on PATH in a dev environment.
	missing := checkRequiredTools([]string{"go", "git"})
	if len(missing) != 0 {
		t.Errorf("expected no missing tools, got %v", missing)
	}
}

func TestCheckRequiredTools_SomeMissing(t *testing.T) {
	missing := checkRequiredTools([]string{"go", "this-tool-does-not-exist-xyz123", "another-fake-abc999"})
	if len(missing) != 2 {
		t.Fatalf("expected 2 missing tools, got %d: %v", len(missing), missing)
	}
	// Should NOT include "go"
	for _, m := range missing {
		if m == "go" {
			t.Errorf("go should not be in missing list")
		}
	}
}

func TestCheckRequiredTools_EmptyAndWhitespace(t *testing.T) {
	missing := checkRequiredTools([]string{"", "  ", "go"})
	if len(missing) != 0 {
		t.Errorf("expected no missing for empty/ws/go, got %v", missing)
	}
}

func TestBuildDependencyHint_NoDeps(t *testing.T) {
	cmd := &commands.Command{Name: "simple"}
	hint := buildDependencyHint(cmd, stubSkillLookup{})
	if hint != "" {
		t.Errorf("expected empty hint for skill with no deps, got %q", hint)
	}
}

func TestBuildDependencyHint_AllAvailable(t *testing.T) {
	cmd := &commands.Command{
		Name:         "deploy",
		Dependencies: []string{"check-env", "build-app"},
	}
	lookup := stubSkillLookup{
		"check-env": &commands.Command{Name: "check-env", Enabled: true},
		"build-app": &commands.Command{Name: "build-app", Enabled: true},
	}
	hint := buildDependencyHint(cmd, lookup)
	if hint == "" {
		t.Fatal("expected non-empty hint")
	}
	if !strings.Contains(hint, "check-env") || !strings.Contains(hint, "build-app") {
		t.Errorf("hint should mention both deps, got %q", hint)
	}
	if strings.Contains(hint, "not found") {
		t.Errorf("should not say 'not found' when all available, got %q", hint)
	}
}

func TestBuildDependencyHint_MissingDeps(t *testing.T) {
	cmd := &commands.Command{
		Name:         "deploy",
		Dependencies: []string{"check-env", "missing-skill"},
	}
	lookup := stubSkillLookup{
		"check-env": &commands.Command{Name: "check-env", Enabled: true},
	}
	hint := buildDependencyHint(cmd, lookup)
	if !strings.Contains(hint, "Prerequisite skills") {
		t.Errorf("should mention available prereqs, got %q", hint)
	}
	if !strings.Contains(hint, "missing-skill") {
		t.Errorf("should mention missing dep, got %q", hint)
	}
	if !strings.Contains(hint, "not found") {
		t.Errorf("should say 'not found' for missing dep, got %q", hint)
	}
}

func TestBuildDependencyHint_DisabledDep(t *testing.T) {
	cmd := &commands.Command{
		Name:         "deploy",
		Dependencies: []string{"disabled-skill"},
	}
	lookup := stubSkillLookup{
		"disabled-skill": &commands.Command{Name: "disabled-skill", Enabled: false},
	}
	hint := buildDependencyHint(cmd, lookup)
	if !strings.Contains(hint, "disabled-skill") {
		t.Errorf("should mention disabled dep, got %q", hint)
	}
	if !strings.Contains(hint, "not found") {
		t.Errorf("disabled dep should be treated as missing, got %q", hint)
	}
}

func TestBuildDependencyHint_NilCmd(t *testing.T) {
	hint := buildDependencyHint(nil, stubSkillLookup{})
	if hint != "" {
		t.Errorf("expected empty hint for nil cmd, got %q", hint)
	}
}
