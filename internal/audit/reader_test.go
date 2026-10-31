package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeTestLedger opens a ledger at a temp path, appends the given events,
// anchors, and closes it — the fixture every reader test uses.
func writeTestLedger(t *testing.T, path, session string, evs ...Event) []Entry {
	t.Helper()
	l, err := Open(path, session)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	var sealed []Entry
	for _, ev := range evs {
		e, err := l.Append(ev)
		if err != nil {
			t.Fatalf("append: %v", err)
		}
		sealed = append(sealed, e)
	}
	if err := l.Anchor(); err != nil {
		t.Fatalf("anchor: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return sealed
}

func TestDigestReconstructsRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	writeTestLedger(t, path, "sess-A",
		Event{Tool: "read_file", Status: StatusOK, DurationMS: 120, InputHash: "aa"},
		Event{Tool: "edit_file", Status: StatusOK, DurationMS: 300, InputHash: "bb"},
		Event{Tool: "edit_file", Status: StatusError, Err: "old_text not found in a.go", DurationMS: 80, InputHash: "cc"},
		Event{Tool: "edit_file", Status: StatusError, Err: "old_text not found in b.go", DurationMS: 90, InputHash: "dd"},
		Event{Tool: "run_command", Status: StatusUserApproved, DurationMS: 5, InputHash: "ee"},
		Event{Tool: "run_command", Status: StatusUserDenied, Err: "user said no", DurationMS: 1, InputHash: "ff"},
		Event{Tool: "git_push", Status: StatusInvalid, InvariantID: "INV-NO-FORCE-PUSH", DurationMS: 2, InputHash: "gg"},
		Event{Tool: "a2a_exec", Status: StatusOK, Peer: "order-service", TaskID: "t-9", DurationMS: 50, InputHash: "hh"},
	)

	d, err := Digest(path, "")
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	if d.Entries != 8 {
		t.Fatalf("entries = %d, want 8", d.Entries)
	}
	if !d.VerifyOK {
		t.Fatalf("chain should verify intact, got break %+v trunc %+v", d.FirstBreak, d.Truncated)
	}
	// Tool rollup: edit_file dominates and sorts first.
	if len(d.ToolStats) == 0 || d.ToolStats[0].Tool != "edit_file" {
		t.Fatalf("top tool = %+v, want edit_file", d.ToolStats)
	}
	ef := d.ToolStats[0]
	if ef.Total != 3 || ef.OK != 1 || ef.Errors != 2 {
		t.Fatalf("edit_file stat = %+v", ef)
	}
	// Approval timeline captured both human-gate decisions.
	if len(d.Approvals) != 2 {
		t.Fatalf("approvals = %d, want 2", len(d.Approvals))
	}
	if d.Approvals[0].Status != StatusUserApproved || d.Approvals[1].Status != StatusUserDenied {
		t.Fatalf("approval order/status wrong: %+v", d.Approvals)
	}
	// Invariant rejection attributed.
	if len(d.Invariants) != 1 || d.Invariants[0].InvariantID != "INV-NO-FORCE-PUSH" || d.Invariants[0].Count != 1 {
		t.Fatalf("invariants = %+v", d.Invariants)
	}
	// A2A peer attribution.
	if len(d.Peers) != 1 || d.Peers[0].Peer != "order-service" || d.Peers[0].TaskID != "t-9" || d.Peers[0].Events != 1 {
		t.Fatalf("peers = %+v", d.Peers)
	}
	// Two same-shape errors cluster together after digit normalization.
	if len(d.ErrorClusters) != 1 || d.ErrorClusters[0].Count != 2 {
		t.Fatalf("error clusters = %+v, want 1 cluster of 2", d.ErrorClusters)
	}
	// Render smoke: the report mentions the pieces a human reviewer needs.
	out := d.Render([]string{"internal/db/migrations/001_init.sql"})
	for _, want := range []string{"VERIFIED intact", "Human-gate decisions", "INV-NO-FORCE-PUSH", "order-service", "edit_file", "Blast radius: critical"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q\n%s", want, out)
		}
	}
}

