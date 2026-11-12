package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/audit"
)

func vizTestLedger(t *testing.T) (string, []audit.Entry) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	l, err := audit.Open(path, "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for _, ev := range []audit.Event{
		{Tool: "edit_file", Status: "ok", InputHash: "a", Session: "s1", Rationale: "fix login: <script>alert('xss')</script> stale cache"},
		{Tool: "run_command", Status: "invalid", InputHash: "b", Session: "s1", Err: "blocked by invariant", InvariantID: "inv-1"},
		{Tool: "grep", Status: "ok", InputHash: "c", Session: "s2"},
	} {
		if _, err := l.Append(ev); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	entries, err := audit.Timeline(path, "")
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	return path, entries
}

// TestAuditVizEscapesRationale pins the XSS line of defense: a malicious
// rationale must render escaped, never as live script tags.
func TestAuditVizEscapesRationale(t *testing.T) {
	path, entries := vizTestLedger(t)
	rep, err := audit.Verify(path)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	htm := renderAuditViz(path, "", entries, rep)
	if strings.Contains(htm, "<script>alert") {
		t.Fatal("raw script tag leaked into HTML")
	}
	if !strings.Contains(htm, "&lt;script&gt;alert(&#39;xss&#39;)&lt;/script&gt;") {
		t.Fatal("rationale not escaped as expected")
	}
	if !strings.Contains(htm, "Chain VERIFIED") {
		t.Fatal("clean chain must show VERIFIED banner")
	}
	if !strings.Contains(htm, "st-denied") || !strings.Contains(htm, "inv-1") {
		t.Fatal("denied entry class / invariant badge missing")
	}
}

// TestAuditVizTamperBannerDegrades pins the loud-degrade contract: a
// broken chain must show a FAILED banner, never a silent pretty view.
func TestAuditVizTamperBannerDegrades(t *testing.T) {
	path, entries := vizTestLedger(t)
	// Tamper: flip a byte inside the first data line.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	idx := strings.Index(string(data), `"tool":"edit_file"`)
	if idx < 0 {
		t.Fatal("anchor not found")
	}
	data[idx] = 'X'
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	rep, err := audit.Verify(path)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	htm := renderAuditViz(path, "", entries, rep)
	if !strings.Contains(htm, "Chain verification FAILED") {
		t.Fatal("tampered chain must degrade the banner")
	}
}
