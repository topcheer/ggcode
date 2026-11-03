//go:build integration_local

package a2a

import (
	"context"
	"testing"
	"time"
)

func TestBearerTokenPrecedence(t *testing.T) {
	tests := []struct {
		name       string
		token      string
		expiry     time.Time
		wantResult bool
		wantMethod string
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
			expiry:     time.Time{},
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
				t.Errorf("authMethod = %q after false result, expected unchanged", c.authMethod)
			}
		})
	}
}

func TestBearerTokenPrecedenceBug(t *testing.T) {
	c := &Client{
		bearerToken: "",
		tokenExpiry: time.Now().Add(1 * time.Hour),
	}
	if c.tryBearerToken() {
		t.Error("tryBearerToken returned true for empty token with future expiry — operator precedence bug is present")
	}
}

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

			task := &Task{
				ID:        generateID(),
				ContextID: generateID(),
				Status:    TaskStatus{State: TaskStateSubmitted, Timestamp: time.Now()},
				Skill:     tt.skill,
				History:   []Message{},
				CreatedAt: time.Now(),
				done:      make(chan struct{}),
			}

			handler.mu.Lock()
			handler.tasks[task.ID] = task
			handler.mu.Unlock()

			perm, ok := skillPermissions[tt.skill]
			if !ok {
				t.Fatalf("no permission for skill: %s", tt.skill)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			handler.execute(ctx, task, perm, 0)

			if task.Status.State != TaskStateFailed {
				t.Errorf("expected failed state, got %s", task.Status.State)
			}

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
		History:   []Message{},
		CreatedAt: time.Now(),
		done:      make(chan struct{}),
	}

	handler.mu.Lock()
	handler.tasks[task.ID] = task
	handler.mu.Unlock()

	handler.updateStatus(task, TaskStateWorking, "")

	time.Sleep(100 * time.Millisecond)

	if capturedEvent.Type != "start" {
		t.Errorf("expected start event, got %s", capturedEvent.Type)
	}
	if capturedEvent.TaskID != task.ID {
		t.Errorf("expected task ID %s, got %s", task.ID, capturedEvent.TaskID)
	}
	if capturedEvent.Message == "" {
		t.Error("expected non-empty event message")
	}
}

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
		History:   []Message{},
		CreatedAt: time.Now(),
		done:      make(chan struct{}),
	}

	handler.mu.Lock()
	handler.tasks[task.ID] = task
	handler.mu.Unlock()

	handler.updateStatus(task, TaskStateFailed, "something went wrong")

	time.Sleep(100 * time.Millisecond)

	if capturedEvent.Type != "fail" {
		t.Errorf("expected fail event, got %s", capturedEvent.Type)
	}
	if capturedEvent.Error != "something went wrong" {
		t.Errorf("expected error message, got %s", capturedEvent.Error)
	}
}
