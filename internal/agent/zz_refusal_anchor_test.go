package agent

import (
	"strings"
	"testing"
)

// #anchor-boundary regression: flag anchors must only come from token-start
// dashes. Production false-positive chain (2026-10-17): the persisted
// excerpt "...请追踪 mid-run 用户消息注入路径..." leaked "-run" as an anchor
// via the old free-floating regex, then blocked every
// `go test ./pkg -run 'TestX'` invocation; a sibling entry leaked "-search"
// from "re-search" the same way. Hyphenated prose words are NOT flags.
func TestRefusalFlagsTokenStartOnly(t *testing.T) {
	cases := []struct {
		excerpt string
		want    string // joined with comma; "" means no anchors
	}{
		{"请追踪 mid-run 用户消息注入路径（agent", ""},
		{"do not re-search covered ground", ""},
		{"use -tags goolm only", "-tags"},
		{"use --output=file never again", "--output"},
		{"no --force, no -delete", "--force,-delete"},
	}
	for _, tc := range cases {
		got := strings.Join(refusalFlags(tc.excerpt), ",")
		if got != tc.want {
			t.Fatalf("refusalFlags(%q) = %q, want %q", tc.excerpt, got, tc.want)
		}
	}
}

// Min body length is 3 chars after the dash(es), matching the old regex's
// `{2,}` plus the mandatory first letter.
func TestRefusalFlagsMinBody(t *testing.T) {
	got := refusalFlags("avoid -p 1 and --go and -ok and -run")
	for _, f := range got {
		if f == "-p" || f == "-go" || f == "-ok" {
			t.Fatalf("short body leaked as anchor: %q (all: %v)", f, got)
		}
		if f != "-run" {
			t.Fatalf("want -run kept, got %q (all: %v)", f, got)
		}
	}
}

// The exact production excerpts must no longer produce ANY flag anchor.
func TestRefusalFlagsProductionPollutionExcerpts(t *testing.T) {
	for _, e := range []string{
		`do not modify the auth module"，这条消息是否经过 recordConstraints？请追踪 mid-run 用户消息注入路径（agent`,
		`do not re-search covered ground" 指令矛盾，导致模型行为混乱或多轮冗余搜索`,
		`never block the stop"（LLM 错误/空响应全放行），panic 是唯一漏网异常路径`,
	} {
		if got := refusalFlags(e); len(got) != 0 {
			t.Fatalf("polluted excerpt leaked anchors %v: %q", got, e)
		}
	}
}

// End-to-end: a persisted entry holding "mid-run" prose must NOT block a
// go test command using the literal -run flag.
func TestCheckBlockedMidRunProseDoesNotBlockGoTest(t *testing.T) {
	l := newRefusalLedger(t.TempDir())
	l.record(`do not modify the auth module"，这条消息是否经过 recordConstraints？请追踪 mid-run 用户消息注入路径（agent`, "nl")
	cmd := `{"command":"cd /x && go test -tags goolm ./internal/tui/ -run 'TestHandleRemoteInbound_IMApproval' -count=1 -timeout 120s 2>&1 | tail -2"}`
	if msg := l.checkBlocked("run_command", cmd); msg != "" {
		t.Fatalf("false-positive block persisted: %s", msg)
	}
}

// True refusal anchors still block: a "never use --force push" entry must
// block a command that carries --force.
func TestCheckBlockedRealFlagStillBlocks(t *testing.T) {
	l := newRefusalLedger(t.TempDir())
	l.record("never use --force push to main", "nl")
	cmd := `{"command":"git push --force origin main"}`
	if msg := l.checkBlocked("run_command", cmd); msg == "" || !strings.Contains(msg, "--force") {
		t.Fatalf("expected --force refusal to block, got %q", msg)
	}
}

// Path anchors: in-word extension fragments are not anchors.
func TestRefusalPathInWordExtensionNotAnchor(t *testing.T) {
	got := refusalTargets("keep xconfig.yaml untouched")
	for _, a := range got {
		if a == "config.yaml" {
			t.Fatalf("in-word extension leaked as anchor: %v", got)
		}
	}
}
