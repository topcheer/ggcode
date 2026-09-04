package a2a

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestEventTaskIDJSONTagSpec pins #1470-B: A2A events serialize the task
// reference as taskId (the spec field) - the old house-dialect "id" tag
// left spec-parsing third-party receivers with an empty TaskID.
func TestEventTaskIDJSONTagSpec(t *testing.T) {
	b, err := json.Marshal(TaskStatusUpdateEvent{TaskID: "T-1", Status: TaskStatus{State: TaskStateWorking}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"taskId":"T-1"`) {
		t.Fatalf("status event missing spec taskId field: %s", b)
	}
	b2, err := json.Marshal(TaskArtifactUpdateEvent{TaskID: "T-1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b2), `"taskId":"T-1"`) {
		t.Fatalf("artifact event missing spec taskId field: %s", b2)
	}
	// A single json tag cannot accept both spellings; the spec alignment
	// is intentionally breaking for house-dialect peers - the in-house
	// client shares these structs so round-trips stay consistent.
}
