package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestIsProtectedBranch(t *testing.T) {
	tests := []struct {
		branch string
		want   bool
	}{
		{"main", true},
		{"master", true},
		{"develop", true},
		{"development", true},
		{"production", true},
		{"prod", true},
		{"staging", true},
		{"release/1.0", true},
		{"release/v2.5.0", true},
		{"hotfix/critical", true},
		{"feature/add-login", false},
		{"bugfix/fix-crash", false},
		{"feat/auth", false},
		{"chore/cleanup", false},
		{"refactor/api", false},
		{"my-branch", false},
		{"", false},
		{"mainline", false},    // should not match "main" as substring
		{"masterpiece", false}, // should not match "master" as substring
	}

	for _, tt := range tests {
		t.Run(tt.branch, func(t *testing.T) {
			if got := isProtectedBranch(tt.branch); got != tt.want {
				t.Errorf("isProtectedBranch(%q) = %v, want %v", tt.branch, got, tt.want)
			}
		})
	}
}

func TestBranchGuardState_Reset(t *testing.T) {
	s := newBranchGuardState()
	s.fired = true
	s.cachedBranch = "main"

	s.reset()

	if s.fired {
		t.Error("expected fired=false after reset")
	}
	if s.cachedBranch != "" {
		t.Error("expected cachedBranch empty after reset")
	}
}

func TestCheckBranchGuard_ProtectedBranch(t *testing.T) {
	// Create a temporary git repo on main branch
	dir := t.TempDir()

	// Initialize git repo
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
		{"checkout", "-b", "main"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if err := cmd.Run(); err != nil {
			t.Skipf("git not available: %v", err)
			return
		}
	}

	// Create an initial commit so the branch is established
	initialFile := filepath.Join(dir, "README.md")
	if err := os.WriteFile(initialFile, []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "init").Run()

	a := &Agent{
		workingDir:  dir,
		branchGuard: newBranchGuardState(),
	}

	// First call should return a warning
	hint := a.checkBranchGuard()
	if hint == "" {
		t.Error("expected protected branch warning on first call")
	}
	if !containsSubstr(hint, "PROTECTED BRANCH") {
		t.Errorf("expected warning to contain 'PROTECTED BRANCH', got: %s", hint)
	}
	if !containsSubstr(hint, "main") {
		t.Errorf("expected warning to mention 'main', got: %s", hint)
	}

	// Second call should NOT return a warning (already fired)
	hint2 := a.checkBranchGuard()
	if hint2 != "" {
		t.Error("expected no warning on second call (already fired)")
	}
}

func TestCheckBranchGuard_FeatureBranch(t *testing.T) {
	dir := t.TempDir()

	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
		{"checkout", "-b", "feature/test"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if err := cmd.Run(); err != nil {
			t.Skipf("git not available: %v", err)
			return
		}
	}

	initialFile := filepath.Join(dir, "README.md")
	if err := os.WriteFile(initialFile, []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "init").Run()

	a := &Agent{
		workingDir:  dir,
		branchGuard: newBranchGuardState(),
	}

	hint := a.checkBranchGuard()
	if hint != "" {
		t.Errorf("expected no warning on feature branch, got: %s", hint)
	}
}

func TestCheckBranchGuard_NilGuard(t *testing.T) {
	a := &Agent{
		workingDir:  "/tmp",
		branchGuard: nil,
	}

	hint := a.checkBranchGuard()
	if hint != "" {
		t.Errorf("expected empty hint with nil guard, got: %s", hint)
	}
}

func TestCheckBranchGuard_NoWorkingDir(t *testing.T) {
	a := &Agent{
		workingDir:  "",
		branchGuard: newBranchGuardState(),
	}

	hint := a.checkBranchGuard()
	if hint != "" {
		t.Errorf("expected empty hint with no working dir, got: %s", hint)
	}
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
