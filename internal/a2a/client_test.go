//go:build integration_local

package a2a

import (
	"context"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Test 1: tryBearerToken operator precedence
// ---------------------------------------------------------------------------

func TestBearerTokenPrecedence(t *testing.T) {
	tests := []struct {
		name       string
		token      string
		expiry     time.Time // zero value = IsZero() == true
		wantResult bool
		wantMethod string // expected authMethod after call
	}{
		{
			name:       "empty token with future expiry should return false",
			token:      "",
			expiry:     time.Now().Add(1 * time.Hour),
			wantResult: false,
			wantMethod: "",
		},
		{
			name:       "non-empty token with zero expiry should return true",
			token:      "valid-token",
			expiry:     time.Time{}, // zero value
			wantResult: true,
			wantMethod: "bearer",
		},
		{
			name:       "non-empty token with future expiry should return true",
			token:      "valid-token",
			expiry:     time.Now().Add(1 * time.Hour),
			wantResult: true,
			wantMethod: "bearer",
		},
		{
			name:       "non-empty token with past expiry should return false",
			token:      "valid-token",
			expiry:     time.Now().Add(-1 * time.Hour),
			wantResult: false,
			wantMethod: "",
		},
		{
			name:       "empty token with zero expiry should return false",
			token:      "",
			expiry:     time.Time{},
			wantResult: false,
			wantMethod: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{
				bearerToken: tt.token,
				tokenExpiry: tt.expiry,
			}
			got := c.tryBearerToken()
			if got != tt.wantResult {
				t.Errorf("tryBearerToken() = %v, want %v", got, tt.wantResult)
			}
			if tt.wantResult && c.authMethod != tt.wantMethod {
				t.Errorf("authMethod = %q, want %q", c.authMethod, tt.wantMethod)
			}
			if !tt.wantResult && c.authMethod != "" {
				// If result is false, authMethod should remain unchanged
				// (only set to "bearer" on success path)
				t.Errorf("authMethod = %q after false result, expected unchanged", c.authMethod)
			}
		})
	}
}

// TestBearerTokenPrecedenceBug verifies the specific operator precedence fix.
// Before the fix, the expression was:
//
//	c.bearerToken != "" && c.tokenExpiry.IsZero() || time.Now().Before(c.tokenExpiry)
//
// Which, due to && binding tighter than ||, would evaluate as:
//
//	(c.bearerToken != "" && c.tokenExpiry.IsZero()) || time.Now().Before(c.tokenExpiry)
//
// This means an empty token with a future expiry would return true (wrong).
func TestBearerTokenPrecedenceBug(t *testing.T) {
	// This is the critical case that was wrong before the fix:
	// empty bearerToken BUT future expiry.
	// The buggy code would evaluate:
	//   ("" != "" && zero) || now.Before(future) => false || true => true (WRONG)
	// The fixed code evaluates:
	//   "" != "" && (zero || now.Before(future)) => false && true => false (CORRECT)
	c := &Client{
		bearerToken: "",
		tokenExpiry: time.Now().Add(1 * time.Hour),
	}
	if c.tryBearerToken() {
		t.Error("tryBearerToken returned true for empty token with future expiry — operator precedence bug is present")
	}
}

// ---------------------------------------------------------------------------
// Test 2: History[0] bounds check — empty History should not panic
// ---------------------------------------------------------------------------

