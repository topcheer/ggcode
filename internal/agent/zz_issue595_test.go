package agent

import (
	"testing"
)

// TestIssue595_Bug1_BackgroundVerifyChannels tests Bug 1:
// verifyTools should include start_command, wait_command, task_output,
// read_command_output (#595).
func TestIssue595_Bug1_BackgroundVerifyChannels(t *testing.T) {
	tests := []struct {
		name       string
		toolName   string
		args       map[string]interface{}
		isError    bool
		wantVerify bool // should this count as verification?
		wantClear  bool // should this clear editsSinceVerify?
	}{
		{
			name:       "start_command with verify command clears counter",
			toolName:   "start_command",
			args:       map[string]interface{}{"command": "go test ./..."},
			isError:    false,
			wantVerify: true,
			wantClear:  true,
		},
		{
			name:       "start_command with non-verify command does not count",
			toolName:   "start_command",
			args:       map[string]interface{}{"command": "ls -la"},
			isError:    false,
			wantVerify: false,
			wantClear:  false,
		},
		{
			name:       "start_command failure does not clear counter",
			toolName:   "start_command",
			args:       map[string]interface{}{"command": "go test ./..."},
			isError:    true,
			wantVerify: false, // failure doesn't count as verification
			wantClear:  false,
		},
		{
			name:       "wait_command success clears counter",
			toolName:   "wait_command",
			args:       map[string]interface{}{},
			isError:    false,
			wantVerify: true,
			wantClear:  true,
		},
		{
			name:       "task_output success clears counter",
			toolName:   "task_output",
			args:       map[string]interface{}{},
			isError:    false,
			wantVerify: true,
			wantClear:  true,
		},
		{
			name:       "read_command_output success clears counter",
			toolName:   "read_command_output",
			args:       map[string]interface{}{},
			isError:    false,
			wantVerify: true,
			wantClear:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newPrematureSuccessState()
			s.recordToolCall("edit_file", map[string]interface{}{"file_path": "/foo.go"}, false)
			if s.editsSinceVerify != 1 {
				t.Fatalf("precondition: editsSinceVerify should be 1, got %d", s.editsSinceVerify)
			}

			s.recordToolCall(tt.toolName, tt.args, tt.isError)

			if tt.wantClear {
				if s.editsSinceVerify != 0 {
					t.Errorf("expected editsSinceVerify to be cleared (0), got %d", s.editsSinceVerify)
				}
				if !s.everVerified {
					t.Error("expected everVerified to be true")
				}
				if s.lastVerifyFailed {
					t.Error("expected lastVerifyFailed to be false")
				}
			} else {
				// Counter should NOT be cleared
				if s.editsSinceVerify != 1 {
					t.Errorf("expected editsSinceVerify to remain 1, got %d", s.editsSinceVerify)
				}
				if tt.isError {
					if !s.lastVerifyFailed {
						t.Error("expected lastVerifyFailed to be true for error")
					}
				} else if !tt.wantVerify {
					// Non-verify command should not set everVerified
					if s.everVerified {
						t.Error("expected everVerified to remain false for non-verify command")
					}
				}
			}
		})
	}
}

// TestIssue595_Bug1_BackgroundVerifyWorkflow tests the probe scenario:
// run_command fails -> edit -> start_command -> wait_command -> success claim.
func TestIssue595_Bug1_BackgroundVerifyWorkflow(t *testing.T) {
	s := newPrematureSuccessState()

	// Initial run_command fails
	s.recordToolCall("run_command", map[string]interface{}{"command": "go test ./..."}, true)
	if !s.lastVerifyFailed {
		t.Fatal("expected lastVerifyFailed=true after failed run_command")
	}

	// Edit to fix
	s.recordToolCall("edit_file", map[string]interface{}{"file_path": "/foo.go"}, false)
	if s.editsSinceVerify != 1 {
		t.Fatalf("expected editsSinceVerify=1 after edit, got %d", s.editsSinceVerify)
	}

	// start_command (background test)
	s.recordToolCall("start_command", map[string]interface{}{"command": "go test ./..."}, false)
	if s.editsSinceVerify != 0 {
		t.Errorf("start_command should clear editsSinceVerify, got %d", s.editsSinceVerify)
	}
	if !s.everVerified {
		t.Error("start_command should set everVerified=true")
	}
	if s.lastVerifyFailed {
		t.Error("successful start_command should clear lastVerifyFailed")
	}

	// wait_command succeeds
	s.recordToolCall("wait_command", map[string]interface{}{}, false)
	if s.editsSinceVerify != 0 {
		t.Error("wait_command should keep editsSinceVerify at 0")
	}
	if !s.everVerified {
		t.Error("everVerified should remain true")
	}

	// Claim success - should NOT fire (verification was done)
	hint := s.checkSuccessClaim("All tests pass now.")
	if hint != "" {
		t.Errorf("should not fire after successful verification, got hint: %s", hint)
	}
}

