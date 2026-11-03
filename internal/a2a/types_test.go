package a2a

import (
	"encoding/json"
	"strings"
	"testing"
)

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
}
