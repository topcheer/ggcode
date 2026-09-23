package tool

// Deterministic browser-tool branches (sa-141): empty-profile status and
// URL scheme allow-list. No Chrome is launched.

import (
	"context"
	"strings"
	"testing"
)

func TestBrowserDoStatusEmptySa141(t *testing.T) {
	b := NewBrowser()
	r, err := b.doStatus()
	if err != nil || r.IsError || !strings.Contains(r.Content, "No active browser profiles") {
		t.Fatalf("doStatus empty -> (%+v,%v)", r, err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
}

func TestIsAllowedNavSchemeSa141(t *testing.T) {
	allowed := []string{"http://example.com", "https://example.com/x?y=1"}
	for _, u := range allowed {
		if !isAllowedNavScheme(u) {
			t.Errorf("isAllowedNavScheme(%q) = false, want true", u)
		}
	}
	denied := []string{"file:///etc/passwd", "javascript:alert(1)", "ftp://x", "data:text/html,x", ""}
	for _, u := range denied {
		if isAllowedNavScheme(u) {
			t.Errorf("isAllowedNavScheme(%q) = true, want false", u)
		}
	}
}

func TestChromeNotFoundHelpSa141(t *testing.T) {
	if got := chromeNotFoundHelp(); got == "" || !strings.Contains(got, "Chrome") {
		t.Fatalf("chromeNotFoundHelp = %q", got)
	}
}

func TestBrowserMetadataSa141(t *testing.T) {
	b := NewBrowser()
	if b.Name() != "browser" {
		t.Fatalf("Name() = %q", b.Name())
	}
	if b.Description() == "" {
		t.Fatal("empty description")
	}
	_ = context.Background()
}
