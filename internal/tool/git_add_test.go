package tool

import (
	"strings"
	"testing"
)

func TestMatchSensitivePathSegments1687(t *testing.T) {
	positive := []string{
		".env", "config/.env", "prod.env", "server.pem", "id_rsa",
		".aws/credentials", "home/.aws/credentials", "creds.pem",
	}
	negative := []string{
		"foo/.envrc", "foo/.env.example", "dir.env/x", "app.environment.go",
		"main.go", "README.md",
	}
	for _, p := range positive {
		if !matchSensitivePath(p) {
			t.Errorf("expected sensitive match for %q", p)
		}
	}
	for _, p := range negative {
		if matchSensitivePath(p) {
			t.Errorf("unexpected sensitive match for %q", p)
		}
	}
}

func TestGitCheckoutTrimsValidatedBranch1687(t *testing.T) {
	const paddedBranch = " main "
	const paddedStart = " feature-x "
	if err := validateBranchName(paddedBranch); err != nil {
		t.Fatalf("validator must accept whitespace-padded name: %v", err)
	}
	if err := validateRefName(strings.TrimSpace(paddedStart)); err != nil {
		t.Fatalf("validator must accept trimmed start point: %v", err)
	}
	if trimmed := strings.TrimSpace(paddedBranch); trimmed != "main" {
		t.Fatalf("execution value must be trimmed, got %q", trimmed)
	}
}

func TestGitBlameRejectsOptionLikeRevision1687(t *testing.T) {
	for _, rev := range []string{"-L10,20", "--reverse", "--porcelain"} {
		if err := validateRefName(rev); err == nil {
			t.Errorf("validateRefName must reject option-like revision %q", rev)
		}
	}
}
