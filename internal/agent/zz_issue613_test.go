package agent

import (
	"strings"
	"testing"
)

func TestIssue613_ForesightResultEmptyHelper(t *testing.T) {
	if !foresightResultEmpty("") {
		t.Fatal("empty string must be empty")
	}
	if foresightResultEmpty(strings.Repeat("return ErrEmpty\n", 20)) {
		t.Fatal("long content containing 'empty' must not be empty")
	}
	if !foresightResultEmpty("no results found") {
		t.Fatal("structured no-results marker must be empty")
	}
	if foresightResultEmpty("package main\nfunc f() {}") {
		t.Fatal("short real content must not be empty")
	}
}
