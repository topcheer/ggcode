package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/audit"
)

func writeLedgerForGate(t *testing.T, path string, tamper bool) {
	t.Helper()
	l, err := audit.Open(path, "sess-gate")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := l.Append(audit.Event{Tool: "read_file", Status: audit.StatusOK, InputHash: "aa"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := l.Anchor(); err != nil {
		t.Fatalf("anchor: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if !tamper {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	edited := strings.Replace(string(raw), "\"read_file\"", "\"write_file\"", 1)
	if edited == string(raw) {
		t.Fatalf("tamper probe did not change the ledger")
	}
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestAuditReportStrictGate(t *testing.T) {
	dir := t.TempDir()

	intact := filepath.Join(dir, "intact.jsonl")
	writeLedgerForGate(t, intact, false)
	tampered := filepath.Join(dir, "tampered.jsonl")
	writeLedgerForGate(t, tampered, true)

	// Intact ledger: exit code stays 0 with and without --strict.
	for _, strict := range []bool{false, true} {
		cmd := newAuditCmd()
		args := []string{"report", "--path", intact}
		if strict {
			args = append(args, "--strict")
		}
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("intact ledger strict=%v: unexpected error: %v", strict, err)
		}
	}

	// Tampered ledger: without --strict the report prints TAMPER EVIDENCE but
	// exits 0 (human-readable default); with --strict it exits non-zero.
	cmd := newAuditCmd()
	cmd.SetArgs([]string{"report", "--path", tampered})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("tampered ledger without --strict: unexpected error: %v", err)
	}

	cmd = newAuditCmd()
	cmd.SetArgs([]string{"report", "--path", tampered, "--strict"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("tampered ledger with --strict: expected non-zero exit, got nil")
	}
}
