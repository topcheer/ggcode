package im

import (
	"strings"
	"testing"
)

func TestFormatApprovalRequest(t *testing.T) {
	got := FormatApprovalRequest(ToolLangEn, "read_file", `{"path":"README.md"}`)
	if got == "" {
		t.Fatal("expected non-empty approval request text")
	}
	if !strings.Contains(got, "Approval required:") {
		t.Fatalf("unexpected request text: %q", got)
	}
}

func TestFormatApprovalResult(t *testing.T) {
	got := FormatApprovalResult(ToolLangZhCN, "read_file", "allow")
	if got == "" {
		t.Fatal("expected non-empty approval result text")
	}
}
