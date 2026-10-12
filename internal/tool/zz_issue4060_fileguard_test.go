package tool

// #4060 companion tests: the FS case fold (#1683 matchSegFS) must cover the
// directory-prefix branch and the matchDoubleStar internals, not just the two
// glob call points. Before the fix, matchProtectedPattern(".GIT/...") under
// the default ".git/" pattern and "SECRETS-DIR/x" under "secrets-dir/**"
// both failed OPEN on macOS/Windows while the OS resolved them to the
// protected targets (same files on APFS/NTFS).

import (
	"path/filepath"
	"testing"
)

// Directory-prefix branch: ".git/" pattern vs ".GIT" case variants must fold
// on case-insensitive FSes (workspace-root prefix AND any-component forms).
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
	// Segment equality path: "other.GIT" is a DIFFERENT name and must not
	// fold-match ".git" (fold must not over-match distinct basenames).
	if matchProtectedPattern("other.GIT/config", "/ws/other.GIT/config", ".git/") {
		t.Fatal("other.GIT is a distinct directory - must not be protected by .git/")
	}
}

// ** pattern internals: every filepath.Match site inside matchDoubleStar
// must fold (prefix segments, multi-segment suffix tails, base fallback).
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
	// Distinct directory must stay unprotected (fold is not a wildcard).
	if matchProtectedPattern("secrets-other/x", "/ws/secrets-other/x", "secrets-dir/**") {
		t.Fatal("secrets-other is a distinct directory - must not fold-match secrets-dir/**")
	}
}

// End-to-end with DEFAULT patterns (zero config): the shipped ".git/"
// protection must catch a case-variant path inside the workspace on
// case-insensitive FSes - the exact agent-writable bypass from the issue.
func TestIssue4060_DefaultGitPatternCaseVariant(t *testing.T) {
	if !fsCaseInsensitive() {
		t.Skip("host FS is case-sensitive - fold path not compiled in")
	}
	ws := t.TempDir()
	g := NewFileGuard(nil) // default patterns include ".git/"
	protected, _ := g.IsProtected(filepath.Join(ws, "subdir", ".GIT", "config"), ws)
	if !protected {
		t.Fatal("default .git/ protection missed subdir/.GIT/config on a case-insensitive FS (#4060 fail-open)")
	}
}

// Linux (and any case-sensitive FS): the fold must stay OFF - .GIT is a
// genuinely different directory there and must NOT be swallowed by .git/.
func TestIssue4060_CaseSensitiveFSStaysExact(t *testing.T) {
	if fsCaseInsensitive() {
		t.Skip("host FS is case-insensitive - exact-match path not compiled in")
	}
	if matchProtectedPattern(".GIT/config", "/ws/.GIT/config", ".git/") {
		t.Fatal("on case-sensitive FSes .GIT is a distinct directory - must not match .git/")
	}
}
