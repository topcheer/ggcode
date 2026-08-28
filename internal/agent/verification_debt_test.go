package agent

import (
	"testing"
)

func TestVerificationDebt_NoWarnBelowThreshold(t *testing.T) {
	v := newVerificationDebtState()
	// 4 modifications -- below threshold of 5
	for i := 0; i < 4; i++ {
		v.recordToolCall("edit_file", `{"path":"f.go"}`)
	}
	if msg := v.maybeWarn(); msg != "" {
		t.Fatalf("expected no warning for 4 modifications, got: %s", msg)
	}
}

func TestVerificationDebt_WarnsAtThreshold(t *testing.T) {
	v := newVerificationDebtState()
	// Need >=6 total calls and >=5 unverified modifications
	for i := 0; i < 5; i++ {
		v.recordToolCall("edit_file", `{"path":"f.go"}`)
	}
	v.recordToolCall("read_file", `{"path":"other.go"}`)
	// After one read, debt is 4 (5-1), need one more edit
	v.recordToolCall("edit_file", `{"path":"f.go"}`)
	if msg := v.maybeWarn(); msg == "" {
		t.Fatal("expected warning for 5+ unverified modifications")
	}
}

func TestVerificationDebt_VerificationResetsDebt(t *testing.T) {
	v := newVerificationDebtState()
	// Stack 6 modifications
	for i := 0; i < 6; i++ {
		v.recordToolCall("edit_file", `{"path":"f.go"}`)
	}
	// Run a build -- should reset debt
	v.recordToolCall("run_command", `{"command":"go build ./..."}`)
	if msg := v.maybeWarn(); msg != "" {
		t.Fatalf("expected no warning after verification, got: %s", msg)
	}
	if v.debt != 0 {
		t.Fatalf("expected debt=0 after verification, got %d", v.debt)
	}
}

func TestVerificationDebt_GroundingReducesDebt(t *testing.T) {
	v := newVerificationDebtState()
	v.recordToolCall("edit_file", `{"path":"f.go"}`)
	v.recordToolCall("edit_file", `{"path":"g.go"}`)
	// read_file is grounding -- should reduce debt by 1
	v.recordToolCall("read_file", `{"path":"g.go"}`)
	if v.debt != 1 {
		t.Fatalf("expected debt=1 after grounding, got %d", v.debt)
	}
}

func TestVerificationDebt_MaxWarningsPerRun(t *testing.T) {
	v := newVerificationDebtState()
	// Build up debt
	for i := 0; i < 10; i++ {
		v.recordToolCall("edit_file", `{"path":"f.go"}`)
	}

	msg1 := v.maybeWarn()
	if msg1 == "" {
		t.Fatal("expected first warning")
	}
	msg2 := v.maybeWarn()
	if msg2 == "" {
		t.Fatal("expected second warning")
	}
	msg3 := v.maybeWarn()
	if msg3 != "" {
		t.Fatal("expected no third warning (max 2 per run)")
	}
}

func TestVerificationDebt_MinTotalBeforeWarn(t *testing.T) {
	v := newVerificationDebtState()
	// Only 3 total calls -- below minTotal of 6
	for i := 0; i < 3; i++ {
		v.recordToolCall("edit_file", `{"path":"f.go"}`)
	}
	if msg := v.maybeWarn(); msg != "" {
		t.Fatalf("expected no warning below minTotal, got: %s", msg)
	}
}

func TestVerificationDebt_Reset(t *testing.T) {
	v := newVerificationDebtState()
	for i := 0; i < 10; i++ {
		v.recordToolCall("edit_file", `{"path":"f.go"}`)
	}
	v.reset()
	if v.totalCalls != 0 || v.debt != 0 || v.modifyCount != 0 {
		t.Fatal("reset did not clear state")
	}
}

func TestVerificationDebt_RunCommandNonVerifyIsNeutral(t *testing.T) {
	v := newVerificationDebtState()
	for i := 0; i < 5; i++ {
		v.recordToolCall("edit_file", `{"path":"f.go"}`)
	}
	// echo is not a verification command
	v.recordToolCall("run_command", `{"command":"echo hello"}`)
	if v.verifyCount != 0 {
		t.Fatal("non-verification run_command should not increment verifyCount")
	}
}

