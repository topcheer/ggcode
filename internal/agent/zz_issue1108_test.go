package agent

import (
	"strings"
	"testing"
)

func TestIssue1108_ForInitLoopDowngradedOnGo122(t *testing.T) {
	inst := loopCaptureInstance{
		varName:  "i",
		kind:     "goroutine",
		loopType: "for",
	}
	msg := formatLoopCaptureWarning(inst, true)
	if strings.Contains(msg, "classic Go gotcha") {
		t.Fatalf("for-init loop capture must be downgraded on Go 1.22+ (#1108), got: %s", msg)
	}

	msg = formatLoopCaptureWarning(inst, false)
	if !strings.Contains(msg, "classic Go gotcha") {
		t.Fatalf("pre-1.22 must keep the classic gotcha warning, got: %s", msg)
	}
}

func TestIssue1112_DeferBranchDowngradedOnGo122(t *testing.T) {
	inst := loopCaptureInstance{
		varName:  "item",
		kind:     "defer",
		loopType: "range",
	}
	msg := formatLoopCaptureWarning(inst, true)
	if strings.Contains(msg, "final value") {
		t.Fatalf("defer branch must be downgraded on Go 1.22+ (#1112), got: %s", msg)
	}
	if !strings.Contains(msg, "defer func() { use(item) }(item)") {
		t.Fatalf("defer branch needs a defer-appropriate hint (#1112), got: %s", msg)
	}

	msg = formatLoopCaptureWarning(inst, false)
	if !strings.Contains(msg, "final value") {
		t.Fatalf("pre-1.22 defer branch must keep the final-value warning, got: %s", msg)
	}
}

func TestIssue1123_GoModVersionParsing(t *testing.T) {
	cases := []struct {
		name    string
		goMod   string
		want122 bool
	}{
		{"1.21", "module x\n\ngo 1.21\n", false},
		{"1.9", "module x\n\ngo 1.9\n", false},
		{"1.22", "module x\n\ngo 1.22\n", true},
		{"1.22.0", "module x\n\ngo 1.22.0\n", true},
		{"1.26.2", "module x\n\ngo 1.26.2\n", true},
		{"go2", "module x\n\ngo 2.0\n", true},
		{"toolchain only", "module x\n\ntoolchain go1.26.2\n", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := goModDeclares122Plus([]byte(tc.goMod)); got != tc.want122 {
				t.Fatalf("goModDeclares122Plus(%q) = %v, want %v", tc.goMod, got, tc.want122)
			}
		})
	}
}
