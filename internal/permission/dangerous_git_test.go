package permission

import (
	"testing"
)

func TestDangerousDetector_GitHigh(t *testing.T) {
	d := NewDangerousDetector()

	tests := []struct {
		cmd    string
		danger bool
	}{
		// High danger git commands
		{"git push --force", true},
		{"git push --force origin main", true},
		{"git push -f", true},
		{"git push -f origin", true},
		{"git push origin --force", true},
		{"git push --mirror", true},
		{"git push --mirror origin", true},
		{"git push --delete origin feature-branch", true},
		{"git reset --hard HEAD~1", true},
		{"git reset --hard origin/main", true},
		{"git reset --hard", true},
		{"git clean -fd", true},
		{"git clean -fdx", true},
		{"git clean -f -d -x", true},
		{"git clean -df", true},
		{"git clean -xf", true},
		{"git checkout -- .", true},
		{"git checkout .", true},
		{"git restore --staged --worktree .", true},
		{"git restore --worktree .", true},

		// Safe git commands
		{"git status", false},
		{"git diff", false},
		{"git log --oneline", false},
		{"git add file.go", false},
		{"git commit -m 'test'", false},
		{"git push origin main", false},
		{"git push", false},
		{"git reset HEAD~1", false},        // soft reset, no --hard
		{"git checkout -b feature", false}, // create branch, not destructive
		{"git stash", false},               // stash push, not clear/drop
		{"git restore file.go", false},     // single file restore, not .
		{"git branch feature", false},      // create branch, not delete
	}

	for _, tt := range tests {
		if got := d.IsDangerous(tt.cmd); got != tt.danger {
			check := d.Check(tt.cmd)
			t.Errorf("IsDangerous(%q) = %v, want %v (matched level=%v pattern=%v reason=%v)",
				tt.cmd, got, tt.danger, check.Level, check.Pattern, check.Reason)
		}
	}
}

func TestDangerousDetector_GitMedium(t *testing.T) {
	d := NewDangerousDetector()

	tests := []struct {
		cmd    string
		danger bool
	}{
		// Medium danger git commands
		{"git branch -D feature", true},
		{"git branch -D main", true},
		{"git stash clear", true},
		{"git stash drop", true},
		{"git stash drop stash@{0}", true},
		{"git push --force-with-lease", true},
		{"git push --force-with-lease origin", true},
		{"git rebase --abort", true},
		{"git merge --abort", true},
		{"git restore --staged .", true},

		// Safe alternatives
		{"git branch -d merged-feature", false}, // -d (lowercase) only deletes merged branches
		{"git stash list", false},
		{"git stash pop", false},
		{"git stash apply", false},
		{"git rebase main", false},
		{"git merge main", false},
	}

	for _, tt := range tests {
		if got := d.IsDangerous(tt.cmd); got != tt.danger {
			check := d.Check(tt.cmd)
			t.Errorf("IsDangerous(%q) = %v, want %v (matched level=%v reason=%v)",
				tt.cmd, got, tt.danger, check.Level, check.Reason)
		}
	}
}

func TestDangerousDetector_GitCheckLevels(t *testing.T) {
	d := NewDangerousDetector()

	tests := []struct {
		cmd   string
		level DangerLevel
	}{
		{"git push --force", DangerHigh},
		{"git reset --hard HEAD", DangerHigh},
		{"git clean -fd", DangerHigh},
		{"git push --force-with-lease", DangerMedium},
		{"git branch -D feature", DangerMedium},
		{"git stash clear", DangerMedium},
		{"git status", DangerNone},
		{"git diff --cached", DangerNone},
	}

	for _, tt := range tests {
		got := d.Check(tt.cmd)
		if got.Level != tt.level {
			t.Errorf("Check(%q).Level = %v, want %v", tt.cmd, got.Level, tt.level)
		}
	}
}

func TestDangerousDetector_GitWorstMatch(t *testing.T) {
	d := NewDangerousDetector()

	// git push --force --mirror should match both, worst is High
	check := d.Check("git push --force --mirror origin")
	if check.Level < DangerHigh {
		t.Errorf("git push --force --mirror should be at least DangerHigh, got %v", check.Level)
	}

	// sudo git reset --hard should match sudo (medium) + git reset --hard (high) = high
	check = d.Check("sudo git reset --hard")
	if check.Level < DangerHigh {
		t.Errorf("sudo git reset --hard should be at least DangerHigh, got %v", check.Level)
	}
}
