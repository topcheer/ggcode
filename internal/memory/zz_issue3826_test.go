package memory

// #3826 probes: strong intent prefixes win over weak transient infixes in
// classification, and `summary` is no longer stripped from dedup keys.

import (
	"testing"
	"time"
)

func TestIssue3826_FixInfixDoesNotHijackPersistentPrefix(t *testing.T) {
	for _, key := range []string{"release-fix-runbook", "build-fix-guide"} {
		if got := classifyMemory(key); got != CategoryPersistent {
			t.Fatalf("%s classified %v, want persistent (strong ^prefix beats -fix- infix)", key, got)
		}
	}
}

func TestIssue3826_TransientShapesStillTransient(t *testing.T) {
	// No persistent/evolving intent: the transient shapes keep their class.
	for _, key := range []string{"impl-task-auth", "session-fix", "session-fix-bug"} {
		if got := classifyMemory(key); got != CategoryTransient {
			t.Fatalf("%s classified %v, want transient", key, got)
		}
	}
	if got := classifyMemory("competitor-analysis"); got != CategoryEvolving {
		t.Fatalf("evolving prefix lost: %v", got)
	}
}

func TestIssue3826_PersistentRunbookNotExpired(t *testing.T) {
	// The issue's victim: a 40-day-old release-fix-runbook must NOT expire.
	meta := MemoryMeta{Category: classifyMemory("release-fix-runbook")}
	if shouldExpire(meta, time.Now().AddDate(0, 0, 40)) {
		t.Fatal("persistent release-fix-runbook must never expire")
	}
}

func TestIssue3826_SummaryNotStrippedFromDedupKey(t *testing.T) {
	// research-summary-methods keeps its semantic `summary` segment and no
	// longer collapses onto the unrelated research-methods key.
	if got, want := dedupKeyFor("research-summary-methods"), "research-summary-methods"; got != want {
		t.Fatalf("dedupKeyFor = %q, want %q (summary must survive)", got, want)
	}
	if got, want := dedupKeyFor("competitor-analysis-2026-07-13-r3"), "competitor-analysis"; got != want {
		t.Fatalf("true version segments still stripped: got %q, want %q", got, want)
	}
}
