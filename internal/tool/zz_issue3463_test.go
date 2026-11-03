package tool

import (
	"strings"
	"testing"
	"time"
)

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

	base := normalizeCostKey(`awk '{print $1 "#"}' data.txt`)
	if got := normalizeCostKey(`awk '{print $1 "#"}' data.txt # real comment`); got != base {
		t.Errorf("quoted-hash command with real trailing comment normalized to %q, want %q", got, base)
	}

	if got := normalizeCostKey(`echo a#b # tail`); got != `echo a` {
		t.Errorf("unquoted # should start comment at first occurrence, got %q", got)
	}
}

func TestIssue3463NormalizeCostKeyURLDoubleSlashIsLiteral(t *testing.T) {

	quoted := normalizeCostKey(`curl "https://example.com//double" -s`)
	if got := normalizeCostKey(`curl "https://example.com//double" -s // retry`); got != quoted {
		t.Errorf("URL // followed by real comment normalized to %q, want %q", got, quoted)
	}

	if got := normalizeCostKey(`curl https://example.com // comment`); got != `curl https://example.com` {
		t.Errorf("bare URL must survive, whitespace-led // comment must strip; got %q", got)
	}
}

func TestIssue3463CostHintFiresAcrossCommentVariants(t *testing.T) {
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
