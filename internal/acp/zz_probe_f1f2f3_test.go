package acp

// Probe tests for claims F1/F2/F3 against internal/acp/client.go.
// Read-only investigation: this file adds tests only, no source changes.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/permission"
)

const (
	probeCrashHelperEnv    = "GGCODE_TEST_PROBE_CRASH_HELPER"
	probeApprovalHelperEnv = "GGCODE_TEST_PROBE_APPROVAL_HELPER"
	probeHOLHelperEnv      = "GGCODE_TEST_PROBE_HOL_HELPER"
	probeHOLMarkerEnv      = "GGCODE_TEST_PROBE_HOL_MARKER"
)

func probeClient(t *testing.T, env string, helper string) *Client {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	t.Setenv(env, "1")
	return NewClient(
		DiscoveredAgent{
			Def: AgentDef{
				Name:       helper,
				ACPCommand: []string{"-test.run=" + helper, "--"},
			},
			Path: exe,
		},
		t.TempDir(),
		nil, // nil policy: Ask falls through to onApproval
		nil,
	)
}

// ---------- F3: crash does not clear running/sessionID; EnsureReady lies ----------

func TestProbeF3_CrashLeavesStaleReadyState(t *testing.T) {
	c := probeClient(t, probeCrashHelperEnv, "TestProbeCrashHelperProcess")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Close()
	if err := c.NewSession(ctx, c.workingDir); err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	// Helper exits(1) on session/prompt -> simulates OOM crash.
	_, err1 := c.Prompt(ctx, "go")
	if err1 == nil {
		t.Fatal("expected prompt #1 to fail after crash")
	}
	t.Logf("prompt #1 error: %v", err1)

	// Give readLoop a moment to fully exit (done closed).
	deadline := time.Now().Add(2 * time.Second)
	c.mu.Lock()
	done := c.done
	c.mu.Unlock()
	for done != nil {
		select {
		case <-done:
			done = nil
		case <-time.After(50 * time.Millisecond):
		}
		if done != nil && time.Now().After(deadline) {
			t.Fatal("readLoop did not exit after crash")
		}
	}

	// THE CLAIM: EnsureReady must fail (process is dead) but returns nil.
	if err := c.EnsureReady(ctx); err != nil {
		t.Logf("EnsureReady returned error (claim would be weakened): %v", err)
	} else {
		c.mu.Lock()
		stillRunning := c.running
		staleSession := c.sessionID
		c.mu.Unlock()
		t.Logf("EnsureReady returned NIL with running=%v staleSession=%q (stale ready state)", stillRunning, staleSession)
		if !stillRunning {
			t.Fatal("running flag unexpectedly cleared - claim F3 structure changed")
		}
		if staleSession == "" {
			t.Fatal("sessionID unexpectedly cleared - claim F3 structure changed")
		}
	}

	// And a subsequent Prompt on the same client still fails: no self-heal.
	_, err2 := c.Prompt(ctx, "go again")
	if err2 == nil {
		t.Fatal("expected prompt #2 to fail (no self-heal); got success")
	}
	if !strings.Contains(err2.Error(), "exited unexpectedly") &&
		!strings.Contains(err2.Error(), "EOF") &&
		!strings.Contains(err2.Error(), "closed") {
		t.Fatalf("prompt #2 unexpected error: %v", err2)
	}
	t.Logf("prompt #2 error (no self-heal): %v", err2)
}

func TestProbeCrashHelperProcess(t *testing.T) {
	if os.Getenv(probeCrashHelperEnv) != "1" {
		t.Skip("helper process only")
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var req JSONRPCRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}
		switch req.Method {
		case "initialize":
			_ = encoder.Encode(JSONRPCResponse{
				JSONRPC: "2.0", ID: req.ID,
				Result: InitializeResponse{
					ProtocolVersion:   ProtocolVersion,
					AgentCapabilities: AgentCapabilities{LoadSession: true},
					AgentInfo:         ImplementationInfo{Name: "crash-helper", Version: "1.0"},
				},
			})
		case "session/new":
			_ = encoder.Encode(JSONRPCResponse{
				JSONRPC: "2.0", ID: req.ID, Result: NewSessionResponse{SessionID: "s-crash"},
			})
		case "session/prompt":
			// Simulate hard crash (OOM-kill): no close frames, just die.
			os.Exit(1)
		}
	}
}

// ---------- F2: human approval latency counted against 5-min idle timer ----------

