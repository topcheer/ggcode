package a2a

// Regression tests for #642: Message.snapshot() and Artifact.snapshot() must
// deep-copy the Metadata protocol-extension field, or every Snapshot-based
// tasks API path (tasks/get, tasks/list, ActiveTasks) silently drops it.

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestIssue642_MessageSnapshotPreservesMetadata(t *testing.T) {
	orig := Message{
		Role:      "agent",
		MessageID: "msg-1",
		Parts:     []Part{{Kind: "text", Text: "hello"}},
		Metadata:  json.RawMessage(`{"partitionKey":"shard-7","routing":{"queue":"critical"}}`),
	}

	cp := orig.snapshot()

	if cp.Metadata == nil {
		t.Fatal("Message.snapshot() dropped Metadata entirely")
	}
	if !bytes.Equal(cp.Metadata, orig.Metadata) {
		t.Fatalf("Message.metadata not copied: got %s want %s", cp.Metadata, orig.Metadata)
	}

	// Deep copy, not slice sharing: mutating the original must not affect the
	// snapshot (json.RawMessage shares the backing array on shallow copy).
	orig.Metadata[0] = ' '
	orig.Metadata[len(orig.Metadata)-1] = ' '
	var snapMap, wantMap map[string]any
	if err := json.Unmarshal(cp.Metadata, &snapMap); err != nil {
		t.Fatalf("snapshot metadata no longer valid JSON after mutating original: %v", err)
	}
	if err := json.Unmarshal([]byte(`{"partitionKey":"shard-7","routing":{"queue":"critical"}}`), &wantMap); err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(t, snapMap, wantMap) {
		t.Fatalf("snapshot metadata mutated with original: %s", cp.Metadata)
	}
}

func TestIssue642_ArtifactSnapshotPreservesMetadata(t *testing.T) {
	orig := Artifact{
		ArtifactID: "art-1",
		Append:     true,
		LastChunk:  false,
		Parts:      []Part{{Kind: "text", Text: "chunk"}},
		Metadata:   json.RawMessage(`{"attribution":"remote-agent","ttl":3600}`),
	}

	cp := orig.snapshot()

	if cp.Metadata == nil {
		t.Fatal("Artifact.snapshot() dropped Metadata entirely")
	}
	if !bytes.Equal(cp.Metadata, orig.Metadata) {
		t.Fatalf("Artifact.metadata not copied: got %s want %s", cp.Metadata, orig.Metadata)
	}

	orig.Metadata[0] = ' '
	var snapMap, wantMap map[string]any
	if err := json.Unmarshal(cp.Metadata, &snapMap); err != nil {
		t.Fatalf("snapshot metadata no longer valid JSON after mutating original: %v", err)
	}
	if err := json.Unmarshal([]byte(`{"attribution":"remote-agent","ttl":3600}`), &wantMap); err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(t, snapMap, wantMap) {
		t.Fatalf("snapshot metadata mutated with original: %s", cp.Metadata)
	}
}

func TestIssue642_TaskSnapshotPropagatesNestedMetadata(t *testing.T) {
	// The end-to-end path from the issue: GetTask/ListTasks/ActiveTasks all
	// return t.Snapshot(), so nested Message/Artifact metadata must survive
	// the top-level Snapshot too.
	task := &Task{
		ID:       "task-42",
		Status:   TaskStatus{State: TaskStateCompleted},
		Metadata: json.RawMessage(`{"top":true}`),
		History: []Message{{
			Role:      "agent",
			MessageID: "m1",
			Metadata:  json.RawMessage(`{"nested":"message"}`),
		}},
		Artifacts: []Artifact{{
			ArtifactID: "a1",
			Metadata:   json.RawMessage(`{"nested":"artifact"}`),
		}},
	}

	snap := task.Snapshot()

	if string(snap.History[0].Metadata) != `{"nested":"message"}` {
		t.Errorf("task history message metadata lost in Snapshot: %s", snap.History[0].Metadata)
	}
	if string(snap.Artifacts[0].Metadata) != `{"nested":"artifact"}` {
		t.Errorf("task artifact metadata lost in Snapshot: %s", snap.Artifacts[0].Metadata)
	}
	if string(snap.Metadata) != `{"top":true}` {
		t.Errorf("task top-level metadata lost in Snapshot: %s", snap.Metadata)
	}
}

func TestIssue642_SnapshotNilMetadataStaysNil(t *testing.T) {
	m := Message{Role: "user"}.snapshot()
	if m.Metadata != nil {
		t.Errorf("nil Metadata should stay nil, got %s", m.Metadata)
	}
	a := Artifact{ArtifactID: "x"}.snapshot()
	if a.Metadata != nil {
		t.Errorf("nil Metadata should stay nil, got %s", a.Metadata)
	}
}

func jsonEqual(t *testing.T, a, b map[string]any) bool {
	t.Helper()
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}
