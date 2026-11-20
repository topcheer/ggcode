package permission

import (
	"strings"
	"testing"
)

// Fleet incident 2026-11-21: permission_rules.json accumulated two mojibake
// blobs (12.5MB / 1.2MB) that compiled into ~540MB of regexp instructions
// per seat - 80% of boot heap, 2.5GB startup RSS fleet-wide. These tests pin
// both guards: the length gate and the process-wide compile cache.

func TestCompileCommandPatternRejectsRunawayLength(t *testing.T) {
	giant := "# " + strings.Repeat("\u00c3\u0083", maxCommandPatternLen/2+16)
	re, err := compileCommandPattern(giant)
	if err == nil {
		t.Fatalf("expected ErrPatternTooLong for %d-byte pattern, got compiled regex %v", len(giant), re)
	}
	if err != ErrPatternTooLong {
		t.Fatalf("expected ErrPatternTooLong, got %v", err)
	}
}

func TestCompileCommandPatternCachedSharesObject(t *testing.T) {
	re1, err1 := compileCommandPatternCached("ls *")
	re2, err2 := compileCommandPatternCached("ls *")
	if err1 != nil || err2 != nil {
		t.Fatalf("unexpected errors: %v %v", err1, err2)
	}
	if re1 != re2 {
		t.Fatalf("expected the same *regexp.Regexp object from the cache, got two")
	}
}

func TestNewCommandRuleSetFromListsSkipsRunawayPattern(t *testing.T) {
	giant := "# " + strings.Repeat("x", maxCommandPatternLen+1)
	rs := NewCommandRuleSetFromLists([]string{"git diff*", giant}, nil)
	if got := len(rs.allow); got != 1 {
		t.Fatalf("expected only the valid pattern to compile (1), got %d", got)
	}
}