// TestIssue595_Bug2_AfterOnceGuardWords tests Bug 2:
// Past-tense assertions with "after"/"once" should NOT be guarded.
func TestIssue595_Bug2_AfterOnceGuardWords(t *testing.T) {
	tests := []struct {
		name        string
		text        string
		wantGuarded bool // true = should be guarded (no fire), false = should fire
	}{
		{
			name:        "After applying - past participle, should fire (NOT guarded)",
			text:        "After applying the fix, all tests pass.",
			wantGuarded: false,
		},
		{
			name:        "After running - past participle, should fire (NOT guarded)",
			text:        "After running the tests, everything passes.",
			wantGuarded: false,
		},
		{
			name:        "Once applied - past participle, should fire (NOT guarded)",
			text:        "Once applied, the fix resolves the issue.",
			wantGuarded: false,
		},
		{
			name:        "Once running - incomplete form, should be guarded",
			text:        "Once running, the tests will pass.",
			wantGuarded: true,
		},
		{
			name:        "After applying now - incomplete form, should be guarded",
			text:        "After applying now, all tests pass.",
			wantGuarded: true,
		},
		{
			name:        "After starting - incomplete form, should be guarded",
			text:        "After starting the build, we'll verify.",
			wantGuarded: true,
		},
		{
			name:        "After will - future tense, should be guarded",
			text:        "After we will fix it, tests will pass.",
			wantGuarded: true,
		},
		{
			name:        "Once pending - incomplete, should be guarded",
			text:        "Once pending, the fix will apply.",
			wantGuarded: true,
		},
		{
			name:        "Standard if - always guarded",
			text:        "If all tests pass, the task is complete.",
			wantGuarded: true,
		},
		{
			name:        "Standard when - always guarded",
			text:        "When the build passes, we're done.",
			wantGuarded: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newPrematureSuccessState()
			s.recordToolCall("edit_file", map[string]interface{}{"file_path": "/foo.go"}, false)

			hint := s.checkSuccessClaim(tt.text)
			fired := (hint != "")

			if tt.wantGuarded && fired {
				t.Errorf("text should be guarded (no hint), but got hint: %s", hint)
			}
			if !tt.wantGuarded && !fired {
				t.Errorf("text should NOT be guarded (should fire hint), but got none")
			}
		})
	}
}

// TestIssue595_Bug2_ProbeScenario tests the exact probe from the issue:
// "After applying the fix, all tests pass." should fire (not guarded).
func TestIssue595_Bug2_ProbeScenario(t *testing.T) {
	s := newPrematureSuccessState()
	s.recordToolCall("edit_file", map[string]interface{}{"file_path": "/foo.go"}, false)

	// This is the exact probe from the issue - should fire (not guarded)
	hint := s.checkSuccessClaim("After applying the fix, all tests pass.")
	if hint == "" {
		t.Fatal("should fire - 'After applying' is past-tense completion, NOT a guard")
	}

	// Contrast: "Applying the fix now. All tests pass." should also fire
	s2 := newPrematureSuccessState()
	s2.recordToolCall("edit_file", map[string]interface{}{"file_path": "/foo.go"}, false)
	hint2 := s2.checkSuccessClaim("Applying the fix now. All tests pass.")
	if hint2 == "" {
		t.Fatal("should fire - this is the control case from the issue")
	}
}

// TestIssue595_Bug1_NonVerifyStartCommand tests that start_command with
// non-verify commands does NOT count as verification.
func TestIssue595_Bug1_NonVerifyStartCommand(t *testing.T) {
	s := newPrematureSuccessState()
	s.recordToolCall("edit_file", map[string]interface{}{"file_path": "/foo.go"}, false)

	// start_command with non-verify command should NOT reset counter
	s.recordToolCall("start_command", map[string]interface{}{"command": "sleep 10"}, false)
	if s.editsSinceVerify != 1 {
		t.Errorf("expected editsSinceVerify=1 (not cleared by non-verify start_command), got %d", s.editsSinceVerify)
	}
	if s.everVerified {
		t.Error("everVerified should remain false for non-verify start_command")
	}

	// Should still fire on success claim
	hint := s.checkSuccessClaim("Done!")
	if hint == "" {
		t.Fatal("should fire - non-verify start_command should not count")
	}
}
