package tui

import (
	"strings"
	"testing"
)

// #5526: the approval panel flattened newlines and truncated at 500 chars,
// turning the last human checkpoint in bypass mode into rubber-stamp
// theater for long commands. The preview must keep line structure (head +
// tail + "+K more lines" signal) and surface a leading "# comment" as the
// subtitle. One-liners must render unchanged.
func TestApprovalCommandPreview(t *testing.T) {
	// One-liner: unchanged (no subtitle, single line).
	sub, prev := approvalCommandPreview("ls -la /tmp")
	if sub != "" || prev != "ls -la /tmp" {
		t.Fatalf("one-liner must render unchanged, got sub=%q prev=%q", sub, prev)
	}

	// 300-line heredoc: subtitle from "# comment", head 8 + tail 2 + signal.
	var b strings.Builder
	b.WriteString("# compile gate: python3 writes and builds embedded java+kotlin\n")
	b.WriteString("python3 - <<'EOF'\n")
	for i := 0; i < 298; i++ {
		b.WriteString("print(step)\n")
	}
	b.WriteString("EOF\n")
	sub2, prev2 := approvalCommandPreview(b.String())
	if sub2 == "" || !strings.HasPrefix(sub2, "#") {
		t.Fatalf("leading comment must become subtitle, got %q", sub2)
	}
	lines := strings.Split(prev2, "\n")
	if len(lines) != 8+1+2 {
		t.Fatalf("preview must be head8 + signal + tail2 (11 lines), got %d", len(lines))
	}
	if !strings.Contains(prev2, "+290 more lines") {
		t.Fatalf("signal line must report remaining count, got: %q", prev2)
	}
	if !strings.Contains(prev2, "EOF") {
		t.Fatalf("tail must keep the heredoc terminator, got: %q", prev2)
	}
}
