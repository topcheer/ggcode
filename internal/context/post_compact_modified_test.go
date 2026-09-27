package context

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// TestBuildPostCompactStateModifiedFilesSection pins the artifact-tracking
// split: files targeted by mutating tools (edit/write family) are listed
// under "Files modified this session" ahead of reads, and a file that was
// both read and edited appears once (in the modified section), not twice.
// Factory.ai's production audit found artifact tracking to be the weakest
// dimension of every summarization-based compaction method.
func TestBuildPostCompactStateModifiedFilesSection(t *testing.T) {
	cm := NewManager(4096)
	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "fix the bug"}}},
		{Role: "assistant", Content: []provider.ContentBlock{
			provider.ToolUseBlock("c1", "read_file", []byte(`{"path":"a_read.go"}`)),
		}},
		{Role: "assistant", Content: []provider.ContentBlock{
			provider.ToolUseBlock("c2", "edit_file", []byte(`{"file_path":"b_edit.go","old_text":"x","new_text":"y"}`)),
		}},
		{Role: "assistant", Content: []provider.ContentBlock{
			provider.ToolUseBlock("c3", "write_file", []byte(`{"path":"c_write.go","content":"x"}`)),
		}},
		// Re-read of the edited file must not duplicate it into Recent files.
		{Role: "assistant", Content: []provider.ContentBlock{
			provider.ToolUseBlock("c4", "read_file", []byte(`{"path":"b_edit.go"}`)),
		}},
		// grep also uses a "path" input key but never mutates: excluded.
		{Role: "assistant", Content: []provider.ContentBlock{
			provider.ToolUseBlock("c5", "grep", []byte(`{"path":"d_readonly.go","pattern":"x"}`)),
		}},
	}

	state := cm.buildPostCompactState(msgs)

	if !strings.Contains(state, "Files modified this session:") {
		t.Fatalf("expected modified-files section in state:\n%s", state)
	}
	if !strings.Contains(state, "- b_edit.go") || !strings.Contains(state, "- c_write.go") {
		t.Fatalf("expected edited files in modified section:\n%s", state)
	}
	modIdx := strings.Index(state, "Files modified this session:")
	recIdx := strings.Index(state, "Recent files:")
	if modIdx < 0 || recIdx < 0 || modIdx > recIdx {
		t.Fatalf("expected modified section before recent files:\n%s", state)
	}
	recentSection := state[recIdx:]
	for _, p := range []string{"b_edit.go", "c_write.go"} {
		if strings.Contains(recentSection, p) {
			t.Fatalf("modified path %s must not appear in Recent files section:\n%s", p, state)
		}
	}
	// grep's "path" input never mutates, so d_readonly.go belongs in Recent
	// files, not in the modified section.
	if !strings.Contains(recentSection, "- d_readonly.go") || !strings.Contains(recentSection, "- a_read.go") {
		t.Fatalf("expected read paths in Recent files section:\n%s", state)
	}
	modSection := state[:modIdx]
	if strings.Contains(modSection, "d_readonly.go") {
		t.Fatalf("readonly grep path must not appear in modified section:\n%s", state)
	}
}

// TestPostCompactStateChainCarriesModified pins chain safety: a second
// compaction must re-collect modified files from the prior [Post-compact
// state] message, so early-session edits survive repeated summarization.
func TestPostCompactStateChainCarriesModified(t *testing.T) {
	cm := NewManager(4096)
	prior := "[Post-compact state]\nFiles modified this session:\n- early.go\n\nRecent files:\n- old_read.go\n"
	msgs := []provider.Message{
		{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: prior}}},
		{Role: "assistant", Content: []provider.ContentBlock{
			provider.ToolUseBlock("c1", "multi_edit", []byte(`{"file_path":"late.go","edits":[]}`)),
		}},
	}

	state := cm.buildPostCompactState(msgs)

	if !strings.Contains(state, "- early.go") {
		t.Fatalf("chain-carried modified file lost:\n%s", state)
	}
	if !strings.Contains(state, "- late.go") {
		t.Fatalf("fresh modified file missing:\n%s", state)
	}
	if !strings.Contains(state, "- old_read.go") {
		t.Fatalf("chain-carried recent file lost:\n%s", state)
	}
}

// TestCollectToolFilePathsToolCoverage pins the mutating-tool set and the
// input keys that designate the mutated file across the edit/write family,
// including notebook_edit's notebook_path and apply_patch's required
// top-level path.
func TestCollectToolFilePathsToolCoverage(t *testing.T) {
	msgs := []provider.Message{
		{Role: "assistant", Content: []provider.ContentBlock{
			provider.ToolUseBlock("c1", "notebook_edit", []byte(`{"notebook_path":"nb.ipynb"}`)),
			provider.ToolUseBlock("c2", "apply_patch", []byte(`{"path":"patched.go","diff":"..."}`)),
			provider.ToolUseBlock("c3", "read_file", []byte(`{"path":"just_read.go"}`)),
		}},
	}
	got := collectToolFilePaths(msgs, modifiedFileLimit)
	want := []string{"nb.ipynb", "patched.go"}
	if len(got) != len(want) {
		t.Fatalf("collectToolFilePaths = %v, want %v", got, want)
	}
	for i, p := range want {
		if got[i] != p {
			t.Fatalf("collectToolFilePaths[%d] = %q, want %q", i, got[i], p)
		}
	}

	// Cap must be honored.
	small := collectToolFilePaths(msgs, 1)
	if len(small) != 1 || small[0] != "nb.ipynb" {
		t.Fatalf("limit not honored: %v", small)
	}
}

// TestAppendModifiedPathsCapsAndDedups pins the merge semantics used to
// combine chain-carried and fresh modified paths.
func TestAppendModifiedPathsCapsAndDedups(t *testing.T) {
	fresh := make([]string, 0, modifiedFileLimit+3)
	for i := 0; i < modifiedFileLimit+3; i++ {
		fresh = append(fresh, string(rune('a'+i))+".go")
	}
	got := appendModifiedPaths(nil, fresh)
	if len(got) != modifiedFileLimit {
		t.Fatalf("expected cap %d, got %d", modifiedFileLimit, len(got))
	}
	merged := appendModifiedPaths([]string{"a.go", "carried.go"}, []string{"a.go", "new.go"})
	if len(merged) != 3 || merged[0] != "a.go" || merged[1] != "carried.go" || merged[2] != "new.go" {
		t.Fatalf("unexpected merge result: %v", merged)
	}
}
