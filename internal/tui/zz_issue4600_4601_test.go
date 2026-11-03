package tui

import (
	"os"
	"strings"
	"testing"
)

func TestIssue4601_NilManagerUnbindReturnsError(t *testing.T) {
	m := Model{}
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

	if !strings.Contains(src, `if url == "" {
		return nil
	}`) {
		t.Fatal("buildCustomResolved url guard changed - collection/resolution alignment broken")
	}
}