func TestProbeF2_IdleTimeoutFiresDuringHumanApproval(t *testing.T) {
	t.Run("human_slower_than_idle_false_timeout", func(t *testing.T) {
		c := probeClient(t, probeApprovalHelperEnv, "TestProbeApprovalHelperProcess")
		c.promptIdleTime = 500 * time.Millisecond // shrink idle window
		approvalDelay := 5 * time.Second          // human answers well after idle expired (10x margin: robust under load)
		c.SetApprovalHandler(func(ctx context.Context, toolName, input string) permission.Decision {
			select {
			case <-time.After(approvalDelay):
				return permission.Allow
			case <-ctx.Done():
				return permission.Deny
			}
		})
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := c.Start(ctx); err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer c.Close()
		if err := c.NewSession(ctx, c.workingDir); err != nil {
			t.Fatalf("NewSession: %v", err)
		}

		_, err := c.Prompt(ctx, "needs approval")
		if err == nil {
			t.Fatal("expected timeout error, got success")
		}
		if !strings.Contains(err.Error(), "timeout waiting for agent prompt completion") {
			t.Fatalf("expected idle-timeout error, got: %v", err)
		}
		t.Logf("CONFIRMED F2: prompt aborted while human approval was in flight: %v", err)
	})

	t.Run("human_faster_than_idle_succeeds", func(t *testing.T) {
		c := probeClient(t, probeApprovalHelperEnv, "TestProbeApprovalHelperProcess")
		// Generous idle window: the control case asserts a clean success, so the
		// margin must survive a loaded CI runner (observed 4s+ round-trip in
		// verify-ci with a 3s window — a false failure, not a product bug).
		c.promptIdleTime = 15 * time.Second
		c.SetApprovalHandler(func(ctx context.Context, toolName, input string) permission.Decision {
			time.Sleep(300 * time.Millisecond) // fast human
			return permission.Allow
		})
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := c.Start(ctx); err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer c.Close()
		if err := c.NewSession(ctx, c.workingDir); err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		res, err := c.Prompt(ctx, "needs approval")
		if err != nil {
			t.Fatalf("control case must succeed, got: %v", err)
		}
		if res == nil || res.StopReason == "" {
			t.Fatalf("expected completed result, got %+v", res)
		}
	})
}

func TestProbeApprovalHelperProcess(t *testing.T) {
	if os.Getenv(probeApprovalHelperEnv) != "1" {
		t.Skip("helper process only")
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var req JSONRPCRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}
		switch req.Method {
		case "initialize":
			_ = encoder.Encode(JSONRPCResponse{
				JSONRPC: "2.0", ID: req.ID,
				Result: InitializeResponse{
					ProtocolVersion:   ProtocolVersion,
					AgentCapabilities: AgentCapabilities{LoadSession: true},
					AgentInfo:         ImplementationInfo{Name: "approval-helper", Version: "1.0"},
				},
			})
		case "session/new":
			_ = encoder.Encode(JSONRPCResponse{
				JSONRPC: "2.0", ID: req.ID, Result: NewSessionResponse{SessionID: "s-appr"},
			})
		case "session/prompt":
			// Agent asks the human for permission, then completes after answer.
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0", "id": 9001, "method": "session/request_permission",
				"params": map[string]any{
					"sessionID": "s-appr",
					"options": []map[string]any{
						{"optionId": "allow", "name": "Allow", "kind": "allow_once"},
						{"optionId": "deny", "name": "Deny", "kind": "reject_once"},
					},
				},
			})
			// Wait for the client's permission answer (a response line, no method).
			answered := false
			for !answered && scanner.Scan() {
				var line map[string]json.RawMessage
				if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
					continue
				}
				if _, isResp := line["result"]; isResp {
					answered = true
				}
				if _, isErr := line["error"]; isErr {
					answered = true
				}
			}
			_ = encoder.Encode(JSONRPCResponse{
				JSONRPC: "2.0", ID: req.ID,
				Result: map[string]any{"stopReason": "end_turn"},
			})
		}
	}
}

// ---------- F1: readLoop parked in blocking approval handler starves pending responses ----------

