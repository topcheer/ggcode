package tool

import (
	"strings"
	"testing"
)

// #4688: URLs and emails in commit messages are not file references. Any
// message carrying a PR/issue link used to raise phantom absent-file
// warnings ("github.com", "bar.com", "page.html"); both must stay silent
// while genuine phantom-file detection stays live.

func TestIssue4688UrlInMessageNoPhantomWarning(t *testing.T) {
	msg := "fix(session): repair rollback in git_commit.go (see https://github.com/topcheer/ggcode/pull/4648)"
	got := AnalyzeMessageDiffConsistency(msg, consistencyDiff)
	if got != "" {
		t.Fatalf("message with a PR link must not warn, got: %s", got)
	}
}

func TestIssue4688WwwUrlAndPathTailNoPhantomWarning(t *testing.T) {
	msg := "docs: update git_commit.go notes from www.example.com/page.html"
	got := AnalyzeMessageDiffConsistency(msg, consistencyDiff)
	if got != "" {
		t.Fatalf("www. URL and its path tail must not warn, got: %s", got)
	}
}

func TestIssue4688EmailInMessageNoPhantomWarning(t *testing.T) {
	msg := "fix: git_commit.go owner contact foo@bar.com for questions"
	got := AnalyzeMessageDiffConsistency(msg, consistencyDiff)
	if got != "" {
		t.Fatalf("email host must not warn, got: %s", got)
	}
}

func TestIssue4688BareDomainNoPhantomWarning(t *testing.T) {
	// Scheme-less host mention - covered by the public-suffix shape filter,
	// not the URL stripper.
	msg := "fix: git_commit.go mirrors an upstream pattern from github.com"
	got := AnalyzeMessageDiffConsistency(msg, consistencyDiff)
	if got != "" {
		t.Fatalf("bare domain mention must not warn, got: %s", got)
	}
}

func TestIssue4688RealPhantomStillWarnsAlongsideUrl(t *testing.T) {
	// A genuine absent-file mention next to a PR link must still warn - the
	// filter must not over-suppress real detection.
	msg := "fix(tool): repair checkCommitMessageQuality in provider_panel.go, refs https://github.com/topcheer/ggcode/issues/4688"
	got := AnalyzeMessageDiffConsistency(msg, consistencyDiff)
	if !strings.Contains(got, "provider_panel.go") {
		t.Fatalf("real phantom file must still be named, got: %s", got)
	}
	if strings.Contains(got, "github.com") {
		t.Fatalf("the PR link domain must not appear as a phantom, got: %s", got)
	}
}

func TestIssue4688StripUrlsAndEmails(t *testing.T) {
	got := stripUrlsAndEmails("see https://x.io/a and www.y.io/b plus a@b.com ok")
	for _, bad := range []string{"https://x.io/a", "www.y.io/b", "a@b.com"} {
		if strings.Contains(got, bad) {
			t.Fatalf("stripper left %q in %q", bad, got)
		}
	}
	if !strings.Contains(got, "see") || !strings.Contains(got, "ok") {
		t.Fatalf("non-URL prose must survive, got %q", got)
	}
}
