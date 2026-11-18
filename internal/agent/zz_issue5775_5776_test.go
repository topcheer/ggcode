package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// #5775/#5776: byte-slice truncation produced invalid UTF-8 on CJK-mixed
// strings. All truncation/excerpt paths must emit valid UTF-8.

const cjkMixed = "重构 userLoginHandler 模块的鉴权逻辑并补充相应的错误处理 including retry"

func Test5775TruncatePlanItemCJKValidUTF8(t *testing.T) {
	got := truncatePlanItem(cjkMixed, 30)
	if !utf8.ValidString(got) {
		t.Fatalf("truncatePlanItem produced invalid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("long input must be ellipsized, got %q", got)
	}
	if got := truncatePlanItem("short", 30); got != "short" {
		t.Fatalf("short input must pass through, got %q", got)
	}
}

func Test5775PlanStepExcerptCJKValidUTF8(t *testing.T) {
	long := cjkMixed + strings.Repeat("补", 40)
	got := planStepExcerpt(long)
	if !utf8.ValidString(got) {
		t.Fatalf("planStepExcerpt produced invalid UTF-8: %q", got)
	}
}

func Test5776RuneBoundaryHelpers(t *testing.T) {
	// Floor into the middle of a 3-byte rune must back up to its start.
	mid := 1 + 3 + 1 // past "重" (3B) into the middle of the next rune
	if got := runeBoundaryFloor(cjkMixed, mid); !utf8.RuneStart(cjkMixed[got]) {
		t.Fatalf("floor must land on a rune start, got %d", got)
	}
	if got := runeBoundaryCeil(cjkMixed, mid); !utf8.RuneStart(cjkMixed[got]) {
		t.Fatalf("ceil must land on a rune start, got %d", got)
	}
	if runeBoundaryFloor(cjkMixed, 0) != 0 {
		t.Fatal("floor at 0 must stay 0")
	}
}
