package tui

import (
	"os"
	"strings"
	"testing"
)

// #4601: unbind and clear must surface an error when the IM manager is nil,
// matching the bind path - an empty result left panel.message blank, so the
// keypress was silently swallowed.
func TestIssue4601_NilManagerUnbindReturnsError(t *testing.T) {
	m := Model{} // imManager nil
	msg := m.unbindNostrEntry("test-adapter")()
	res, ok := msg.(nostrBindResultMsg)
	if !ok {
		t.Fatalf("want nostrBindResultMsg, got %T", msg)
	}
	if res.err == nil {
		t.Fatal("#4601 unbind with nil manager must return an error, got nil")
	}
	if res.message != "" {
		t.Fatalf("error path must not carry a success message, got %q", res.message)
	}
}

func TestIssue4601_NilManagerClearReturnsError(t *testing.T) {
	m := Model{}
	msg := m.clearNostrChannel("test-adapter")()
	res, ok := msg.(nostrBindResultMsg)
	if !ok {
		t.Fatalf("want nostrBindResultMsg, got %T", msg)
	}
	if res.err == nil {
		t.Fatal("#4601 clear with nil manager must return an error, got nil")
	}
}

// #4600: the collection guard must require both name AND url. This pins the
// guard shape inside RunOnboard (the collect step is inline in the interactive
// flow); a regression to name-only re-opens the half-filled-Custom leak.
func TestIssue4600_CustomCollectionGuardRequiresNameAndURL(t *testing.T) {
	srcB, err := os.ReadFile("onboard.go")

	src := string(srcB)
	if err != nil {
		t.Fatalf("read onboard.go: %v", err)
	}
	want := `strings.TrimSpace(om.customFields[0].Value()) != "" && strings.TrimSpace(om.customFields[1].Value()) != ""`
	if !strings.Contains(src, want) {
		t.Fatal("#4600 guard regressed: collection no longer requires both name (field 0) and url (field 1)")
	}
	// buildCustomResolved must still treat url=="" as no-endpoint so the two
	// validation paths stay aligned.
	if !strings.Contains(src, `if url == "" {
		return nil
	}`) {
		t.Fatal("buildCustomResolved url guard changed - collection/resolution alignment broken")
	}
}
