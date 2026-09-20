package tool

// #2590 regression: git_blame's revision and git_checkout's start_point are
// REVSPECS, not ref names. validateRefName's check-ref-format character
// rules wrongly refused HEAD~N / HEAD^ / HEAD@{N} / main~3 while git_show
// (same family) accepted them - and the error text leaked "start_point"
// wording into blame's context. The revspec guard keeps the real #1687
// case-4 injection vector (leading '-') and the range guard ('..').

import (
	"strings"
	"testing"
)

func TestIssue2590_RevspecSyntaxAccepted(t *testing.T) {
	legal := []string{
		"HEAD", "HEAD~1", "HEAD~20", "HEAD^", "HEAD^^", "HEAD^2",
		"main~3", "HEAD@{2}", "refs/tags/v1.0.0~2", "abc123", "release$v2",
	}
	for _, rev := range legal {
		if err := validateRevspec(rev); err != nil {
			t.Errorf("validateRevspec(%q) = %v, want nil (legal revspec)", rev, err)
		}
	}
}

func TestIssue2590_InjectionVectorStillRefused(t *testing.T) {
	refused := []string{"-L10,20", "--reverse", "HEAD..main", "HEAD...main"}
	for _, rev := range refused {
		if err := validateRevspec(rev); err == nil {
			t.Errorf("validateRevspec(%q) = nil, want error (option injection / range)", rev)
		}
	}
	// EMBEDDED control characters stay refused (trailing whitespace is
	// legitimately trimmed, mirroring the #1687 case-2 trim-copy fix).
	if err := validateRevspec("HEAD~1\x01"); err == nil {
		t.Error("embedded control character in revspec not refused")
	}
}

// The blame tool must surface the fixed wording - no more "start_point"
// leak from the ref-name validator into the revision context.
func TestIssue2590_BlameErrorWordingHasNoStartPointLeak(t *testing.T) {
	err := validateRevspec("-L10,20")
	if err == nil || !strings.Contains(err.Error(), "revision") {
		t.Fatalf("revspec error should speak revision language, got: %v", err)
	}
	if strings.Contains(err.Error(), "start_point") {
		t.Fatalf("revspec error leaks start_point wording: %v", err)
	}
}
