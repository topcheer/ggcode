package swarm

// Regression tests for GitHub issue #2786: the permanent-failure parking
// paths (quota/auth failure, transient retry cap, panic retry cap) mark the
// failed task StatusCompleted so it is never re-claimed (#1295) — but
// allBlockersComplete only reads Status, so a parked failure "satisfied" its
// BlockedBy edges and teammates executed dependents against output that
// never existed. The fix propagates failure along the reverse-dependency
// closure (OrchestraBench, arXiv:2608.05263: a failed node must fail its
// downstream, not unlock it).

import (
	"testing"

	"github.com/topcheer/ggcode/internal/task"
)

// chainBoard builds A ← B ← C, D ← (B,C), and an independent E.
func chainBoard(t *testing.T) (*task.Manager, map[string]string) {
	t.Helper()
	mgr := task.NewManager()
	a := mgr.Create("A", "", "", nil)
	b := mgr.Create("B", "", "", nil)
	c := mgr.Create("C", "", "", nil)
	d := mgr.Create("D", "", "", nil)
	e := mgr.Create("E", "", "", nil)
	for id, deps := range map[string][]string{
		b.ID: {a.ID},
		c.ID: {b.ID},
		d.ID: {b.ID, c.ID},
	} {
		if _, err := mgr.Update(id, task.UpdateOptions{AddBlockedBy: deps}); err != nil {
			t.Fatalf("wire deps: %v", err)
		}
	}
	return mgr, map[string]string{"A": a.ID, "B": b.ID, "C": c.ID, "D": d.ID, "E": e.ID}
}

// Parking a permanently failed task must park its whole downstream closure:
// B and C transitively, the diamond D, and NOT the independent E.
func TestIssue2786_FailureCascadesToDependents(t *testing.T) {
	mgr, ids := chainBoard(t)

	parkTaskFailed(mgr, ids["A"], "quota_exhausted", "boom", parkOpts{})

	for _, label := range []string{"B", "C", "D"} {
		got, ok := mgr.Get(ids[label])
		if !ok {
			t.Fatalf("%s vanished", label)
		}
		if got.Status != task.StatusCompleted {
			t.Fatalf("%s not parked by cascade, status=%v", label, got.Status)
		}
		if got.Metadata["permanent_error"] != "dependency_failed" {
			t.Fatalf("%s missing dependency_failed marker: %v", label, got.Metadata)
		}
		if got.Owner != "" {
			t.Fatalf("%s owner not cleared by cascade: %q", label, got.Owner)
		}
	}
	a, _ := mgr.Get(ids["A"])
	if a.Metadata["permanent_error"] != "quota_exhausted" {
		t.Fatalf("root failure marker wrong: %v", a.Metadata)
	}
	e, _ := mgr.Get(ids["E"])
	if e.Status != task.StatusPending {
		t.Fatalf("independent E must stay pending, got %v", e.Status)
	}
}

// A dependent that already completed successfully before the cascade must
// not be overwritten, and its own dependents stay claimable.
func TestIssue2786_CascadeSkipsTerminalDependents(t *testing.T) {
	mgr, ids := chainBoard(t)
	bDone := task.StatusCompleted
	if _, err := mgr.Update(ids["B"], task.UpdateOptions{Status: &bDone, Metadata: map[string]string{"result": "ok"}}); err != nil {
		t.Fatal(err)
	}

	parkTaskFailed(mgr, ids["A"], "max_retries_exceeded", "boom", parkOpts{})

	b, _ := mgr.Get(ids["B"])
	if b.Metadata["permanent_error"] != "" || b.Metadata["result"] != "ok" {
		t.Fatalf("successful B overwritten by cascade: %v", b.Metadata)
	}
	c, _ := mgr.Get(ids["C"])
	if c.Status != task.StatusPending {
		t.Fatalf("C depends only on the successful B and must stay pending, got %v", c.Status)
	}
}

// The panic-rollback parking variant keeps its #2579 ExpectedStatus guard:
// parking an already-terminal task must fail the guard AND cascade nothing.
func TestIssue2786_RollbackParkingGuardStopsCascade(t *testing.T) {
	mgr, ids := chainBoard(t)
	bDone := task.StatusCompleted
	if _, err := mgr.Update(ids["B"], task.UpdateOptions{Status: &bDone}); err != nil {
		t.Fatal(err)
	}

	inProgress := task.StatusInProgress
	parkTaskFailed(mgr, ids["B"], "max_retries_exceeded", "teammate panicked repeatedly",
		parkOpts{extra: map[string]string{"retry_attempts": "3"}, expected: &inProgress})

	b, _ := mgr.Get(ids["B"])
	if b.Status != task.StatusCompleted || b.Metadata["permanent_error"] != "" {
		t.Fatalf("#2579 guard violated: completed B was re-parked: status=%v meta=%v", b.Status, b.Metadata)
	}
	c, _ := mgr.Get(ids["C"])
	if c.Status != task.StatusPending {
		t.Fatalf("no-op parking must not cascade, C got %v", c.Status)
	}
}

// End-to-end claim semantics: after the cascade, a teammate must not pick up
// a dependent whose dependency permanently failed.
func TestIssue2786_ParkedDependentNotClaimable(t *testing.T) {
	mgr, ids := chainBoard(t)

	parkTaskFailed(mgr, ids["A"], "auth_failure", "401", parkOpts{})

	b, _ := mgr.Get(ids["B"])
	if b.Status != task.StatusCompleted {
		t.Fatalf("B should be parked, got %v", b.Status)
	}
	// tryClaimPendingTask scans for pending tasks; B/C/D are completed now,
	// so nothing in the failed closure is claimable. Only E is left pending,
	// and it has no unmet dependency.
	for _, tk := range mgr.List() {
		if tk.Status == task.StatusPending && !allBlockersComplete(mgr, tk) {
			t.Fatalf("task %s pending with unmet deps would be claimed by mistake", tk.ID)
		}
	}
}
