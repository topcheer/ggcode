package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/tool"
)

var statsBase = time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)

// TestWilsonInterval verifies the Wilson score interval against hand-computed
// values and the NIST "zero failures != zero risk" property (upper bound).
func TestWilsonInterval(t *testing.T) {
	t.Parallel()

	// n=0 is undefined.
	if _, _, ok := wilsonInterval(0, 0, wilsonZ); ok {
		t.Fatal("n=0 should not define an interval")
	}

	// 0/10: interval must start at 0 with a strictly positive upper bound
	// well below 1 (zero observed failures does not establish zero risk).
	lo, hi, ok := wilsonInterval(0, 10, wilsonZ)
	if !ok || lo != 0 || hi <= 0 || hi >= 0.3 {
		t.Fatalf("wilson(0,10) = [%.3f, %.3f], want [0, <0.3]", lo, hi)
	}

	// 5/5: interval must end at 1 with a strictly positive lower bound.
	lo, hi, ok = wilsonInterval(5, 5, wilsonZ)
	if !ok || hi != 1 || lo < 0.5 {
		t.Fatalf("wilson(5,5) = [%.3f, %.3f], want lo>0.5, hi=1", lo, hi)
	}

	// 3/10: hand-computed Wilson 95% is about [0.107, 0.603] - it contains the point
	// estimate and is wider than the (invalid at small n) Wald interval.
	lo, hi, ok = wilsonInterval(3, 10, wilsonZ)
	if !ok || lo < 0.09 || lo > 0.13 || hi < 0.58 || hi > 0.63 {
		t.Fatalf("wilson(3,10) = [%.3f, %.3f], want ≈[0.107, 0.603]", lo, hi)
	}

	// Interval must always contain the point estimate.
	lo, hi, _ = wilsonInterval(7, 20, wilsonZ)
	p := 7.0 / 20.0
	if lo > p || hi < p {
		t.Fatalf("wilson(7,20) = [%.3f, %.3f] does not contain p=%.2f", lo, hi, p)
	}
}

// TestGuidanceStatsFireAndRepeat checks the repeat-window semantics: a tag
// re-firing inside the window counts as a repeat; outside it does not.
func TestGuidanceStatsFireAndRepeat(t *testing.T) {
	t.Parallel()
	s := newGuidanceStats()

	s.recordFire("TOOL-STORM", statsBase)
	s.recordFire("TOOL-STORM", statsBase.Add(1*time.Minute))  // repeat
	s.recordFire("TOOL-STORM", statsBase.Add(20*time.Minute)) // outside window

	st := s.tags["TOOL-STORM"]
	if st == nil || st.Fires != 3 || st.Repeats != 1 {
		t.Fatalf("TOOL-STORM = %+v, want fires=3 repeats=1", st)
	}

	// Empty tags are ignored (callers filter, but double-check).
	s.recordFire("", statsBase)
	if len(s.tags) != 1 {
		t.Fatalf("empty tag recorded: %v", s.tags)
	}
}

// TestGuidanceStatsNegativeAttribution checks the join step: negative
// signals attribute to recent unattributed fires only, each fire at most once.
func TestGuidanceStatsNegativeAttribution(t *testing.T) {
	t.Parallel()
	s := newGuidanceStats()

	s.recordFire("A", statsBase)
	s.recordFire("B", statsBase.Add(1*time.Minute))
	s.recordNegative(statsBase.Add(2 * time.Minute)) // attributes A and B

	if s.tags["A"].NegativeHits != 1 || s.tags["B"].NegativeHits != 1 {
		t.Fatalf("first negative: A=%d B=%d, want 1/1",
			s.tags["A"].NegativeHits, s.tags["B"].NegativeHits)
	}

	// Same-window repeat negative: fires already attributed — no inflation.
	s.recordNegative(statsBase.Add(3 * time.Minute))
	if s.tags["A"].NegativeHits != 1 || s.tags["B"].NegativeHits != 1 {
		t.Fatal("second negative double-attributed existing fires")
	}

	// New fire, later negative inside the window.
	s.recordFire("A", statsBase.Add(4*time.Minute))
	s.recordNegative(statsBase.Add(5 * time.Minute))
	if s.tags["A"].NegativeHits != 2 {
		t.Fatalf("A negative hits = %d, want 2", s.tags["A"].NegativeHits)
	}

	// Negative with no recent fires still counts as a negative signal.
	s.recordNegative(statsBase.Add(30 * time.Minute))
	if s.negatives != 4 {
		t.Fatalf("negatives = %d, want 4", s.negatives)
	}
}

