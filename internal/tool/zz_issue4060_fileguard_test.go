package tool

import (
	"path/filepath"
	"testing"
)

func TestIssue4060_DirPrefixCaseVariantFolds(t *testing.T) {
	if !fsCaseInsensitive() {
		t.Skip("host FS is case-sensitive - fold path not compiled in")
	}
	if !matchProtectedPattern(".GIT/config", "/ws/.GIT/config", ".git/") {
		t.Fatal("workspace-root .GIT/ prefix must fold to the protected .git/ pattern on this FS")
	}
	if !matchProtectedPattern("subdir/.GIT/config", "/ws/subdir/.GIT/config", ".git/") {
		t.Fatal("any-component .GIT segment must fold to the protected .git/ pattern on this FS")
	}

	if matchProtectedPattern("other.GIT/config", "/ws/other.GIT/config", ".git/") {
		t.Fatal("other.GIT is a distinct directory - must not be protected by .git/")
	}
}

func TestIssue4060_DoubleStarCaseVariantFolds(t *testing.T) {
	if !fsCaseInsensitive() {
		t.Skip("host FS is case-sensitive - fold path not compiled in")
	}
	if !matchProtectedPattern("SECRETS-DIR/x", "/ws/SECRETS-DIR/x", "secrets-dir/**") {
		t.Fatal("** pattern prefix segment must fold on this FS (#4060)")
	}
	if !matchProtectedPattern("secrets-dir/NESTED/key.pem", "/ws/secrets-dir/NESTED/key.pem", "secrets-dir/**/KEY.PEM") {
		t.Fatal("** multi-segment suffix tail must fold on this FS (#4060)")
	}

	if matchProtectedPattern("secrets-other/x", "/ws/secrets-other/x", "secrets-dir/**") {
		t.Fatal("secrets-other is a distinct directory - must not fold-match secrets-dir/**")
	}
}

func TestIssue4060_DefaultGitPatternCaseVariant(t *testing.T) {
	if !fsCaseInsensitive() {
		t.Skip("host FS is case-sensitive - fold path not compiled in")
	}
	ws := t.TempDir()
	g := NewFileGuard(nil)
	protected, _ := g.IsProtected(filepath.Join(ws, "subdir", ".GIT", "config"), ws)
	if !protected {
		t.Fatal("default .git/ protection missed subdir/.GIT/config on a case-insensitive FS (#4060 fail-open)")
	}
}

func TestIssue4060_CaseSensitiveFSStaysExact(t *testing.T) {
	if fsCaseInsensitive() {
		t.Skip("host FS is case-insensitive - exact-match path not compiled in")
	}
	if matchProtectedPattern(".GIT/config", "/ws/.GIT/config", ".git/") {
		t.Fatal("on case-sensitive FSes .GIT is a distinct directory - must not match .git/")
	}
}
