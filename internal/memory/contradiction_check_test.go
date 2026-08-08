package memory

import (
	"strings"
	"testing"
)

func TestContradictionCheck_BuildCommandConflict(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	writeMem(t, dir, "build", "build command: go build ./...\nframework: gin\n")

	cc := am.CheckContradiction("build2", "build command: go build -tags goolm ./...\nframework: gin\n")
	if !cc.HasConflict() {
		t.Fatalf("expected contradiction for differing build command, got none")
	}
	found := false
	for _, c := range cc.Conflicts {
		if c.Subject == "build command" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'build command' subject in conflicts: %+v", cc.Conflicts)
	}
}

func TestContradictionCheck_NoConflictSameValue(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	writeMem(t, dir, "build", "build command: go build ./...\n")

	cc := am.CheckContradiction("build2", "build command: go build ./...\n")
	if cc.HasConflict() {
		t.Fatalf("expected no contradiction for identical values, got: %+v", cc.Conflicts)
	}
}

func TestContradictionCheck_FrameworkConflict(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	writeMem(t, dir, "stack", "framework: gin\n")

	cc := am.CheckContradiction("stack2", "framework: echo\n")
	if !cc.HasConflict() {
		t.Fatalf("expected contradiction for gin vs echo, got none")
	}
}

func TestContradictionCheck_UnrelatedNoConflict(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	writeMem(t, dir, "deploy", "deploy command: make deploy\n")

	cc := am.CheckContradiction("test", "test command: go test ./...\n")
	if cc.HasConflict() {
		t.Fatalf("expected no contradiction for unrelated subjects, got: %+v", cc.Conflicts)
	}
}

func TestContradictionCheck_SkipsSameKey(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	writeMem(t, dir, "build", "build command: go build ./...\n")

	cc := am.CheckContradiction("build", "build command: go build -tags goolm ./...\n")
	if cc.HasConflict() {
		t.Fatalf("expected no self-contradiction, got: %+v", cc.Conflicts)
	}
}

func TestContradictionCheck_VersionConflict(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	writeMem(t, dir, "deps", "go version: 1.21\n")

	cc := am.CheckContradiction("deps2", "go version: 1.22\n")
	if !cc.HasConflict() {
		t.Fatalf("expected version conflict, got none")
	}
}

func TestContradictionCheck_OppositeValueConflict(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	writeMem(t, dir, "config", "caching: enabled\n")

	cc := am.CheckContradiction("config2", "caching: disabled\n")
	if !cc.HasConflict() {
		t.Fatalf("expected enabled/disabled conflict, got none")
	}
}

func TestContradictionCheck_PolarityConflict(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	writeMem(t, dir, "style", "Always use tabs for indentation.\n")

	cc := am.CheckContradiction("style2", "Never use tabs for indentation.\n")
	if !cc.HasConflict() {
		t.Fatalf("expected polarity conflict, got none")
	}
}

func TestContradictionCheck_EmptyMemory(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	cc := am.CheckContradiction("new", "build command: go build ./...\n")
	if cc.HasConflict() {
		t.Fatalf("expected no conflict in empty store")
	}
}

func TestContradictionCheck_NoStructuredClaims(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	writeMem(t, dir, "notes", "This is a general note about the project.\n")

	cc := am.CheckContradiction("notes2", "This is a different general note.\n")
	if cc.HasConflict() {
		t.Fatalf("expected no conflict for unstructured text, got: %+v", cc.Conflicts)
	}
}

func TestContradictionFormatWarning(t *testing.T) {
	cc := ContradictionCheck{
		Conflicts: []ContradictionConflict{
			{
				ExistingKey:   "build",
				Subject:       "build command",
				NewValue:      "go build -tags goolm ./...",
				ExistingValue: "go build ./...",
			},
		},
	}
	w := cc.FormatContradictionWarning("build2")
	if w == "" {
		t.Fatal("expected non-empty warning")
	}
	if !strings.Contains(w, "build2") {
		t.Errorf("warning should mention new key: %s", w)
	}
}

func TestContradictionFormatWarning_Empty(t *testing.T) {
	cc := ContradictionCheck{}
	if cc.FormatContradictionWarning("key") != "" {
		t.Error("expected empty warning for no conflicts")
	}
}

func TestClaimsConflict_Direct(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"identical", "go build ./...", "go build ./...", false},
		{"version drift", "version 1.21", "version 1.22", true},
		{"opposite", "enabled", "disabled", true},
		{"polarity", "~tabs", "tabs", true},
		{"unrelated", "gin", "make deploy", false},
		{"empty", "", "value", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := claimsConflict(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("claimsConflict(%q,%q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