func TestClassifyDebtAction(t *testing.T) {
	tests := []struct {
		tool string
		args string
		want debtAction
	}{
		{"read_file", "{}", debtGrounding},
		{"grep", "{}", debtGrounding},
		{"edit_file", "{}", debtModifying},
		{"write_file", "{}", debtModifying},
		{"multi_edit_file", "{}", debtModifying},
		{"run_command", `{"command":"go build"}`, debtVerifying},
		{"run_command", `{"command":"go test"}`, debtVerifying},
		{"run_command", `{"command":"echo hi"}`, debtNeutral},
		{"lsp_diagnostics", "{}", debtVerifying},
		{"git_status", "{}", debtNeutral},
	}
	for _, tc := range tests {
		got := classifyDebtAction(tc.tool, tc.args)
		if got != tc.want {
			t.Errorf("classifyDebtAction(%q, %q) = %v, want %v", tc.tool, tc.args, got, tc.want)
		}
	}
}

func TestIsVerificationCommand(t *testing.T) {
	verifyCmds := []string{
		"go build ./...",
		"go test ./...",
		"npm run build",
		"make verify-ci",
		"cargo test",
		"pytest -v",
		"go vet ./...",
		// Env-prefixed verification commands stay recognized.
		"GOFLAGS=-p=1 go test ./...",
	}
	for _, cmd := range verifyCmds {
		if !isVerificationCommand(cmd) {
			t.Errorf("isVerificationCommand(%q) = false, want true", cmd)
		}
	}
	nonVerifyCmds := []string{
		"echo hello",
		"ls -la",
		"cat file.txt",
		"git status",
		// #1224: noun/prefix misfires - none of these verify anything, yet
		// the old anywhere-token / trimmed-prefix matching classified them as
		// verification and silently reset the edit-abandonment detector.
		"rm -rf build",
		"mkdir test",
		"ls test",
		"cat build",
		"mv x check",
		"cd test && ./run.sh",
		"cargo clean",
		"cargo fmt",
		"yarn install",
		"yarn remove lodash",
		"makefile-parser input.mk",
		"git commit -m \"now go test passes\"",
	}
	for _, cmd := range nonVerifyCmds {
		if isVerificationCommand(cmd) {
			t.Errorf("isVerificationCommand(%q) = true, want false", cmd)
		}
	}
}

func TestVerificationDebt_RealisticScenario(t *testing.T) {
	v := newVerificationDebtState()
	// Agent reads a file (grounding), edits it, reads another, edits, edits,
	// edits, edits -- debt accumulates because no build/test in between.
	v.recordToolCall("read_file", `{"path":"main.go"}`)
	v.recordToolCall("edit_file", `{"path":"main.go"}`)
	v.recordToolCall("read_file", `{"path":"util.go"}`)
	v.recordToolCall("edit_file", `{"path":"util.go"}`)
	v.recordToolCall("edit_file", `{"path":"util.go"}`)
	v.recordToolCall("edit_file", `{"path":"helper.go"}`)
	// At this point: total=6, debt=3 (2 edits after first read, 1 grounding
	// reduced from 2->1, then 3 more edits = 4... let me trace:
	// read: total=1, debt=0
	// edit: total=2, debt=1
	// read: total=3, debt=0 (grounding reduces 1->0)
	// edit: total=4, debt=1
	// edit: total=5, debt=2
	// edit: total=6, debt=3
	if v.debt != 3 {
		t.Errorf("expected debt=3, got %d", v.debt)
	}
	// Below threshold of 5
	if msg := v.maybeWarn(); msg != "" {
		t.Fatalf("expected no warning at debt=3, got: %s", msg)
	}
	// Two more edits push debt to 5
	v.recordToolCall("edit_file", `{"path":"a.go"}`)
	v.recordToolCall("edit_file", `{"path":"b.go"}`)
	if v.debt != 5 {
		t.Errorf("expected debt=5, got %d", v.debt)
	}
	if msg := v.maybeWarn(); msg == "" {
		t.Fatal("expected warning at debt=5")
	}
}
