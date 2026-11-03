package agent

import (
	"fmt"
	"strings"
	"testing"
)

func TestCheckCopylock_TruncationBug(t *testing.T) {

	oldSrc := `package main

import "sync"

func old1(a sync.Mutex) {}
func old2(b sync.Mutex) {}
func old3(c sync.Mutex) {}
func old4(d sync.Mutex) {}
func old5(e sync.Mutex) {}
`

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

	var reportedCount int
	_, err := fmt.Sscanf(truncationMsg, "...and %d more", &reportedCount)
	if err != nil {
		t.Fatalf("failed to parse truncation message '%s': %v", truncationMsg, err)
	}

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

	t.Logf("Truncation message: %s", truncationMsg)
	t.Logf("Reported count: %d, Actual remaining new issues: %d", reportedCount, expectedCount)
}
