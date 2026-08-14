package agent

import (
	"encoding/json"
	"testing"
)

func couplingRawJSON(t *testing.T, v map[string]interface{}) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestBatchCoupling_NoWarningForSingleCall(t *testing.T) {
	s := newBatchCouplingState()
	calls := []couplingToolCall{
		{name: "read_file", args: couplingRawJSON(t, map[string]interface{}{"path": "/foo/bar.go"})},
	}
	if got := s.checkBatchCoupling(calls); got != "" {
		t.Fatalf("expected no warning for single call, got: %s", got)
	}
}

func TestBatchCoupling_NoWarningForUnrelatedCalls(t *testing.T) {
	s := newBatchCouplingState()
	calls := []couplingToolCall{
		{name: "read_file", args: couplingRawJSON(t, map[string]interface{}{"path": "/a.go"})},
		{name: "read_file", args: couplingRawJSON(t, map[string]interface{}{"path": "/b.go"})},
	}
	if got := s.checkBatchCoupling(calls); got != "" {
		t.Fatalf("expected no warning for unrelated reads, got: %s", got)
	}
}

func TestBatchCoupling_EditThenReadSameFile(t *testing.T) {
	s := newBatchCouplingState()
	calls := []couplingToolCall{
		{name: "edit_file", args: couplingRawJSON(t, map[string]interface{}{"file_path": "/foo.go"})},
		{name: "read_file", args: couplingRawJSON(t, map[string]interface{}{"path": "/foo.go"})},
	}
	got := s.checkBatchCoupling(calls)
	if got == "" {
		t.Fatal("expected coupling warning for edit_file + read_file on same file")
	}
}

func TestBatchCoupling_EditThenReadDifferentFile(t *testing.T) {
	s := newBatchCouplingState()
	calls := []couplingToolCall{
		{name: "edit_file", args: couplingRawJSON(t, map[string]interface{}{"file_path": "/a.go"})},
		{name: "read_file", args: couplingRawJSON(t, map[string]interface{}{"path": "/b.go"})},
	}
	if got := s.checkBatchCoupling(calls); got != "" {
		t.Fatalf("expected no coupling warning for different files, got: %s", got)
	}
}

func TestBatchCoupling_WriteThenReadSameFile(t *testing.T) {
	s := newBatchCouplingState()
	calls := []couplingToolCall{
		{name: "write_file", args: couplingRawJSON(t, map[string]interface{}{"path": "/new.go"})},
		{name: "read_file", args: couplingRawJSON(t, map[string]interface{}{"path": "/new.go"})},
	}
	got := s.checkBatchCoupling(calls)
	if got == "" {
		t.Fatal("expected coupling warning for write_file + read_file on same file")
	}
}

func TestBatchCoupling_MkdirThenWriteFile(t *testing.T) {
	s := newBatchCouplingState()
	calls := []couplingToolCall{
		{name: "file_ops", args: couplingRawJSON(t, map[string]interface{}{"operations": []map[string]interface{}{{"action": "mkdir", "source": "/newdir"}}})},
		{name: "write_file", args: couplingRawJSON(t, map[string]interface{}{"path": "/newdir/file.go"})},
	}
	got := s.checkBatchCoupling(calls)
	if got == "" {
		t.Fatal("expected coupling warning for mkdir + write_file to that dir")
	}
}

func TestBatchCoupling_MkdirThenWriteUnrelatedFile(t *testing.T) {
	s := newBatchCouplingState()
	calls := []couplingToolCall{
		{name: "file_ops", args: couplingRawJSON(t, map[string]interface{}{"operations": []map[string]interface{}{{"action": "mkdir", "source": "/newdir"}}})},
		{name: "write_file", args: couplingRawJSON(t, map[string]interface{}{"path": "/other/file.go"})},
	}
	if got := s.checkBatchCoupling(calls); got != "" {
		t.Fatalf("expected no coupling for unrelated mkdir + write, got: %s", got)
	}
}

func TestBatchCoupling_GitCheckoutThenRead(t *testing.T) {
	s := newBatchCouplingState()
	calls := []couplingToolCall{
		{name: "git_checkout", args: couplingRawJSON(t, map[string]interface{}{"branch": "feature"})},
		{name: "read_file", args: couplingRawJSON(t, map[string]interface{}{"path": "/any.go"})},
	}
	got := s.checkBatchCoupling(calls)
	if got == "" {
		t.Fatal("expected coupling warning for git_checkout + read_file")
	}
}

func TestBatchCoupling_GitAddThenCommit(t *testing.T) {
	s := newBatchCouplingState()
	calls := []couplingToolCall{
		{name: "git_add", args: couplingRawJSON(t, map[string]interface{}{"files": []string{"foo.go"}})},
		{name: "git_commit", args: couplingRawJSON(t, map[string]interface{}{"message": "test"})},
	}
	got := s.checkBatchCoupling(calls)
	if got == "" {
		t.Fatal("expected coupling warning for git_add + git_commit")
	}
}

func TestBatchCoupling_MaxWarnings(t *testing.T) {
	s := newBatchCouplingState()
	calls := []couplingToolCall{
		{name: "edit_file", args: couplingRawJSON(t, map[string]interface{}{"file_path": "/foo.go"})},
		{name: "read_file", args: couplingRawJSON(t, map[string]interface{}{"path": "/foo.go"})},
	}
	// First call fires.
	if got := s.checkBatchCoupling(calls); got == "" {
		t.Fatal("expected first warning")
	}
	// Second call fires (maxWarns=2).
	if got := s.checkBatchCoupling(calls); got == "" {
		t.Fatal("expected second warning")
	}
	// Third call should be suppressed.
	if got := s.checkBatchCoupling(calls); got != "" {
		t.Fatalf("expected suppression after maxWarns, got: %s", got)
	}
}

func TestBatchCoupling_Reset(t *testing.T) {
	s := newBatchCouplingState()
	s.warnsIssued = s.maxWarns
	s.reset()
	if s.warnsIssued != 0 {
		t.Fatalf("expected warnsIssued=0 after reset, got %d", s.warnsIssued)
	}
}

func TestBatchCoupling_ReverseOrderFlagged(t *testing.T) {
	s := newBatchCouplingState()
	// #150: read_file before edit_file — parallel execution would return
	// pre-edit content, so the reverse order is ALSO flagged now.
	calls := []couplingToolCall{
		{name: "read_file", args: couplingRawJSON(t, map[string]interface{}{"path": "/foo.go"})},
		{name: "edit_file", args: couplingRawJSON(t, map[string]interface{}{"file_path": "/foo.go"})},
	}
	if got := s.checkBatchCoupling(calls); got == "" {
		t.Fatal("expected warning for reverse order (read before edit)")
	}
}
