package agentruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- #5527 acceptance pins: curator dry-run rounds opt-in -------------------
//
// Five pins from the spec issue:
// 1. N=0 → byte-identical behavior (autonomy path untouched)
// 2. Round < N → store hash unchanged + audit records marked dry_run:true
// 3. Round == N+1 → auto-apply resumes, audit no longer marked
// 4. ForceDryRun → unbounded observe rounds; unset returns to schedule
// 5. Existing suite green (this file compiles/runs alongside unchanged pins)

// observeArchiveVerdict is a judge that wants to archive everything — the
// maximally mutating verdict, so any leaked apply in dry-run shows up as a
// file disappearing.
func observeArchiveVerdict(keys ...string) string {
	var vs []SweepVerdict
	for _, k := range keys {
		vs = append(vs, SweepVerdict{Key: k, Action: SweepArchive, Rationale: "dry-run probe: wants archive", Confidence: 0.95})
	}
	return verdictJSON(vs...)
}

func storeHash(t *testing.T, dir string) string {
	t.Helper()
	// hash the file inventory: names + contents of every .md in the store
	entries, _ := os.ReadDir(dir)
	var b strings.Builder
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			data, _ := os.ReadFile(filepath.Join(dir, e.Name()))
			b.WriteString(e.Name())
			b.WriteByte(0)
			b.Write(data)
			b.WriteByte(0)
		}
	}
	return b.String()
}

