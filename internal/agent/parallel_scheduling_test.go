package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// Conflict-aware partial parallelism (parallel_scheduling.go): file-scoped
// mutators withhold only colliding reads from pre-execution; unrelated reads
// keep their parallel latency win. Whole-tree / unknown-scope mutators keep
// the #1475-A..#1829 batch-wide skip.

type stubReadTool struct{ name string }

func (s stubReadTool) Name() string        { return s.name }
func (s stubReadTool) Description() string { return "stub" }
func (s stubReadTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (s stubReadTool) Execute(_ context.Context, _ json.RawMessage) (tool.Result, error) {
	return tool.Result{Content: "stub-ok"}, nil
}

func TestFileScopedMutationPath(t *testing.T) {
	cases := []struct {
		name    string
		tool    string
		args    string
		scoped  bool
		wantPth string
	}{
		{"edit_file_path", "edit_file", `{"file_path":"/x/a.go"}`, true, "/x/a.go"},
		{"multi_edit", "multi_edit_file", `{"file_path":"a.go"}`, true, "a.go"},
		{"write", "write_file", `{"path":"/x/b.go"}`, true, "/x/b.go"},
		{"notebook", "notebook_edit", `{"notebook_path":"/x/n.ipynb"}`, true, "/x/n.ipynb"},
		{"legacy_edit_arg", "edit_file", `{"path":"/x/a.go"}`, false, ""},
		{"git_checkout", "git_checkout", `{"branch":"main"}`, false, ""},
		{"undo_edit", "undo_edit", `{"path":"/x/a.go"}`, false, ""},
		{"file_ops", "file_ops", `{"action":"delete","source":"/x/a.go"}`, false, ""},
	}
	for _, c := range cases {
		p, scoped := fileScopedMutationPath(c.tool, []byte(c.args))
		if scoped != c.scoped || p != c.wantPth {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", c.name, p, scoped, c.wantPth, c.scoped)
		}
	}
}

func TestReadAffectedByMutation(t *testing.T) {
	mut := []string{"/x/a.go"}
	cases := []struct {
		name     string
		tool     string
		args     string
		affected bool
	}{
		{"same_file_read", "read_file", `{"path":"/x/a.go"}`, true},
		{"unrelated_file_read", "read_file", `{"path":"/x/b.go"}`, false},
		{"abs_vs_rel_mismatch", "read_file", `{"path":"a.go"}`, true}, // conservative
		{"lsp_same_file", "lsp_diagnostics", `{"path":"/x/a.go"}`, true},
		{"lsp_other_file", "lsp_diagnostics", `{"path":"/y/z.go"}`, false},
		{"grep_over_mutated_dir", "grep", `{"pattern":"p","path":"/x"}`, true},
		{"grep_unrelated_dir", "grep", `{"pattern":"p","path":"/y"}`, false},
		{"grep_no_path_cwd", "grep", `{"pattern":"p"}`, true},
		{"search_files_dir", "search_files", `{"pattern":"p","directory":"/x"}`, true},
		{"list_dir_unrelated", "list_directory", `{"path":"/y"}`, false},
		{"git_status_any_mutation", "git_status", `{}`, true},
		{"git_diff_any_mutation", "git_diff", `{}`, true},
		{"multi_file_read_colliding", "multi_file_read", `{"files":[{"path":"/y/1.go"},{"path":"/x/a.go"}]}`, true},
		{"multi_file_read_clean", "multi_file_read", `{"files":[{"path":"/y/1.go"},{"path":"/y/2.go"}]}`, false},
		{"unknown_tool", "code_search", `{"query":"q"}`, true},
	}
	for _, c := range cases {
		if got := readAffectedByMutation(c.tool, []byte(c.args), mut); got != c.affected {
			t.Errorf("%s: got %v, want %v", c.name, got, c.affected)
		}
	}
}

func TestPartitionBatchForParallelism(t *testing.T) {
	ok := func(calls ...provider.ToolCallDelta) []provider.ToolCallDelta { return calls }
	blocked := []struct {
		name  string
		calls []provider.ToolCallDelta
	}{
		{"shell", ok(
			provider.ToolCallDelta{ID: "1", Name: "run_command", Arguments: []byte(`{"command":"ls"}`)},
			provider.ToolCallDelta{ID: "2", Name: "read_file", Arguments: []byte(`{"path":"/y/b.go"}`)},
		)},
		{"start_command", ok(
			provider.ToolCallDelta{ID: "1", Name: "start_command", Arguments: []byte(`{}`)},
			provider.ToolCallDelta{ID: "2", Name: "read_file", Arguments: []byte(`{"path":"/y/b.go"}`)},
		)},
		{"delegate", ok(
			provider.ToolCallDelta{ID: "1", Name: "delegate", Arguments: []byte(`{}`)},
			provider.ToolCallDelta{ID: "2", Name: "read_file", Arguments: []byte(`{"path":"/y/b.go"}`)},
		)},
		{"spawn", ok(
			provider.ToolCallDelta{ID: "1", Name: "spawn_agent", Arguments: []byte(`{}`)},
			provider.ToolCallDelta{ID: "2", Name: "read_file", Arguments: []byte(`{"path":"/y/b.go"}`)},
		)},
		{"warp", ok(
			provider.ToolCallDelta{ID: "1", Name: "warp", Arguments: []byte(`{}`)},
			provider.ToolCallDelta{ID: "2", Name: "read_file", Arguments: []byte(`{"path":"/y/b.go"}`)},
		)},
		{"whole_tree_git", ok(
			provider.ToolCallDelta{ID: "1", Name: "git_checkout", Arguments: []byte(`{"branch":"b"}`)},
			provider.ToolCallDelta{ID: "2", Name: "read_file", Arguments: []byte(`{"path":"/y/b.go"}`)},
		)},
		{"undo_edit_unknown_target", ok(
			provider.ToolCallDelta{ID: "1", Name: "undo_edit", Arguments: []byte(`{"path":"/x/a.go"}`)},
			provider.ToolCallDelta{ID: "2", Name: "read_file", Arguments: []byte(`{"path":"/y/b.go"}`)},
		)},
	}
	for _, c := range blocked {
		if _, schedulable := partitionBatchForParallelism(c.calls); schedulable {
			t.Errorf("%s: batch must not be schedulable", c.name)
		}
	}

	mutated, schedulable := partitionBatchForParallelism(ok(
		provider.ToolCallDelta{ID: "1", Name: "edit_file", Arguments: []byte(`{"file_path":"/x/a.go"}`)},
		provider.ToolCallDelta{ID: "2", Name: "write_file", Arguments: []byte(`{"path":"/x/c.go"}`)},
		provider.ToolCallDelta{ID: "3", Name: "read_file", Arguments: []byte(`{"path":"/y/b.go"}`)},
	))
	if !schedulable || len(mutated) != 2 {
		t.Fatalf("mixed file-scoped batch should be schedulable with 2 targets, got (%v, %v)", mutated, schedulable)
	}
}

// The integration invariant: with a registered read tool, a batch of
// [edit a.go, read a.go, read b.go] pre-executes ONLY the unrelated read;
// the colliding read is withheld for the sequential loop.
func TestPreExecuteReadOnly_PartialParallelMixedBatch(t *testing.T) {
	a := &Agent{
		tools:      tool.NewRegistry(),
		speculator: newSpeculator(),
	}
	if err := a.tools.Register(stubReadTool{name: "read_file"}); err != nil {
		t.Fatalf("register stub: %v", err)
	}
	calls := []provider.ToolCallDelta{
		{ID: "1", Name: "edit_file", Arguments: []byte(`{"file_path":"/x/a.go","old_text":"o","new_text":"n"}`)},
		{ID: "2", Name: "read_file", Arguments: []byte(`{"path":"/x/a.go"}`)}, // collides -> withheld
		{ID: "3", Name: "read_file", Arguments: []byte(`{"path":"/y/b.go"}`)}, // unrelated -> pre-executed
	}
	results := a.preExecuteReadOnlyTools(context.Background(), calls)
	if len(results) != 1 {
		t.Fatalf("want exactly 1 pre-executed result (the unrelated read), got %d: %+v", len(results), results)
	}
	if _, ok := results[2]; !ok {
		t.Fatalf("pre-executed result should be keyed by index 2 (unrelated read), got %+v", results)
	}
}

// Colliding-only batch degrades to the old all-skip behavior (nothing left to
// parallelize) - pins that #1475-A's scenario is still fully guarded.
func TestPreExecuteReadOnly_CollidingOnlyBatchNil(t *testing.T) {
	a := &Agent{
		tools:      tool.NewRegistry(),
		speculator: newSpeculator(),
	}
	if err := a.tools.Register(stubReadTool{name: "read_file"}); err != nil {
		t.Fatalf("register stub: %v", err)
	}
	calls := []provider.ToolCallDelta{
		{ID: "1", Name: "edit_file", Arguments: []byte(`{"file_path":"/x/a.go","old_text":"o","new_text":"n"}`)},
		{ID: "2", Name: "read_file", Arguments: []byte(`{"path":"/x/a.go"}`)},
	}
	if got := a.preExecuteReadOnlyTools(context.Background(), calls); len(got) != 0 {
		t.Fatalf("colliding read must be withheld from pre-exec, got %+v", got)
	}
}
