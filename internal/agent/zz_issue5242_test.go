package agent

import (
	"encoding/json"
	"reflect"
	"testing"
)

// #5242: #5240 removed the file_ops case from the SHARED
// extractEditFilePaths helper to stop the unread-edit guard misfiring on
// batch deletes - correct for the guard, but it silently starved the
// three tracker call sites (success-path recorders, failure counter).
// A move's destination must be registered as freshly written and a
// successful file_ops must reset the consecutive-edit-failure counter.

func TestIssue5242_TrackerViewIncludesFileOps(t *testing.T) {
	args := json.RawMessage(`{"operations":[
		{"action":"move","source":"old/name.go","destination":"new/name.go"},
		{"action":"delete","source":"tmp/junk.go"},
		{"action":"mkdir","source":"tmp/newdir"}
	]}`)
	got := extractTrackedEditPaths("file_ops", args)
	want := []string{"new/name.go", "old/name.go", "tmp/junk.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tracker view: want %v, got %v", want, got)
	}
}

func TestIssue5242_MalformedOpsSafe(t *testing.T) {
	if got := extractTrackedEditPaths("file_ops", json.RawMessage(`{"operations":"oops"}`)); got != nil {
		t.Errorf("non-array operations must yield nil, got %v", got)
	}
	if got := extractTrackedEditPaths("file_ops", json.RawMessage(`{"operations":[42,null]}`)); got != nil {
		t.Errorf("non-object ops must be skipped, got %v", got)
	}
}
