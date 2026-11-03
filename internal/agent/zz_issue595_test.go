package agent

import (
	"testing"
)

func TestIssue595_Bug1_BackgroundVerifyChannels(t *testing.T) {
	tests := []struct {
		name       string
		toolName   string
		args       map[string]interface{}
		isError    bool
		wantVerify bool
		wantClear  bool
	}{
		{
			name:       "start_command with verify command registers only (#1153)",
			toolName:   "start_command",
			args:       map[string]interface{}{"command": "go test ./..."},
			isError:    false,
			wantVerify: false,
			wantClear:  false,
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
			name:       "start_command failure is not a verify outcome (#1153)",
			toolName:   "start_command",
			args:       map[string]interface{}{"command": "go test ./..."},
			isError:    true,
			wantVerify: false,
			wantClear:  false,
		},
		{
			name:       "wait_command on unregistered job is conservative (#1153)",
			toolName:   "wait_command",
			args:       map[string]interface{}{},
			isError:    false,
			wantVerify: false,
			wantClear:  false,
		},
		{
			name:       "task_output on unregistered task is conservative (#1153)",
			toolName:   "task_output",
			args:       map[string]interface{}{},
			isError:    false,
			wantVerify: false,
			wantClear:  false,
		},
		{
			name:       "read_command_output on unregistered job is conservative (#1153)",
			toolName:   "read_command_output",
			args:       map[string]interface{}{},
			isError:    false,
			wantVerify: false,
			wantClear:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newPrematureSuccessState()
			s.recordToolCall("edit_file", map[string]interface{}{"file_path": "/foo.go"}, false, "")
			if s.editsSinceVerify != 1 {
				t.Fatalf("precondition: editsSinceVerify should be 1, got %d", s.editsSinceVerify)
			}

			s.recordToolCall(tt.toolName, tt.args, tt.isError, "")

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

				if s.editsSinceVerify != 1 {
					t.Errorf("expected editsSinceVerify to remain 1, got %d", s.editsSinceVerify)
				}
				if tt.isError {
					if !s.lastVerifyFailed && tt.toolName == "run_command" {
						t.Error("expected lastVerifyFailed to be true for error")
					}
				} else if !tt.wantVerify {

					if s.everVerified {
						t.Error("expected everVerified to remain false for non-verify command")
					}
				}
			}
		})
	}
}

func TestIssue595_Bug1_BackgroundVerifyWorkflow(t *testing.T) {
	s := newPrematureSuccessState()

	s.recordToolCall("run_command", map[string]interface{}{"command": "go test ./..."}, true, "")
	if !s.lastVerifyFailed {
		t.Fatal("expected lastVerifyFailed=true after failed run_command")
	}

	s.recordToolCall("edit_file", map[string]interface{}{"file_path": "/foo.go"}, false, "")
	if s.editsSinceVerify != 1 {
		t.Fatalf("expected editsSinceVerify=1 after edit, got %d", s.editsSinceVerify)
	}

	s.recordToolCall("start_command", map[string]interface{}{"command": "go test ./..."}, false,
		"Job ID: cmd-595\nStatus: running\n")
	if s.editsSinceVerify != 1 {
		t.Errorf("start_command must not clear editsSinceVerify at launch, got %d", s.editsSinceVerify)
	}
	if s.everVerified {
		t.Error("starting go test must not set everVerified before a result exists (#1153)")
	}

	s.recordToolCall("wait_command", map[string]interface{}{"job_id": "cmd-595"}, false,
		"Job ID: cmd-595\nStatus: completed\nok \tpkg\t0.4s\n")
	if s.editsSinceVerify != 0 {
		t.Error("completed background verify job should keep editsSinceVerify at 0")
	}
	if !s.everVerified {
		t.Error("everVerified should remain true")
	}

	hint := s.checkSuccessClaim("All tests pass now.")
	if hint != "" {
		t.Errorf("should not fire after successful verification, got hint: %s", hint)
	}
}

func TestIssue595_Bug2_AfterOnceGuardWords(t *testing.T) {
	tests := []struct {
		name        string
		text        string
		wantGuarded bool
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
			s.recordToolCall("edit_file", map[string]interface{}{"file_path": "/foo.go"}, false, "")

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

func TestIssue595_Bug2_ProbeScenario(t *testing.T) {
	s := newPrematureSuccessState()
	s.recordToolCall("edit_file", map[string]interface{}{"file_path": "/foo.go"}, false, "")

	hint := s.checkSuccessClaim("After applying the fix, all tests pass.")
	if hint == "" {
		t.Fatal("should fire - 'After applying' is past-tense completion, NOT a guard")
	}

	s2 := newPrematureSuccessState()
	s2.recordToolCall("edit_file", map[string]interface{}{"file_path": "/foo.go"}, false, "")
	hint2 := s2.checkSuccessClaim("Applying the fix now. All tests pass.")
	if hint2 == "" {
		t.Fatal("should fire - this is the control case from the issue")
	}
}

func TestIssue595_Bug1_NonVerifyStartCommand(t *testing.T) {
	s := newPrematureSuccessState()
	s.recordToolCall("edit_file", map[string]interface{}{"file_path": "/foo.go"}, false, "")

	s.recordToolCall("start_command", map[string]interface{}{"command": "sleep 10"}, false, "")
	if s.editsSinceVerify != 1 {
		t.Errorf("expected editsSinceVerify=1 (not cleared by non-verify start_command), got %d", s.editsSinceVerify)
	}
	if s.everVerified {
		t.Error("everVerified should remain false for non-verify start_command")
	}

	hint := s.checkSuccessClaim("Done!")
	if hint == "" {
		t.Fatal("should fire - non-verify start_command should not count")
	}
}
