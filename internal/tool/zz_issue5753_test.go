package tool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/restart"
)

// zz_issue5753_test.go pins the arm-time half of the restart handshake: the
// OK receipt must stamp the durable arm record (target identity + sha) and
// say in-band that the outcome is verified post-restart, not by this receipt.

type issue5753Requester struct {
	called    bool
	execPath  string
	debugMode bool
}

func (f *issue5753Requester) RequestRestart(reason string, debugMode bool) {
	f.called = true
	f.debugMode = debugMode
}

func (f *issue5753Requester) ExecTargetPath() string     { return f.execPath }
func (f *issue5753Requester) ExecTargetRevision() string { return "" }

func TestIssue5753ArmStampsDurableRecord(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GGCODE_RESTART_ARM_DIR", dir)
	target := filepath.Join(t.TempDir(), "ggcode-new")
	if err := os.WriteFile(target, []byte("staged new binary for #5753"), 0o755); err != nil {
		t.Fatal(err)
	}

	req := &issue5753Requester{execPath: target}
	rt := &RestartTool{Requester: req}
	res, err := rt.Execute(context.Background(), json.RawMessage(`{"reason":"binary updated to 145acc13c7c6"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}
	if !req.called {
		t.Error("RequestRestart was not called")
	}

	// The receipt must be honest about intent-vs-outcome (#5753 ask).
	if !strings.Contains(res.Content, "Outcome handshake (#5753)") {
		t.Errorf("receipt must describe the outcome handshake: %s", res.Content)
	}
	if !strings.Contains(res.Content, "ARMED-BUT-NOT-EXECUTED") {
		t.Errorf("receipt must name the failure alarm it will raise: %s", res.Content)
	}
	if !strings.Contains(res.Content, "This OK certifies the ARMING") {
		t.Errorf("receipt must not certify the exec outcome itself: %s", res.Content)
	}

	// The durable record must carry the target's content identity.
	rec, ok := restart.LoadArmRecord()
	if !ok {
		t.Fatal("arm record was not written at arm time")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if rec.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("record sha = %s, want sha256 of target bytes", rec.SHA256)
	}
	if rec.TargetPath != target || rec.Reason != "binary updated to 145acc13c7c6" {
		t.Errorf("record target/reason mismatch: %+v", rec)
	}
}

func TestIssue5753ArmWithoutExecTargetSkipsRecord(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GGCODE_RESTART_ARM_DIR", dir)

	rt := &RestartTool{Requester: &issue5753Requester{}}
	res, err := rt.Execute(context.Background(), json.RawMessage(`{"reason":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}
	if strings.Contains(res.Content, "Outcome handshake (#5753)") {
		t.Errorf("no exec target: handshake note must be absent: %s", res.Content)
	}
	if _, ok := restart.LoadArmRecord(); ok {
		t.Error("no exec target: no arm record must be written")
	}
}
