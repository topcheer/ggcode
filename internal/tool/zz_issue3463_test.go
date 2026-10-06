package tool

import (
	"strings"
	"testing"
	"time"
)

// #3463: normalizeCostKey must actually implement what its comment
// promised - comment-bearing and whitespace variants of the same command
// hit the same history bucket. Before the fix, "go test ./x/" and
// "go test ./x/ # slow" produced different keys, so alternating usage
// never reached costHintMinRuns and the cost hint never fired.

func TestIssue3463NormalizeCostKeyCommentVariantsShareBucket(t *testing.T) {
	base := normalizeCostKey("go test ./internal/tool/")
	variants := []string{
		"go test ./internal/tool/ # slow",
		"go test ./internal/tool/ // slow suite",
		"go test ./internal/tool/#flaky",
	}
	for _, v := range variants {
		if got := normalizeCostKey(v); got != base {
			t.Errorf("comment variant %q normalized to %q, want %q", v, got, base)
		}
	}
}

func TestIssue3463NormalizeCostKeyWhitespaceVariantsShareBucket(t *testing.T) {
	base := normalizeCostKey("go test ./internal/tool/")
	for _, v := range []string{
		"  go test ./internal/tool/  ",
		"go  test ./internal/tool/",
		"go\ttest\t./internal/tool/",
	} {
		if got := normalizeCostKey(v); got != base {
			t.Errorf("whitespace variant %q normalized to %q, want %q", v, got, base)
		}
	}
}

func TestIssue3463NormalizeCostKeyQuotedHashIsLiteral(t *testing.T) {
	// '#' inside quotes must not start a comment: the whole command
	// including the quoted # is the key.
	base := normalizeCostKey(`awk '{print $1 "#"}' data.txt`)
	if got := normalizeCostKey(`awk '{print $1 "#"}' data.txt # real comment`); got != base {
		t.Errorf("quoted-hash command with real trailing comment normalized to %q, want %q", got, base)
	}
	// An unquoted # that only appears inside the command body would
	// truncate - that is the documented shell semantic we mirror.
	if got := normalizeCostKey(`echo a#b # tail`); got != `echo a` {
		t.Errorf("unquoted # should start comment at first occurrence, got %q", got)
	}
}

func TestIssue3463NormalizeCostKeyURLDoubleSlashIsLiteral(t *testing.T) {
	// // inside quotes (URL) must survive; unquoted // starts a comment.
	quoted := normalizeCostKey(`curl "https://example.com//double" -s`)
	if got := normalizeCostKey(`curl "https://example.com//double" -s // retry`); got != quoted {
		t.Errorf("URL // followed by real comment normalized to %q, want %q", got, quoted)
	}
	// #3472: whitespace-led "//" after a BARE url is a real comment - strip
	// the comment, keep the url (the old assertion pinned the over-strip
	// regression that keyed this as "curl https:").
	if got := normalizeCostKey(`curl https://example.com // comment`); got != `curl https://example.com` {
		t.Errorf("bare URL must survive, whitespace-led // comment must strip; got %q", got)
	}
}

func TestIssue3463CostHintFiresAcrossCommentVariants(t *testing.T) {
	// End-to-end: record two runs under comment-variant spellings and
	// confirm the hint now fires (min runs=2, avg >= threshold).
	const (
		slow    = 3 * time.Minute
		baseCmd = "go test ./internal/deep/"
	)
	recordCommandCost(baseCmd, slow)
	recordCommandCost(baseCmd+" # slow", slow)
	hint := commandCostHint(baseCmd)
	if hint == "" {
		t.Fatal("cost hint did not fire across comment variants - bucket split persists")
	}
	if want := "~3m0s"; !strings.Contains(hint, want) {
		t.Errorf("hint %q missing average %q", hint, want)
	}
}

func TestIssue3463NormalizeCostKeyAllComment(t *testing.T) {
	if got := normalizeCostKey("# just a comment"); got != "" {
		t.Errorf("comment-only command should normalize to empty, got %q", got)
	}
}
