package tool

// #3472 probe: dce576ced's stripTrailingComment "//" branch ate BARE URLs -
// "curl https://example.com" keyed as "curl https:" (colon precedes "//",
// quote-awareness only protects QUOTED URLs). The fix requires "//" to
// start a token (preceded by whitespace/line start) to count as a comment.

import "testing"

func TestIssue3472_BareURLKeepsScheme(t *testing.T) {
	if got := normalizeCostKey("curl https://example.com"); got != "curl https://example.com" {
		t.Fatalf("bare URL must survive intact, got %q", got)
	}
	if got := normalizeCostKey("wget http://a.example/x//y"); got != "wget http://a.example/x//y" {
		t.Fatalf("host-internal // must survive, got %q", got)
	}
}

func TestIssue3472_CommentFormsStillStrip(t *testing.T) {
	if got := normalizeCostKey("go test ./x/ # slow"); got != "go test ./x/" {
		t.Fatalf("# comment must strip, got %q", got)
	}
	if got := normalizeCostKey("make build // verbose note"); got != "make build" {
		t.Fatalf("token-leading // comment must strip, got %q", got)
	}
}

func TestIssue3472_DistinctURLsDistinctKeys(t *testing.T) {
	a := normalizeCostKey("curl https://example.com")
	b := normalizeCostKey("curl https://other.example.org")
	if a == b {
		t.Fatalf("distinct URLs must key distinctly (no over-bucketing), both %q", a)
	}
}