func TestProbeF1_HeadOfLineBlockingAndCloseHang(t *testing.T) {
	t.Skip("#1087 F1 fixed: permission/FS requests now handled asynchronously; blocking probe no longer applies")

	// Original probe test for detecting head-of-line blocking.
	// Retained for reference - the bug manifested as:
	// - Response written by agent but undeliverable (readLoop parked in approval)
	// - Close() blocked behind parked readLoop
	// Fix: async handling of session/request_permission, fs/read_text_file, fs/write_text_file

	markerPath := filepath.Join(t.TempDir(), "noop-responded")
	t.Setenv(probeHOLMarkerEnv, markerPath)

	c := probeClient(t, probeHOLHelperEnv, "TestProbeHOLHelperProcess")
	release := make(chan struct{})
	c.SetApprovalHandler(func(ctx context.Context, toolName, input string) permission.Decision {
		// Deliberately ignore ctx, mirroring wailskit's context.WithoutCancel bridge.
		<-release
		return permission.Allow
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := c.NewSession(ctx, c.workingDir); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	// Helper fires session/request_permission right after session/new; the
	// readLoop should now be parked inside onApproval.
	time.Sleep(300 * time.Millisecond)

	// Fire a request the helper answers instantly. The response will be
	// written by the helper into the pipe but cannot be delivered while the
	// readLoop is parked.
	type reqResult struct {
		err error
	}
	reqCh := make(chan reqResult, 1)
	go func() {
		_, err := c.sendRequest("probe/noop", map[string]any{}, 700*time.Millisecond)
		reqCh <- reqResult{err: err}
	}()

	// Wait until the helper has actually written the response (marker file).
	markerSeen := false
	deadlineM := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadlineM) {
		if _, err := os.Stat(markerPath); err == nil {
			markerSeen = true
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if !markerSeen {
		t.Fatal("helper never wrote the response marker")
	}

	// The request must still time out despite the response sitting in the pipe.
	select {
	case r := <-reqCh:
		if r.err == nil {
			t.Fatal("probe/noop unexpectedly succeeded - no head-of-line blocking?")
		}
		if !strings.Contains(r.err.Error(), "timeout waiting for client response") {
			t.Fatalf("expected delivery timeout, got: %v", r.err)
		}
		t.Logf("CONFIRMED F1 (a): response written by agent but undeliverable; sendRequest timed out: %v", r.err)
	case <-time.After(3 * time.Second):
		t.Fatal("sendRequest did not settle")
	}

	// Close() cannot finish while readLoop is parked: done channel stays open
	// even though Close kills the process.
	closeDone := make(chan error, 1)
	go func() { closeDone <- c.Close() }()
	select {
	case <-closeDone:
		t.Fatal("Close returned while readLoop was still parked in approval handler")
	case <-time.After(400 * time.Millisecond):
		t.Log("CONFIRMED F1 (b): Close() blocked behind parked readLoop (done not closed)")
	}

	// Release the approval; everything unwinds.
	close(release)
	select {
	case <-closeDone:
		t.Log("Close returned after approval released")
	case <-time.After(15 * time.Second):
		t.Fatal("Close never returned after approval release")
	}
}

func TestProbeHOLHelperProcess(t *testing.T) {
	if os.Getenv(probeHOLHelperEnv) != "1" {
		t.Skip("helper process only")
	}
	markerPath := os.Getenv(probeHOLMarkerEnv)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var line map[string]json.RawMessage
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			continue
		}
		var method string
		if raw, ok := line["method"]; ok {
			_ = json.Unmarshal(raw, &method)
		}
		var id json.RawMessage
		if raw, ok := line["id"]; ok {
			id = raw
		}
		switch method {
		case "initialize":
			_ = encoder.Encode(JSONRPCResponse{
				JSONRPC: "2.0", ID: &id,
				Result: InitializeResponse{
					ProtocolVersion:   ProtocolVersion,
					AgentCapabilities: AgentCapabilities{LoadSession: true},
					AgentInfo:         ImplementationInfo{Name: "hol-helper", Version: "1.0"},
				},
			})
		case "session/new":
			_ = encoder.Encode(JSONRPCResponse{
				JSONRPC: "2.0", ID: &id, Result: NewSessionResponse{SessionID: "s-hol"},
			})
			// Immediately ask permission; the client readLoop parks here.
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0", "id": 9002, "method": "session/request_permission",
				"params": map[string]any{
					"sessionID": "s-hol",
					"options": []map[string]any{
						{"optionId": "allow", "name": "Allow", "kind": "allow_once"},
					},
				},
			})
		case "":
			// Response from client (e.g. permission answer) - ignore.
			continue
		default:
			// Answer any other request instantly...
			_ = encoder.Encode(JSONRPCResponse{JSONRPC: "2.0", ID: &id, Result: map[string]any{}})
			// ...then prove the write happened via the marker file.
			if method == "probe/noop" {
				_ = os.WriteFile(markerPath, []byte(fmt.Sprintf("%d", time.Now().UnixNano())), 0o644)
			}
		}
	}
}
