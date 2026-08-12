package agent

import (
	"fmt"
	"strings"
	"testing"
)

// TestCheckCopylock_TruncationBug verifies the truncation count bug:
// When delta-aware filtering skips pre-existing issues, the truncation message
// incorrectly uses len(issues) (which includes skipped old issues) instead of
// the actual count of remaining NEW issues.
//
// Bug scenario:
// - New content has 11 issues (5 old + 6 new)
// - Delta filter skips 5 old issues
// - After adding 4 new warnings, we trigger truncation
// - Current code: len(issues) - len(warnings) = 11 - 4 = 7 (WRONG - includes old issues)
// - Correct: should count only NEW issues = 6 - 4 = 2
func TestCheckCopylock_TruncationBug(t *testing.T) {
	// Old content with 5 pre-existing copylock violations
	oldSrc := `package main

import "sync"

func old1(a sync.Mutex) {}
func old2(b sync.Mutex) {}
func old3(c sync.Mutex) {}
func old4(d sync.Mutex) {}
func old5(e sync.Mutex) {}
`

	// New content adds 6 NEW copylock violations (total = 11 issues)
	newSrc := `package main

import "sync"

func old1(a sync.Mutex) {}
func old2(b sync.Mutex) {}
func old3(c sync.Mutex) {}
func old4(d sync.Mutex) {}
func old5(e sync.Mutex) {}

func new1(f sync.Mutex) {}
func new2(g sync.Mutex) {}
func new3(h sync.Mutex) {}
func new4(i sync.Mutex) {}
func new5(j sync.Mutex) {}
func new6(k sync.Mutex) {}
`

	result := checkCopylock("foo.go", oldSrc, newSrc)

	if len(result) == 0 {
		t.Fatal("expected warnings for new copylock issues, got none")
	}

	// Find the truncation message
	var truncationMsg string
	for _, w := range result {
		if strings.Contains(w, "more copylock issue") {
			truncationMsg = w
			break
		}
	}

	if truncationMsg == "" {
		t.Fatal("expected truncation message, found none")
	}

	// Extract the number from the truncation message
	// Expected format: "...and %d more copylock issue(s)"
	var reportedCount int
	_, err := fmt.Sscanf(truncationMsg, "...and %d more", &reportedCount)
	if err != nil {
		t.Fatalf("failed to parse truncation message '%s': %v", truncationMsg, err)
	}

	// The bug: reportedCount will be 7 (11 total - 4 shown)
	// But it should be 2 (6 new - 4 shown = 2 remaining new issues)
	expectedCount := 2

	if reportedCount != expectedCount {
		t.Errorf("BUG CONFIRMED: truncation message reports %d more issues, but should report %d\n"+
			"Explanation:\n"+
			"  - Total issues in new content: 11 (5 old + 6 new)\n"+
			"  - Delta filter skipped: 5 old issues\n"+
			"  - Warnings shown: 4 new issues\n"+
			"  - Remaining NEW issues: 2 (6 new - 4 shown)\n"+
			"  - Bug calculates: len(issues) - len(warnings) = 11 - 4 = 7 (WRONG!)\n"+
			"  - Correct calculation: remaining NEW issues = 2\n"+
			"Truncation message: %s",
			reportedCount, expectedCount, truncationMsg)
	}

	// Count how many new warnings we actually have (should be 4 or 5 due to truncation)
	// Should be: 4 shown + 1 truncation message = 5 total
	// But the truncation message says "7 more" which is wrong
	t.Logf("Truncation message: %s", truncationMsg)
	t.Logf("Reported count: %d, Actual remaining new issues: %d", reportedCount, expectedCount)
}
