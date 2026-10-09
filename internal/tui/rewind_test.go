package tui

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/checkpoint"
)

func TestBuildRewindMemory_ListsDedupedFiles(t *testing.T) {
	reverted := []checkpoint.Checkpoint{
		{FilePath: "/w/src/a.go", ToolCall: "edit_file"},
		{FilePath: "/w/src/a.go", ToolCall: "edit_file"}, // same file, deduped
		{FilePath: "/w/src/b.go", ToolCall: "write_file"},
	}
	got := buildRewindMemory(reverted, 2)
	for _, want := range []string{"rewind memory", "2 turn(s)", "a.go", "b.go", "tried-and-rolled-back"} {
		if !strings.Contains(got, want) {
			t.Errorf("memory missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "a.go") != 1 {
		t.Errorf("file list not deduped:\n%s", got)
	}
}

func TestBuildRewindMemory_NoEdits(t *testing.T) {
	got := buildRewindMemory(nil, 1)
	if !strings.Contains(got, "no tracked file edits") {
		t.Errorf("expected no-edits note, got:\n%s", got)
	}
}

func TestBuildRewindMemory_CapsFileList(t *testing.T) {
	var reverted []checkpoint.Checkpoint
	for i := 0; i < 15; i++ {
		reverted = append(reverted, checkpoint.Checkpoint{
			FilePath: strings.Repeat("d", i%3) + "/f" + strings.Repeat("0", 4-i%10*0) + string(rune('a'+i)) + ".go",
			ToolCall: "edit_file",
		})
	}
	// 15 distinct files: cap is 10 listed + "+5 more".
	got := buildRewindMemory(reverted, 1)
	if !strings.Contains(got, "+5 more") {
		t.Errorf("expected '+5 more' cap marker, got:\n%s", got)
	}
}

func TestRewindFileList_SortedAndDeduped(t *testing.T) {
	files := rewindFileList([]checkpoint.Checkpoint{
		{FilePath: "/w/z.go"},
		{FilePath: "/w/a.go"},
		{FilePath: "/w/z.go"},
		{FilePath: ""}, // skipped
	})
	want := "a.go, z.go"
	if strings.Join(files, ", ") != want {
		t.Errorf("want %q, got %q", want, strings.Join(files, ", "))
	}
}
