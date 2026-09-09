package util

import "testing"

// #1849 case 2: width-arithmetic callers leak negative budgets on narrow
// terminals - the old contract returned the string untruncated and broke
// fixed-width panel rendering. Negative budget = no room = empty string.
func Test1849TruncateNegativeClamp(t *testing.T) {
	if got := Truncate("hello world", -3); got != "" {
		t.Fatalf("negative budget must yield empty string, got %q", got)
	}
	if got := Truncate("hello", -1); got != "" {
		t.Fatalf("negative budget must yield empty string, got %q", got)
	}
}

// Regression guards for the untouched contract points.
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