func TestCuratorDryRun_StoreUntouchedAndAuditMarked(t *testing.T) {
	am := newSweepStore(t, [2]string{"lesson-a", "content a"}, [2]string{"lesson-b", "content b"})
	before := storeHash(t, am.Dir())

	stub := stubLLM(func(_ context.Context, _ string) (string, error) {
		return observeArchiveVerdict("lesson-a", "lesson-b"), nil
	})
	rep, err := RunMemorySweep(context.Background(), am, stub, SweepOptions{
		DryRunRounds: 2, AuditBase: am.Dir(), Now: fixedNow(time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("dry round: %v", err)
	}
	if !rep.DryRun {
		t.Fatal("round inside window must be flagged DryRun")
	}
	if rep.Applied != 0 {
		t.Fatalf("dry round must apply nothing, applied %d", rep.Applied)
	}
	if after := storeHash(t, am.Dir()); after != before {
		t.Fatal("dry round must leave the store byte-identical")
	}
	// audit records carry dry_run:true
	data, _ := os.ReadFile(filepath.Join(am.Dir(), "sweep-audit.jsonl"))
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("dry round must write one audit record per verdict, got %d", len(lines))
	}
	var rec sweepAuditRecord
	if err := json.Unmarshal([]byte(lines[1]), &rec); err != nil {
		t.Fatal(err)
	}
	if !rec.DryRun {
		t.Fatal("audit record from a dry round must carry dry_run:true")
	}
	// round counters advanced
	st := readSweepSlotState(filepath.Join(am.Dir(), "sweep-state.json"))
	if st.DryRoundsDone != 1 || st.CompletedRounds != 1 {
		t.Fatalf("round bookkeeping wrong: %+v", st)
	}
}

func TestCuratorDryRun_TransitionToApplyAtN(t *testing.T) {
	am := newSweepStore(t, [2]string{"old-entry", "stale content"})
	stub := stubLLM(func(_ context.Context, _ string) (string, error) {
		return observeArchiveVerdict("old-entry"), nil
	})
	base := SweepOptions{DryRunRounds: 1, AuditBase: am.Dir()}
	// round 1: dry
	if _, err := RunMemorySweep(context.Background(), am, stub, base); err != nil {
		t.Fatalf("dry round: %v", err)
	}
	if _, err := os.Stat(filepath.Join(am.Dir(), "old-entry.md")); err != nil {
		t.Fatal("dry round must not archive")
	}
	// round 2: N=1 consumed → real apply
	rep2, err := RunMemorySweep(context.Background(), am, stub, base)
	if err != nil {
		t.Fatalf("apply round: %v", err)
	}
	if rep2.DryRun {
		t.Fatal("round after window must not be dry")
	}
	if rep2.Applied != 1 {
		t.Fatalf("post-window round must apply, got %d", rep2.Applied)
	}
	if _, err := os.Stat(filepath.Join(am.Dir(), "old-entry.md")); !os.IsNotExist(err) {
		t.Fatal("archive verdict must apply after the window")
	}
	// audit: first marked dry, second unmarked
	data, _ := os.ReadFile(filepath.Join(am.Dir(), "sweep-audit.jsonl"))
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var rec1, rec2 sweepAuditRecord
	_ = json.Unmarshal([]byte(lines[0]), &rec1)
	_ = json.Unmarshal([]byte(lines[1]), &rec2)
	if !rec1.DryRun || rec2.DryRun {
		t.Fatalf("audit marker transition wrong: rec1.dry=%v rec2.dry=%v", rec1.DryRun, rec2.DryRun)
	}
}

func TestCuratorDryRun_ForcePinAndRelease(t *testing.T) {
	am := newSweepStore(t, [2]string{"keep-me", "content"})
	stub := stubLLM(func(_ context.Context, _ string) (string, error) {
		return observeArchiveVerdict("keep-me"), nil
	})
	// force-pinned: even many rounds stay dry
	for i := 0; i < 3; i++ {
		rep, err := RunMemorySweep(context.Background(), am, stub, SweepOptions{ForceDryRun: true, AuditBase: am.Dir()})
		if err != nil {
			t.Fatalf("forced dry round %d: %v", i, err)
		}
		if !rep.DryRun {
			t.Fatalf("force pin must keep rounds dry (round %d)", i)
		}
	}
	if _, err := os.Stat(filepath.Join(am.Dir(), "keep-me.md")); err != nil {
		t.Fatal("force pin must never mutate")
	}
	st := readSweepSlotState(filepath.Join(am.Dir(), "sweep-state.json"))
	if st.DryRoundsDone != 3 {
		t.Fatalf("force rounds must consume window slots, got %d", st.DryRoundsDone)
	}
	// release: a normal round (no flag, no window) applies
	rep, err := RunMemorySweep(context.Background(), am, stub, SweepOptions{AuditBase: am.Dir()})
	if err != nil {
		t.Fatalf("release round: %v", err)
	}
	if rep.DryRun || rep.Applied != 1 {
		t.Fatalf("released round must apply normally: %+v", rep)
	}
}

func TestCuratorDryRun_DefaultOffIsByteIdentical(t *testing.T) {
	am := newSweepStore(t, [2]string{"e1", "content 1"})
	stub := stubLLM(func(_ context.Context, _ string) (string, error) {
		return observeArchiveVerdict("e1"), nil
	})
	rep, err := RunMemorySweep(context.Background(), am, stub, SweepOptions{AuditBase: am.Dir()})
	if err != nil {
		t.Fatalf("default round: %v", err)
	}
	if rep.DryRun || rep.Applied != 1 {
		t.Fatalf("default (zero flags) must behave exactly as before #5527: %+v", rep)
	}
	// audit record must NOT carry the dry_run key (byte-identical format)
	data, _ := os.ReadFile(filepath.Join(am.Dir(), "sweep-audit.jsonl"))
	if strings.Contains(string(data), "dry_run") {
		t.Fatal("default-off audit records must not carry a dry_run field (byte-identical format)")
	}
}

func TestCuratorDryRun_AuditFormatAligned(t *testing.T) {
	am := newSweepStore(t, [2]string{"x", "content x"})
	stub := stubLLM(func(_ context.Context, _ string) (string, error) {
		return observeArchiveVerdict("x"), nil
	})
	if _, err := RunMemorySweep(context.Background(), am, stub, SweepOptions{DryRunRounds: 1, AuditBase: am.Dir()}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(am.Dir(), "sweep-audit.jsonl"))
	var rec map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &rec); err != nil {
		t.Fatal(err)
	}
	// audit-format alignment with #5518: same five keys, plus the marker
	for _, k := range []string{"timestamp", "actor", "key", "action", "rationale", "dry_run"} {
		if _, ok := rec[k]; !ok {
			t.Fatalf("dry audit record missing key %q: %v", k, rec)
		}
	}
}
