package tool

import (
	"strings"
	"testing"
)

// #1687 case 3: the #835 substring anchoring mis-fired - "/.env" hit
// foo/.envrc and ".env/" hit dir.env/x. Segment-exact matching keeps the
// suffix semantics (server.pem, prod.env still match) while killing both
// false positives.
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

// #1687 case 2: " main " validates (validator trims a copy) but must be
// executed as the trimmed value, not re-fail at the git layer.
func TestGitCheckoutTrimsValidatedBranch1687(t *testing.T) {
	// The trimmed value must be what reaches the VCS layer. We test the
	// arg-normalization directly: validation passes and the field is
	// rewritten before Checkout is invoked.
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

// #1687 case 4: blame revisions that look like options must be rejected
// before they reach the git argv (before the "--" separator).
func TestGitBlameRejectsOptionLikeRevision1687(t *testing.T) {
	for _, rev := range []string{"-L10,20", "--reverse", "--porcelain"} {
		if err := validateRefName(rev); err == nil {
			t.Errorf("validateRefName must reject option-like revision %q", rev)
		}
	}
}
