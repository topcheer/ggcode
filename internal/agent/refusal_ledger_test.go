package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/tool"
)

func newRefusalTestLedger(t *testing.T) *refusalLedger {
	t.Helper()
	return newRefusalLedger(t.TempDir())
}

func TestExtractRefusalsNegationOnly(t *testing.T) {
	text := "don't touch internal/auth/anything.go. Only use pattern Y. Never run git push --force."
	got := extractRefusals(text)
	if len(got) < 2 {
		t.Fatalf("expected >=2 refusals, got %v", got)
	}
	joined := strings.Join(got, "|")
	if !strings.Contains(joined, "internal/auth") {
		t.Errorf("path refusal missing: %v", got)
	}
	if !strings.Contains(joined, "--force") {
		t.Errorf("flag refusal missing: %v", got)
	}
	// "Only use X" is a directive, not a refusal.
	if strings.Contains(strings.ToLower(joined), "only use") {
		t.Errorf("exclusivity directive leaked into refusal ledger: %v", got)
	}
}

func TestRefusalRecordIdempotent(t *testing.T) {
	l := newRefusalTestLedger(t)
	l.record("don't touch internal/auth/core.go", "nl", refusalKindDirect)
	l.record("don't touch internal/auth/core.go", "nl", refusalKindDirect)
	if n := len(l.data.Entries); n != 1 {
		t.Fatalf("expected 1 entry after dup record, got %d", n)
	}
}

// zz #4284: quoted text / pasted logs / code comments must never produce
// ledger entries - only direct user instructions enter extraction.
func TestIssue4284NonDirectiveTextFiltered(t *testing.T) {
	text := "look at this:\n" +
		"```go\n// don't touch config/production.yaml manually\n```\n" +
		"> note: never use --force push here\n" +
		"2026-10-17 12:00:00 [ERROR] must not retry the deploy\n" +
		"# avoid touching release.lock in scripts\n" +
		"but seriously, don't touch secrets/prod.env anymore"
	got := extractRefusals(text)
	joined := strings.Join(got, " || ")
	for _, junk := range []string{"production.yaml", "--force", "retry the deploy", "release.lock"} {
		if strings.Contains(joined, junk) {
			t.Errorf("non-directive fragment leaked into ledger: %q in %q", junk, joined)
		}
	}
	hit := false
	for _, e := range got {
		if strings.Contains(e, "secrets/prod") {
			hit = true
		}
	}
	if !hit {
		t.Errorf("direct refusal must still be extracted, got %q", joined)
	}
}

// zz #4284: legacy entries (empty Kind) are re-annotated at load;
// comment/quote/log-shaped excerpts become advisory-only and never block.
func TestIssue4284LegacyReannotationAndAdvisorySkip(t *testing.T) {
	l := newRefusalTestLedger(t)
	// Simulate a pre-#4284 ledger: kind field absent on disk.
	l.data.Entries = []refusalEntry{
		{Excerpt: "// don't touch internal/auth/core.go", Source: "nl", Ts: time.Now().Unix()},
		{Excerpt: "> never use git push --force", Source: "nl", Ts: time.Now().Unix()},
		{Excerpt: "don't touch internal/util/paths.go", Source: "nl", Ts: time.Now().Unix()},
	}
	for i := range l.data.Entries {
		if l.data.Entries[i].Kind == "" {
			if refusalExcerptLooksNonDirective(l.data.Entries[i].Excerpt) {
				l.data.Entries[i].Kind = refusalKindFragment
			} else {
				l.data.Entries[i].Kind = refusalKindDirect
			}
		}
	}
	l.saveLocked()
	// Reload from disk: load() must reproduce the same annotation.
	l2 := newRefusalLedger(l.workingDir)
	args := `{"command":"cat internal/auth/core.go"}`
	if msg := l2.checkBlocked("run_command", args); msg != "" {
		t.Errorf("advisory (fragment) entry must not block, got %q", msg)
	}
	if msg := l2.checkBlocked("edit_file", `{"file_path":"internal/util/paths.go","old_text":"a","new_text":"b"}`); msg == "" {
		t.Errorf("user-direct entry must still block")
	}
	// New recording path tags user-direct.
	l2.record("never edit build/release.sh", "nl", refusalKindDirect)
	if msg := l2.checkBlocked("edit_file", `{"file_path":"build/release.sh","new_text":"x"}`); msg == "" {
		t.Errorf("fresh user-direct entry must block matching write")
	}
}

func TestRefusalCheckBlockedPath(t *testing.T) {
	l := newRefusalTestLedger(t)
	l.record("don't touch internal/auth/core.go", "nl", refusalKindDirect)
	msg := l.checkBlocked("edit_file", `{"file_path":"/repo/internal/auth/core.go","old_text":"x"}`)
	if msg == "" {
		t.Fatal("write to refused path must block")
	}
	if !strings.Contains(msg, "internal/auth/core.go") {
		t.Errorf("block message must quote the refusal excerpt: %q", msg)
	}
}

func TestRefusalCheckBlockedFlag(t *testing.T) {
	l := newRefusalTestLedger(t)
	l.record("never run git push --force", "nl", refusalKindDirect)
	if msg := l.checkBlocked("run_command", `{"command":"git push --force origin main"}`); msg == "" {
		t.Fatal("refused flag must block run_command")
	}
	if msg := l.checkBlocked("run_command", `{"command":"git push origin main"}`); msg != "" {
		t.Errorf("non-matching command must not block: %q", msg)
	}
}

