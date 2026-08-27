package a2a

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/tool"
)

// TestIssue1110_StopBeforeStartDoesNotBlock guards #1110: Stop() must return
// when Start() was never called. CLI startup cleanup paths (OAuth2/OIDC/mTLS
// validation failures in cmd/ggcode) call Stop before Start and used to
// block forever on a done channel nobody closes.
func TestIssue1110_StopBeforeStartDoesNotBlock(t *testing.T) {
	handler := NewTaskHandler(t.TempDir(), nil, tool.NewRegistry())
	srv := NewServer(ServerConfig{Port: 0}, handler)
	done := make(chan struct{})
	go func() {
		srv.Stop()
		close(done)
	}()
	select {
	case <-done:
		// Stop returned - fixed.
	case <-time.After(2 * time.Second):
		t.Fatal("CONFIRMED: Stop() blocks forever when Start was never called (#1110)")
	}
}

// TestIssue1111_TaskSweptReturnsTaskNotFound guards #1111: when the task is
// swept between completion signaling and the post-done GetTask read, the
// send path must return a TaskNotFound error instead of serializing a nil
// task as result:null (protocol violation).
func TestIssue1111_TaskSweptReturnsTaskNotFound(t *testing.T) {
	handler := NewTaskHandler(t.TempDir(), nil, tool.NewRegistry())

	// Swept task: GetTask reports !ok.
	rec := httptest.NewRecorder()
	writeTaskResultOrNotFound(rec, json.RawMessage(`1`), handler, "no-such-task")
	body := rec.Body.String()
	if !strings.Contains(body, "Task not found") || strings.Contains(body, `"result":null`) {
		t.Fatalf("swept task must return TaskNotFound, got: %s", body)
	}

	// Live task: still serialized as the result.
	h := NewTaskHandler(t.TempDir(), nil, tool.NewRegistry())
	tsk := &Task{ID: "t-live"}
	tsk.Status.State = TaskStateCompleted
	h.mu.Lock()
	h.tasks[tsk.ID] = tsk
	h.mu.Unlock()
	rec2 := httptest.NewRecorder()
	writeTaskResultOrNotFound(rec2, json.RawMessage(`2`), h, "t-live")
	if !strings.Contains(rec2.Body.String(), "t-live") {
		t.Fatalf("live task must be serialized, got: %s", rec2.Body.String())
	}
}