// TestGuidanceStatsReportVerdicts checks the evidence-aware verdict policy.
func TestGuidanceStatsReportVerdicts(t *testing.T) {
	t.Parallel()
	s := newGuidanceStats()

	// noisy: 10 consecutive fires (9 repeats), negative attributes the 5
	// fires within the attribution window (minutes 5-9).
	for i := 0; i < 10; i++ {
		s.recordFire("NOISY", statsBase.Add(time.Duration(i)*time.Minute))
	}
	s.recordNegative(statsBase.Add(11 * time.Minute))
	// clean: 36 fires spaced beyond the repeat window, no negatives -
	// enough n for the zero-hit upper bound to drop below 0.1.
	for i := 0; i < 36; i++ {
		s.recordFire("CLEAN", statsBase.Add(time.Duration(60+i*11)*time.Minute))
	}
	// tiny: 3 fires only.
	for i := 0; i < 3; i++ {
		s.recordFire("TINY", statsBase.Add(time.Duration(120+i)*time.Minute))
	}

	byTag := map[string]GuidanceTagReport{}
	for _, r := range s.report() {
		byTag[r.Tag] = r
	}

	if n := guidancetagNotes(byTag["TINY"]); !strings.Contains(n, "insufficient evidence") {
		t.Fatalf("TINY notes = %q, want insufficient evidence", n)
	}
	noisy := byTag["NOISY"]
	if noisy.Repeats != 9 || noisy.NegativeHits != 5 {
		t.Fatalf("NOISY = %+v", noisy)
	}
	// 9/10 repeats -> Wilson lo ~0.60 crosses the 0.5 flag threshold.
	if n := guidancetagNotes(noisy); !strings.Contains(n, "rarely sticks") {
		t.Fatalf("NOISY notes = %q (lo=%.2f), want repeat flag", n, noisy.RepeatLo)
	}
	clean := byTag["CLEAN"]
	if n := guidancetagNotes(clean); !strings.Contains(n, "clean") || !strings.Contains(n, "95% upper") {
		t.Fatalf("CLEAN notes = %q, want clean + upper bound", n)
	}
}

// TestGuidanceStatsPersistAndAggregate covers the JSONL round trip: persist
// once per run, aggregate across runs, skip malformed lines.
func TestGuidanceStatsPersistAndAggregate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	s := newGuidanceStats()
	s.recordFire("TAG-A", statsBase)
	s.recordFire("TAG-A", statsBase.Add(time.Minute))
	s.recordNegative(statsBase.Add(2 * time.Minute))
	s.recordFire("TAG-B", statsBase)

	s.persist(dir)
	s.persist(dir) // once-guard: must not append twice

	path := filepath.Join(dir, ".ggcode", guidanceStatsFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read persisted file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 JSONL line after double persist, got %d", len(lines))
	}
	var rec guidanceStatsLine
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rec.Tags["TAG-A"] == nil || rec.Tags["TAG-A"].Fires != 2 || rec.Tags["TAG-A"].Repeats != 1 || rec.Tags["TAG-A"].NegativeHits != 2 {
		t.Fatalf("persisted TAG-A = %+v", rec.Tags["TAG-A"])
	}

	// Second run with malformed line noise in the file.
	s2 := newGuidanceStats()
	s2.recordFire("TAG-A", statsBase)
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("{not json}\n")
	f.Close()
	s2.persist(dir)

	reports, runs, err := AggregateGuidanceStats(dir)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if runs != 2 {
		t.Fatalf("runs = %d, want 2 (malformed line skipped)", runs)
	}
	var aggA *GuidanceTagReport
	for i := range reports {
		if reports[i].Tag == "TAG-A" {
			aggA = &reports[i]
		}
	}
	if aggA == nil || aggA.Fires != 3 {
		t.Fatalf("aggregated TAG-A = %+v, want fires=3", aggA)
	}

	// Missing file → no error, zero runs.
	reports, runs, err = AggregateGuidanceStats(filepath.Join(dir, "nonexistent"))
	if err != nil || runs != 0 || reports != nil {
		t.Fatalf("missing file aggregate = (%v, %d, %v)", reports, runs, err)
	}
}

// TestGuidanceStatsNilSafety ensures the hooks are no-ops on zero-value /
// nil receivers (test-constructed Agents without stats).
func TestGuidanceStatsNilSafety(t *testing.T) {
	t.Parallel()
	var s *guidanceStats
	s.recordFire("X", statsBase) // must not panic
	s.recordNegative(statsBase)
	if r := s.report(); r != nil {
		t.Fatalf("nil stats report = %v", r)
	}

	a := &Agent{} // no guidanceStats field set
	a.recordGuidanceFire("[TAG-X] text")
	a.recordGuidanceNegative() // must not panic
}

// TestGuidanceHookIntegration verifies the two delivery paths actually
// record fires after budget gating.
func TestGuidanceHookIntegration(t *testing.T) {
	t.Parallel()

	// Tool-result hint path via appendGuidance.
	a := &Agent{guidanceStats: newGuidanceStats()}
	res := &tool.Result{}
	if !a.appendGuidance(res, "[HOOK-TAG] watch out") {
		t.Fatal("appendGuidance should deliver on a fresh budget")
	}
	if got := a.guidanceStats.tags["HOOK-TAG"]; got == nil || got.Fires != 1 {
		t.Fatalf("appendGuidance hook: HOOK-TAG = %+v", got)
	}
	// Untagged guidance is not tracked.
	a.appendGuidance(res, "no tag here")
	if len(a.guidanceStats.tags) != 1 {
		t.Fatalf("untagged guidance tracked: %v", a.guidanceStats.tags)
	}

	// Iteration-level path via injectGuidance.
	a2 := &Agent{
		contextManager: ctxpkg.NewManager(100000),
		guidanceStats:  newGuidanceStats(),
	}
	if !a2.injectGuidance("[ITER-TAG] reconsider") {
		t.Fatal("injectGuidance should deliver on a fresh budget")
	}
	if got := a2.guidanceStats.tags["ITER-TAG"]; got == nil || got.Fires != 1 {
		t.Fatalf("injectGuidance hook: ITER-TAG = %+v", got)
	}
}