func TestRefusalReadToolsNeverBlock(t *testing.T) {
	l := newRefusalTestLedger(t)
	l.record("don't touch internal/auth/core.go", "nl", refusalKindDirect)
	if msg := l.checkBlocked("read_file", `{"path":"/repo/internal/auth/core.go"}`); msg != "" {
		t.Errorf("read-class tool must never block: %q", msg)
	}
	if msg := l.checkBlocked("grep", `{"pattern":"auth","path":"internal/auth/core.go"}`); msg != "" {
		t.Errorf("grep must never block: %q", msg)
	}
}

func TestRefusalNoStructuredTargetNoBlock(t *testing.T) {
	l := newRefusalTestLedger(t)
	l.record("don't be so verbose in replies", "nl", refusalKindDirect)
	// Pure-semantic refusal: no path/flag anchor -> must not hard-block.
	if msg := l.checkBlocked("run_command", `{"command":"echo hi"}`); msg != "" {
		t.Errorf("target-less refusal must not block: %q", msg)
	}
}

func TestRefusalExpiry(t *testing.T) {
	l := newRefusalTestLedger(t)
	l.record("never run git push --force", "nl", refusalKindDirect)
	// Age the entry beyond the recency window.
	l.data.Entries[0].Ts = time.Now().Add(-31 * 24 * time.Hour).Unix()
	if msg := l.checkBlocked("run_command", `{"command":"git push --force"}`); msg != "" {
		t.Errorf("expired refusal must not block: %q", msg)
	}
}

func TestRefusalRelease(t *testing.T) {
	l := newRefusalTestLedger(t)
	l.record("don't touch internal/auth/core.go", "nl", refusalKindDirect)
	l.record("never run git push --force", "nl", refusalKindDirect)
	n := l.release("ok, you can modify internal/auth/core.go now")
	if n != 1 {
		t.Fatalf("expected 1 released, got %d", n)
	}
	if len(l.data.Entries) != 1 {
		t.Fatalf("expected 1 remaining entry, got %d", len(l.data.Entries))
	}
	if msg := l.checkBlocked("edit_file", `{"file_path":"internal/auth/core.go"}`); msg != "" {
		t.Errorf("released refusal must no longer block: %q", msg)
	}
	if msg := l.checkBlocked("run_command", `{"command":"git push --force"}`); msg == "" {
		t.Error("unrelated refusal must still block")
	}
}

func TestRefusalReleaseWithoutPhraseNoop(t *testing.T) {
	l := newRefusalTestLedger(t)
	l.record("never run git push --force", "nl", refusalKindDirect)
	// Mentions the target but is not a lift phrase -> must NOT release.
	if n := l.release("remember, git push --force is dangerous"); n != 0 {
		t.Fatalf("non-lift mention must not release, released %d", n)
	}
}

func TestRefusalPersistenceAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	l1 := newRefusalLedger(dir)
	l1.record("don't touch internal/auth/core.go", "nl", refusalKindDirect)
	path := filepath.Join(dir, ".ggcode", refusalLedgerFile)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("ledger not persisted: %v", err)
	}
	l2 := newRefusalLedger(dir) // simulates a fresh run/compaction
	if msg := l2.checkBlocked("write_file", `{"path":"internal/auth/core.go"}`); msg == "" {
		t.Fatal("refusal must survive across instances (the enforceable part)")
	}
}

func TestRefusalClearAndSummary(t *testing.T) {
	l := newRefusalTestLedger(t)
	l.record("never run git push --force", "nl", refusalKindDirect)
	l.clear()
	if len(l.data.Entries) != 0 {
		t.Fatal("clear must wipe entries")
	}
}

// Agent-level wiring (r23): the ledger is constructed with the agent,
// re-anchored on SetWorkingDir, and fed from user text via
// recordUserRefusals -- the same entry point agent.go calls after
// recordConstraints.
func TestAgentRefusalLedgerWiring(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	if a.refusalLedger == nil {
		t.Fatal("NewAgent must construct refusalLedger")
	}
	dir := t.TempDir()
	a.SetWorkingDir(dir)
	if a.refusalLedger == nil || a.refusalLedger.workingDir != dir {
		t.Fatal("SetWorkingDir must re-anchor refusalLedger")
	}
	a.recordUserRefusals("don't touch internal/auth/core.go")
	if len(a.refusalLedger.data.Entries) != 1 {
		t.Fatalf("recordUserRefusals must persist, got %d entries", len(a.refusalLedger.data.Entries))
	}
	// The enforced path must work through the Agent's ledger instance.
	if msg := a.refusalLedger.checkBlocked("edit_file", `{"file_path":"`+dir+`/internal/auth/core.go"}`); msg == "" {
		t.Fatal("wired ledger must block matching write")
	}
	// Conversational lift through the Agent entry point releases it.
	if n := a.ReleaseMatchingRefusals("ok, you can modify internal/auth/core.go now"); n != 1 {
		t.Fatalf("ReleaseMatchingRefusals must lift, got %d", n)
	}
}
