package agent

import "testing"

func TestIsEditingTool(t *testing.T) {
	editTools := []string{"edit_file", "multi_edit_file", "multi_file_edit", "write_file", "notebook_edit"}
	for _, tool := range editTools {
		if !isEditingTool(tool) {
			t.Errorf("isEditingTool(%q) = false, want true", tool)
		}
	}
	nonEdit := []string{"read_file", "run_command", "grep", "search_files"}
	for _, tool := range nonEdit {
		if isEditingTool(tool) {
			t.Errorf("isEditingTool(%q) = true, want false", tool)
		}
	}
}
