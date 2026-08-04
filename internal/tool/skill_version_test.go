package tool

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/commands"
)

// versionSkillLookup implements SkillLookup for testing buildDependencyHint.
type versionSkillLookup struct {
	skills map[string]*commands.Command
}

func (s versionSkillLookup) Get(name string) (*commands.Command, bool) {
	cmd, ok := s.skills[name]
	return cmd, ok
}

func TestBuildDependencyHint_VersionMatch(t *testing.T) {
	lookup := versionSkillLookup{
		skills: map[string]*commands.Command{
			"dep": {Name: "dep", Enabled: true, Version: "1.5.0"},
		},
	}
	cmd := &commands.Command{
		Name:         "parent",
		Dependencies: []string{"dep@>=1.0.0"},
	}
	hint := buildDependencyHint(cmd, lookup)
	if !strings.Contains(hint, "dep") {
		t.Errorf("expected hint to mention 'dep', got: %s", hint)
	}
	if strings.Contains(hint, "Version mismatch") {
		t.Errorf("should not report version mismatch for satisfied constraint, got: %s", hint)
	}
}

func TestBuildDependencyHint_VersionMismatch(t *testing.T) {
	lookup := versionSkillLookup{
		skills: map[string]*commands.Command{
			"dep": {Name: "dep", Enabled: true, Version: "1.0.0"},
		},
	}
	cmd := &commands.Command{
		Name:         "parent",
		Dependencies: []string{"dep@>=2.0.0"},
	}
	hint := buildDependencyHint(cmd, lookup)
	if !strings.Contains(hint, "Version mismatch") {
		t.Errorf("expected version mismatch warning, got: %s", hint)
	}
	if !strings.Contains(hint, "requires >=2.0.0") {
		t.Errorf("expected constraint details in hint, got: %s", hint)
	}
	if !strings.Contains(hint, "found 1.0.0") {
		t.Errorf("expected found version in hint, got: %s", hint)
	}
}

func TestBuildDependencyHint_NoVersionConstraint(t *testing.T) {
	lookup := versionSkillLookup{
		skills: map[string]*commands.Command{
			"dep": {Name: "dep", Enabled: true, Version: ""},
		},
	}
	cmd := &commands.Command{
		Name:         "parent",
		Dependencies: []string{"dep"},
	}
	hint := buildDependencyHint(cmd, lookup)
	if !strings.Contains(hint, "dep") {
		t.Errorf("expected hint to mention 'dep', got: %s", hint)
	}
	if strings.Contains(hint, "Version mismatch") {
		t.Errorf("should not report mismatch when no constraint, got: %s", hint)
	}
}

func TestBuildDependencyHint_MissingDep(t *testing.T) {
	lookup := versionSkillLookup{
		skills: map[string]*commands.Command{},
	}
	cmd := &commands.Command{
		Name:         "parent",
		Dependencies: []string{"missing-dep@>=1.0.0"},
	}
	hint := buildDependencyHint(cmd, lookup)
	if !strings.Contains(hint, "not found") {
		t.Errorf("expected 'not found' message, got: %s", hint)
	}
}
