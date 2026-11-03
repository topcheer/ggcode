package agent

import (
	"os"
	"strings"
	"testing"
)

func TestIssue2343HintAssertsOnlyProvableFacts(t *testing.T) {
	src := readToolRedundancySrc(t)

	if strings.Contains(src, "not consecutively") {
		t.Fatal("counts never track call order - the adverb is unprovable and must be gone")
	}
	if strings.Contains(src, "already in your context") {
		t.Fatal("memoize expires read_file (mtime), LSP (15s), git_* (10s) - the claim is false for stateful tools and must be gone")
	}
	if !strings.Contains(src, "If the earlier results are still valid") {
		t.Fatal("replacement must condition the reuse advice on validity (provable phrasing)")
	}

	if !strings.Contains(src, "identical arguments %d times in this session") {
		t.Fatal("the provable count statement must remain")
	}
}

func TestIssue2343EscalationTierStaysProvable(t *testing.T) {
	src := readToolRedundancySrc(t)
	escStart := strings.Index(src, "Warning: %s called %d times with identical arguments")
	if escStart < 0 {
		t.Fatal("escalation warning string not found - tool_redundancy.go drifted")
	}
	esc := src[escStart:]
	if end := strings.Index(esc, "\n\t}\n"); end >= 0 {
		esc = esc[:end]
	}
	for _, banned := range []string{"not consecutively", "already in your context", "has not changed"} {
		if strings.Contains(esc, banned) {
			t.Fatalf("escalation tier must not reintroduce unprovable claim %q", banned)
		}
	}
}

func readToolRedundancySrc(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("tool_redundancy.go")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
