package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// --- fakes ---

type fakeRequester struct {
	execPath  string
	requested bool
}

func (f *fakeRequester) RequestRestart(string, bool) { f.requested = true }
func (f *fakeRequester) ExecTargetPath() string      { return f.execPath }
func (f *fakeRequester) ExecTargetRevision() string  { return "testrev" }

type fakeSelfcheckTool struct {
	name    string
	content string
	isErr   bool
}

func (f *fakeSelfcheckTool) Name() string                { return f.name }
func (f *fakeSelfcheckTool) Description() string         { return "fake" }
func (f *fakeSelfcheckTool) Parameters() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (f *fakeSelfcheckTool) Execute(context.Context, json.RawMessage) (Result, error) {
	return Result{Content: f.content, IsError: f.isErr}, nil
}

// --- #5553 pre-arm smoke gate ---

// Acceptance anchor 1: an injected agent-core defect (smoke failing)
// must REFUSE the restart - no arm, no halt.
func TestRestartRefusedWhenPreArmSmokeFails(t *testing.T) {
	req := &fakeRequester{execPath: "/bin/true"}
	var probed string
	rt := &RestartTool{Requester: req, Smoke: func(_ context.Context, p string) error {
		probed = p
		return errors.New("injected zero-toolDefs defect")
	}}
	res, err := rt.Execute(context.Background(), json.RawMessage(`{"reason":"fleet swap"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected refusal (IsError)")
	}
	if req.requested {
		t.Fatalf("restart must NOT arm on failed smoke")
	}
	if res.HaltAgent {
		t.Fatalf("refusal must not halt the agent")
	}
	if probed != "/bin/true" {
		t.Fatalf("smoke must run on the exec target, got %q", probed)
	}
	for _, want := range []string{"REFUSED", "#5553", "injected zero-toolDefs defect"} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("refusal message missing %q: %s", want, res.Content)
		}
	}
}

// Acceptance anchor 2: healthy build passes the smoke and arms normally.
func TestRestartArmsWhenPreArmSmokePasses(t *testing.T) {
	req := &fakeRequester{execPath: "/bin/true"}
	rt := &RestartTool{Requester: req, Smoke: func(context.Context, string) error { return nil }}
	res, err := rt.Execute(context.Background(), json.RawMessage(`{"reason":"fleet swap"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected refusal: %s", res.Content)
	}
	if !req.requested || !res.HaltAgent {
		t.Fatalf("smoke-pass must arm (requested=%v halt=%v)", req.requested, res.HaltAgent)
	}
}

// No exec target resolvable: smoke is skipped entirely, legacy behavior.
func TestRestartSkipsSmokeWithoutExecTarget(t *testing.T) {
	req := &fakeRequester{execPath: ""}
	called := false
	rt := &RestartTool{Requester: req, Smoke: func(context.Context, string) error {
		called = true
		return errors.New("should not run")
	}}
	res, err := rt.Execute(context.Background(), json.RawMessage(`{"reason":"x"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.IsError || !req.requested {
		t.Fatalf("empty exec target must arm without smoke")
	}
	if called {
		t.Fatalf("smoke must not run without exec target")
	}
}

// --- selfCheckProbe failure classifications (crafted registries) ---

// The #5549 zero-toolDefs class: an (near-)empty registry must fail.
func TestSelfCheckProbeZeroToolDefsFails(t *testing.T) {
	reg := NewRegistry()
	err := selfCheckProbe(context.Background(), reg)
	if err == nil || !strings.Contains(err.Error(), "zero-toolDefs") {
		t.Fatalf("empty registry must fail with zero-toolDefs classification, got: %v", err)
	}
}

// Round-trip output mismatch: tool runs but the marker never appears.
func TestSelfCheckProbeRoundTripMismatchFails(t *testing.T) {
	reg := NewRegistry()
	for i := 0; i < selfCheckMinTools; i++ {
		if err := reg.Register(&fakeSelfcheckTool{name: fmt.Sprintf("filler-%d", i), content: "ok"}); err != nil {
			t.Fatalf("register filler: %v", err)
		}
	}
	if err := reg.Register(&fakeSelfcheckTool{name: "run_command", content: "unrelated output"}); err != nil {
		t.Fatalf("register run_command: %v", err)
	}
	err := selfCheckProbe(context.Background(), reg)
	if err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("mismatched round-trip must fail, got: %v", err)
	}
}

// Healthy crafted registry passes the probe.
func TestSelfCheckProbeHealthyRegistryPasses(t *testing.T) {
	reg := NewRegistry()
	for i := 0; i < selfCheckMinTools; i++ {
		if err := reg.Register(&fakeSelfcheckTool{name: fmt.Sprintf("filler-%d", i), content: "ok"}); err != nil {
			t.Fatalf("register filler: %v", err)
		}
	}
	if err := reg.Register(&fakeSelfcheckTool{name: "run_command", content: selfCheckEchoMarker}); err != nil {
		t.Fatalf("register run_command: %v", err)
	}
	if err := selfCheckProbe(context.Background(), reg); err != nil {
		t.Fatalf("healthy registry must pass: %v", err)
	}
}

// Live happy path: the REAL builtin registry boots and round-trips.
func TestSelfCheckLiveRegistry(t *testing.T) {
	if testing.Short() {
		t.Skip("boots the full builtin tool set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := SelfCheck(ctx, t.TempDir()); err != nil {
		t.Fatalf("live selfcheck must pass on a healthy build: %v", err)
	}
}

// --- RestartSmoke production runner ---

func TestRestartSmokeMissingTargetFails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := RestartSmoke(ctx, filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil || !strings.Contains(err.Error(), "stat exec target") {
		t.Fatalf("missing target must fail, got: %v", err)
	}
}

// A non-Mach-O exec target whose selfcheck exits non-zero must fail the
// smoke (codesign step is skipped for scripts via the Mach-O magic check).
func TestRestartSmokeFailsOnNonZeroSelfcheckExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fixture")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-ggcode")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := RestartSmoke(ctx, script)
	if err == nil || !strings.Contains(err.Error(), "selfcheck") {
		t.Fatalf("non-zero selfcheck exit must fail the smoke, got: %v", err)
	}
}
