package agent

import (
	"os"
	"strings"
	"testing"
)

// #4016: the commit-hint comment must describe the real gate order — it is
// the last HARD gate, with two more continue gates (doc-drift, draft-PR)
// reachable under the default config.
func TestIssue4016_CommentMatchesGateReality(t *testing.T) {
	raw, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	hintAt := strings.Index(src, "checkCommitHintGate(runStats)")
	driftAt := strings.Index(src, "checkDocDriftGate(runStats)")
	draftAt := strings.Index(src, "checkDraftPRHintGate")
	if hintAt < 0 || driftAt < 0 || draftAt < 0 {
		t.Fatal("gate call sites not found (gate set changed?)")
	}
	if !(hintAt < driftAt && driftAt < draftAt) {
		t.Fatal("gate order in source changed: commit-hint is no longer before doc-drift/draft-PR")
	}
	blockStart := strings.Index(src, "// Post-completion commit hint")
	if blockStart < 0 || blockStart > hintAt {
		t.Fatal("commit-hint comment block not found")
	}
	block := src[blockStart:hintAt]
	if strings.Contains(block, "This is the last gate") {
		t.Fatal("#4016: stale 'last gate' claim still present")
	}
	if !strings.Contains(block, "HARD gate") || !strings.Contains(block, "doc-drift") || !strings.Contains(block, "draft-PR") {
		t.Fatal("#4016: comment must state the two follow-on continue gates")
	}
}