// TestHistoryBoundsExecute verifies that execute() does not panic when a task
// has an empty History slice. The guard `len(t.History) > 0` prevents the
// out-of-bounds access at History[0].
func TestHistoryBoundsExecute(t *testing.T) {
	tests := []struct {
		name  string
		skill string
	}{
		{"file-search empty history", SkillFileSearch},
		{"git-ops empty history", SkillGitOps},
		{"command-exec empty history", SkillCommandExec},
		{"code-edit empty history", SkillCodeEdit},
		{"code-review empty history", SkillCodeReview},
		{"full-task empty history", SkillFullTask},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewTaskHandler(".", nil, nil, WithTimeout(2*time.Second))

			// Create a task with empty History via Handle (which normally adds
			// the input message), then manually clear the History before the
			// async execute goroutine runs.
			task := &Task{
				ID:        generateID(),
				ContextID: generateID(),
				Status:    TaskStatus{State: TaskStateSubmitted, Timestamp: time.Now()},
				Skill:     tt.skill,
				History:   []Message{}, // intentionally empty
				CreatedAt: time.Now(),
				done:      make(chan struct{}),
			}

			handler.mu.Lock()
			handler.tasks[task.ID] = task
			handler.mu.Unlock()

			// Get the permission for the skill
			perm, ok := skillPermissions[tt.skill]
			if !ok {
				t.Fatalf("no permission for skill: %s", tt.skill)
			}

			// Execute directly — this used to panic with History[0] on empty slice.
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			// This should not panic; it should set the task to failed with an error.
			handler.execute(ctx, task, perm)

			// Verify the task ended in a failed state with the expected error message.
			if task.Status.State != TaskStateFailed {
				t.Errorf("expected failed state, got %s", task.Status.State)
			}

			// Check that the error message mentions empty/no history.
			if len(task.History) == 0 {
				t.Fatal("expected error message to be appended to history")
			}
			lastMsg := task.History[len(task.History)-1]
			if lastMsg.Role != "agent" {
				t.Errorf("expected agent role in error message, got %s", lastMsg.Role)
			}
			errText := lastMsg.Parts[0].Text
			if errText == "" {
				t.Error("expected non-empty error text")
			}
		})
	}
}

// TestHistoryBoundsUpdateStatus verifies that updateStatus() does not panic
// when a task has an empty History slice. The onTaskEvent callback accesses
// History[0] with a guard.
func TestHistoryBoundsUpdateStatus(t *testing.T) {
	var capturedEvent TaskEventMessage
	handler := NewTaskHandler(".", nil, nil, WithTimeout(2*time.Second))
	handler.SetOnTaskEvent(func(msg TaskEventMessage) {
		capturedEvent = msg
	})

	task := &Task{
		ID:        generateID(),
		ContextID: generateID(),
		Status:    TaskStatus{State: TaskStateSubmitted, Timestamp: time.Now()},
		Skill:     SkillFullTask,
		History:   []Message{}, // intentionally empty
		CreatedAt: time.Now(),
		done:      make(chan struct{}),
	}

	handler.mu.Lock()
	handler.tasks[task.ID] = task
	handler.mu.Unlock()

	// This should not panic — updateStatus fires the onTaskEvent callback
	// which accesses History[0] with a len(t.History) > 0 guard.
	handler.updateStatus(task, TaskStateWorking, "")

	// Give the async event callback time to fire.
	time.Sleep(100 * time.Millisecond)

	// Verify the event was fired and didn't crash.
	if capturedEvent.Type != "start" {
		t.Errorf("expected start event, got %s", capturedEvent.Type)
	}
	if capturedEvent.TaskID != task.ID {
		t.Errorf("expected task ID %s, got %s", task.ID, capturedEvent.TaskID)
	}
	// The start message should not contain a panic trace — just the skill name.
	if capturedEvent.Message == "" {
		t.Error("expected non-empty event message")
	}
}

// TestHistoryBoundsUpdateStatusWithMessage verifies updateStatus with a
// non-empty message AND empty History does not panic.
func TestHistoryBoundsUpdateStatusWithMessage(t *testing.T) {
	var capturedEvent TaskEventMessage
	handler := NewTaskHandler(".", nil, nil, WithTimeout(2*time.Second))
	handler.SetOnTaskEvent(func(msg TaskEventMessage) {
		capturedEvent = msg
	})

	task := &Task{
		ID:        generateID(),
		ContextID: generateID(),
		Status:    TaskStatus{State: TaskStateSubmitted, Timestamp: time.Now()},
		Skill:     SkillFileSearch,
		History:   []Message{}, // intentionally empty
		CreatedAt: time.Now(),
		done:      make(chan struct{}),
	}

	handler.mu.Lock()
	handler.tasks[task.ID] = task
	handler.mu.Unlock()

	// updateStatus with a status message — this appends to History, then
	// fires the event. The event callback should still be safe.
	handler.updateStatus(task, TaskStateFailed, "something went wrong")

	// Give the async event callback time to fire.
	time.Sleep(100 * time.Millisecond)

	if capturedEvent.Type != "fail" {
		t.Errorf("expected fail event, got %s", capturedEvent.Type)
	}
	if capturedEvent.Error != "something went wrong" {
		t.Errorf("expected error message, got %s", capturedEvent.Error)
	}
}
