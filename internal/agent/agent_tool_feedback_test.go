package agent

import (
	"strings"
	"testing"
)

func TestUserRejectedMessage_WithoutFeedback(t *testing.T) {
	got := userRejectedMessage("edit_file", "")
	want := `Permission denied for tool "edit_file". User rejected the request.`
	if got != want {
		t.Fatalf("bare rejection message changed:\n got: %s\nwant: %s", got, want)
	}
}

func TestUserRejectedMessage_WhitespaceOnlyFeedback(t *testing.T) {
	// Whitespace-only feedback must degrade to the bare message, not ship
	// an empty "adjust:" section to the model.
	got := userRejectedMessage("edit_file", "   \n\t ")
	want := `Permission denied for tool "edit_file". User rejected the request.`
	if got != want {
		t.Fatalf("whitespace feedback should degrade to bare message:\n got: %s\nwant: %s", got, want)
	}
}

func TestUserRejectedMessage_WithFeedback(t *testing.T) {
	got := userRejectedMessage("run_command", "don't touch main.go, use the config file")
	for _, want := range []string{
		`Permission denied for tool "run_command".`,
		"asked you to adjust:",
		"don't touch main.go, use the config file",
		"Do NOT retry the identical call",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("feedback message missing %q:\n%s", want, got)
		}
	}
}
