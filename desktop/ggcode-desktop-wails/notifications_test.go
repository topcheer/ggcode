package main

import (
	"testing"
)

func TestNewNotificationManager(t *testing.T) {
	nm := NewNotificationManager()
	if nm == nil {
		t.Fatal("NewNotificationManager returned nil")
	}
	if !nm.enabled {
		t.Error("expected enabled=true by default")
	}
	if !nm.focused {
		t.Error("expected focused=true by default")
	}
	if nm.GetUnread() != 0 {
		t.Error("expected unread=0 by default")
	}
}

func TestSetFocused(t *testing.T) {
	nm := NewNotificationManager()
	nm.SetFocused(false)
	if nm.focused {
		t.Error("expected focused=false after SetFocused(false)")
	}

	// Set unread, then focus should reset it
	nm.unread = 5
	nm.SetFocused(true)
	if !nm.focused {
		t.Error("expected focused=true after SetFocused(true)")
	}
	if nm.GetUnread() != 0 {
		t.Error("expected unread reset to 0 on focus")
	}
}

func TestSetEnabled(t *testing.T) {
	nm := NewNotificationManager()
	nm.SetEnabled(false)
	if nm.enabled {
		t.Error("expected enabled=false after SetEnabled(false)")
	}

	// Disabling should clear unread
	nm.unread = 3
	nm.SetEnabled(false)
	if nm.GetUnread() != 0 {
		t.Error("expected unread reset to 0 on disable")
	}
}

func TestNotifySuppressedWhenFocused(t *testing.T) {
	nm := NewNotificationManager()
	nm.SetFocused(true) // window is focused
	nm.Notify("Test", "Should be suppressed")
	if nm.GetUnread() != 0 {
		t.Error("notification should be suppressed when focused")
	}
}

func TestNotifySuppressedWhenDisabled(t *testing.T) {
	nm := NewNotificationManager()
	nm.SetFocused(false) // window not focused
	nm.SetEnabled(false) // but notifications disabled
	nm.Notify("Test", "Should be suppressed")
	if nm.GetUnread() != 0 {
		t.Error("notification should be suppressed when disabled")
	}
}

func TestNotifyIncrementsUnreadWhenNotFocused(t *testing.T) {
	nm := NewNotificationManager()
	nm.SetFocused(false) // window not focused
	nm.Notify("Test", "Should increment unread")
	if nm.GetUnread() != 1 {
		t.Errorf("expected unread=1, got %d", nm.GetUnread())
	}
	nm.Notify("Test2", "Another notification")
	if nm.GetUnread() != 2 {
		t.Errorf("expected unread=2, got %d", nm.GetUnread())
	}
}

func TestNotifyApprovalNeededWhenNotFocused(t *testing.T) {
	nm := NewNotificationManager()
	nm.SetFocused(false)
	nm.NotifyApprovalNeeded("Approval", "Need your input")
	if nm.GetUnread() != 1 {
		t.Errorf("expected unread=1, got %d", nm.GetUnread())
	}
}

func TestNotifyApprovalNeededWhenFocused(t *testing.T) {
	nm := NewNotificationManager()
	nm.SetFocused(true)
	// Approval notifications fire even when focused, but don't bump badge
	nm.NotifyApprovalNeeded("Approval", "Need your input")
	if nm.GetUnread() != 0 {
		t.Error("approval notification should not increment badge when focused")
	}
}

func TestClearUnread(t *testing.T) {
	nm := NewNotificationManager()
	nm.SetFocused(false)
	nm.Notify("Test", "Message 1")
	nm.Notify("Test", "Message 2")
	if nm.GetUnread() != 2 {
		t.Fatalf("expected unread=2, got %d", nm.GetUnread())
	}
	nm.ClearUnread()
	if nm.GetUnread() != 0 {
		t.Error("expected unread=0 after ClearUnread")
	}
}

func TestItoa(t *testing.T) {
	tests := []struct {
		input    int
		expected string
	}{
		{0, "0"},
		{1, "1"},
		{42, "42"},
		{100, "100"},
	}
	for _, tt := range tests {
		got := itoa(tt.input)
		if got != tt.expected {
			t.Errorf("itoa(%d) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
