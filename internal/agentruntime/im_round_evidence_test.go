package agentruntime

import "testing"

func TestIMRoundStateNoteEditedFileDedupAndCap(t *testing.T) {
	r := &IMRoundState{}
	r.NoteEditedFile("  a.go  ")
	r.NoteEditedFile("a.go") // dedup (after trim)
	r.NoteEditedFile("")     // ignored
	if len(r.FilesEdited) != 1 || r.FilesEdited[0] != "a.go" {
		t.Fatalf("FilesEdited = %v, want [a.go]", r.FilesEdited)
	}

	capped := &IMRoundState{}
	for i := 0; i < maxRoundEditedFiles+5; i++ {
		capped.NoteEditedFile(string(rune('a'+i%26)) + string(rune('a'+i/26)) + ".go")
	}
	if len(capped.FilesEdited) != maxRoundEditedFiles {
		t.Fatalf("cap: len = %d, want %d", len(capped.FilesEdited), maxRoundEditedFiles)
	}

	capped.Reset()
	if capped.FilesEdited != nil || capped.ToolCalls != 0 {
		t.Fatalf("reset: FilesEdited = %v, ToolCalls = %d", capped.FilesEdited, capped.ToolCalls)
	}
}
