package util

import "testing"

func Test1849TruncateNegativeClamp(t *testing.T) {
	if got := Truncate("hello world", -3); got != "" {
		t.Fatalf("negative budget must yield empty string, got %q", got)
	}
	if got := Truncate("hello", -1); got != "" {
		t.Fatalf("negative budget must yield empty string, got %q", got)
	}
}

func Test1849TruncateContract(t *testing.T) {
	if got := Truncate("hello", 5); got != "hello" {
		t.Fatalf("exact fit must return as-is, got %q", got)
	}
	if got := Truncate("hello world", 8); got != "hello..." {
		t.Fatalf("ellipsis truncation broken, got %q", got)
	}
	if got := Truncate("hello", 0); got != "" {
		t.Fatalf("zero budget must be empty, got %q", got)
	}
}