func TestDigestSessionFilter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	writeTestLedger(t, path, "",
		Event{Tool: "read_file", Status: StatusOK, Session: "sess-A", DurationMS: 10, InputHash: "a"},
		Event{Tool: "read_file", Status: StatusOK, Session: "sess-B", DurationMS: 10, InputHash: "b"},
		Event{Tool: "read_file", Status: StatusOK, Session: "sess-A", DurationMS: 10, InputHash: "c"},
	)
	d, err := Digest(path, "sess-A")
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	if d.Entries != 2 || d.SkippedOther != 1 {
		t.Fatalf("entries = %d skipped = %d, want 2/1", d.Entries, d.SkippedOther)
	}
}

func TestDigestCarriesTamperState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	writeTestLedger(t, path, "",
		Event{Tool: "read_file", Status: StatusOK, DurationMS: 10, InputHash: "a"},
		Event{Tool: "run_command", Status: StatusOK, DurationMS: 10, InputHash: "b"},
	)
	// Tamper: edit the second entry's error field in place.
	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	lines[1] = strings.Replace(lines[1], `"status":"ok"`, `"err":"sneaky","status":"error"`, 1)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := Digest(path, "")
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	if d.VerifyOK {
		t.Fatal("digest should report tampered chain")
	}
	if d.FirstBreak == nil {
		t.Fatal("FirstBreak must be set when VerifyOK is false")
	}
	if !strings.Contains(d.Render(nil), "TAMPER EVIDENCE") {
		t.Error("render must surface tamper evidence")
	}
}

func TestBlastRadiusTiers(t *testing.T) {
	cases := []struct {
		files []string
		want  string
	}{
		{[]string{"README.md", "docs/guide/x.md"}, BlastCopy},
		{[]string{"internal/agent/agent.go"}, BlastCode},
		{[]string{"README.md", "cmd/app/main.go"}, BlastCode},
		{[]string{"internal/db/migrations/001.sql"}, BlastCritical},
		{[]string{"pkg/auth/token.go"}, BlastCritical},
		{[]string{".github/workflows/release.yml"}, BlastCritical},
		{[]string{"infra/deploy/terraform/main.tf"}, BlastCritical},
		{[]string{"config/secrets.yaml"}, BlastCritical},
		{nil, BlastCopy},
	}
	for _, c := range cases {
		if got := BlastRadius(c.files); got != c.want {
			t.Errorf("BlastRadius(%v) = %q, want %q", c.files, got, c.want)
		}
	}
}

func TestNormalizeErrPrefixClusters(t *testing.T) {
	a := normalizeErrPrefix("old_text not found in /tmp/build123/a.go line 45")
	b := normalizeErrPrefix("old_text not found in /tmp/build999/b.go line 87")
	if a != b {
		t.Errorf("same-shape errors should cluster:\n%q\n%q", a, b)
	}
	c := normalizeErrPrefix("connection refused")
	if c == a {
		t.Errorf("different errors must not cluster: %q", c)
	}
	if len(normalizeErrPrefix(strings.Repeat("x", 300))) > 80 {
		t.Error("prefix must be length-capped")
	}
}

func TestDigestEmptyLedger(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.jsonl") // never created: absent ledger
	d, err := Digest(path, "")
	if err != nil {
		t.Fatalf("digest on absent ledger should not hard-error: %v", err)
	}
	if d.Entries != 0 || d.FirstTime != "" {
		t.Fatalf("empty digest expected, got %+v", d)
	}
	if !d.VerifyOK {
		t.Error("absent ledger has no tamper evidence; VerifyOK should be true")
	}
}

// Wall-time span must come from entry timestamps, not entry count.
func TestDigestWallDuration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	l, _ := Open(path, "")
	l.Append(Event{Tool: "a", Status: StatusOK, DurationMS: 5, InputHash: "x"})
	// Manually control the second entry's timestamp via a direct seal path is
	// not exposed; instead verify span >= 0 and matches timestamps of entries.
	entries, _ := scanEntries(path)
	if len(entries) != 1 {
		t.Fatalf("fixture wrote %d entries", len(entries))
	}
	if _, err := time.Parse(time.RFC3339Nano, entries[0].Time); err != nil {
		t.Errorf("entry time not RFC3339Nano: %q", entries[0].Time)
	}
	l.Close()
	d, _ := Digest(path, "")
	if d.WallDurationMS < 0 {
		t.Errorf("wall duration negative: %d", d.WallDurationMS)
	}
}
