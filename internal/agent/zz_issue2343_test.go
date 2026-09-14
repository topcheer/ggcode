package agent

import (
	"os"
	"strings"
	"testing"
)

// #2343: the scatter-dup hint must assert only what the detector can
// prove. Source pin (same style as the #1509 pin): the unprovable sister
// sentences are gone from the advisory template.
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
	// The provable core survives: call count from the map is factual.
	if !strings.Contains(src, "identical arguments %d times in this session") {
		t.Fatal("the provable count statement must remain")
	}
}

// The escalation tier never contained the unprovable claims - pin that it
// stays that way through any future rewording.
func TestIssue2343EscalationTierStaysProvable(t *testing.T) {
	src := readToolRedundancySrc(t)
	esc := src[strings.Index(src, "Escalation at higher thresholds"):]
	esc = esc[:strings.Index(esc, "\n\t}\n")]
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
