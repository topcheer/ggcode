package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestLoopDetector_ConsecutiveErrors(t *testing.T) {
	var ld loopDetector

	g := ld.recordResult(true, "edit_file")
	if g != "" {
		t.Errorf("expected no guidance on first error, got: %s", g)
	}

	g = ld.recordResult(true, "run_command")
	if g != "" {
		t.Errorf("expected no guidance on second error, got: %s", g)
	}

	g = ld.recordResult(true, "write_file")
	if g != "" {
		t.Errorf("expected no guidance on third error, got: %s", g)
	}

	g = ld.recordResult(true, "edit_file")
	if g == "" {
		t.Error("expected guidance on fourth consecutive error, got empty string")
	}
	if !strings.Contains(g, "consecutive tool calls") {
		t.Errorf("expected guidance to mention consecutive errors, got: %s", g)
	}

	g = ld.recordResult(true, "edit_file")
	if g != "" {
		t.Errorf("expected no repeat guidance after already given, got: %s", g)
	}
}

func TestLoopDetector_ErrorStreakResetsOnSuccess(t *testing.T) {
	var ld loopDetector

	ld.recordResult(true, "edit_file")
	ld.recordResult(true, "edit_file")
	ld.recordResult(true, "run_command")

	g := ld.recordResult(false, "read_file")
	if g != "" {
		t.Errorf("expected no guidance on success, got: %s", g)
	}

	ld.recordResult(true, "edit_file")
	ld.recordResult(true, "edit_file")
	ld.recordResult(true, "edit_file")
	g = ld.recordResult(true, "edit_file")
	if g == "" {
		t.Error("expected guidance after 4 new errors post-reset")
	}
}

func TestLoopDetector_MixedErrorsAndDuplicates(t *testing.T) {
	var ld loopDetector

	tc1 := testToolCall("edit_file", `{"file_path":"/tmp/a.go","old_text":"x","new_text":"y"}`)
	tc2 := testToolCall("edit_file", `{"file_path":"/tmp/b.go","old_text":"z","new_text":"w"}`)

	g1 := ld.checkDuplicate(tc1)
	if g1 != "" {
		t.Errorf("first call should not trigger duplicate warning: %s", g1)
	}

	g2 := ld.checkDuplicate(tc2)
	if g2 != "" {
		t.Errorf("different call should not trigger duplicate warning: %s", g2)
	}

	g := ld.recordResult(true, "edit_file")
	if g != "" {
		t.Error("should not trigger at 1 error")
	}
	g = ld.recordResult(true, "edit_file")
	if g != "" {
		t.Error("should not trigger at 2 errors")
	}
	g = ld.recordResult(true, "run_command")
	if g != "" {
		t.Error("should not trigger at 3 errors")
	}
	g = ld.recordResult(true, "write_file")
	if g == "" {
		t.Error("should trigger level 1 guidance at 4 errors")
	}
	if ld.errorGuidanceLevel != 1 {
		t.Errorf("expected errorGuidanceLevel=1 after 4 errors, got %d", ld.errorGuidanceLevel)
	}

	g = ld.recordResult(true, "edit_file")
	if g != "" {
		t.Error("should not re-trigger level 1 at 5 errors")
	}
	g = ld.recordResult(true, "edit_file")
	if g != "" {
		t.Error("should not re-trigger level 1 at 6 errors")
	}

	g = ld.recordResult(true, "run_command")
	if g == "" {
		t.Error("should trigger level 2 guidance at 7 errors")
	}
	if ld.errorGuidanceLevel != 2 {
		t.Errorf("expected errorGuidanceLevel=2 after 7 errors, got %d", ld.errorGuidanceLevel)
	}

	g = ld.recordResult(true, "edit_file")
	if g != "" {
		t.Error("should not re-trigger level 2 at 8 errors")
	}
	g = ld.recordResult(true, "edit_file")
	if g != "" {
		t.Error("should not re-trigger level 2 at 9 errors")
	}

	g = ld.recordResult(true, "write_file")
	if g == "" {
		t.Error("should trigger level 3 guidance at 10 errors")
	}
	if ld.errorGuidanceLevel != 3 {
		t.Errorf("expected errorGuidanceLevel=3 after 10 errors, got %d", ld.errorGuidanceLevel)
	}

	g = ld.recordResult(true, "edit_file")
	if g != "" {
		t.Error("should not trigger anything after level 3")
	}

	g = ld.recordResult(false, "edit_file")
	if g != "" {
		t.Error("success should not trigger guidance")
	}
	if ld.errorGuidanceLevel != 0 {
		t.Errorf("expected errorGuidanceLevel=0 after success, got %d", ld.errorGuidanceLevel)
	}
	if ld.consecutiveErrors != 0 {
		t.Errorf("expected consecutiveErrors=0 after success, got %d", ld.consecutiveErrors)
	}
}

func testToolCall(name string, args string) provider.ToolCallDelta {
	return provider.ToolCallDelta{
		Name:      name,
		Arguments: []byte(args),
	}
}

func TestLoopDetector_LongStreakPeriodicRewarn(t *testing.T) {
	var ld loopDetector
	tc := testToolCall("read_file", `{"path":"/big/file.go"}`)
	saw3, saw5, saw10 := false, false, false
	for i := 0; i < 10; i++ {
		g := ld.checkDuplicate(tc)
		switch i {
		case 2:
			saw3 = g != ""
		case 4:
			saw5 = g != ""
		case 9:
			saw10 = g != ""
		default:
			if g != "" {
				t.Fatalf("unexpected warning at streak %d: %s", i+1, g)
			}
		}
	}
	if !saw3 || !saw5 {
		t.Fatalf("baseline warnings missing: saw3=%v saw5=%v", saw3, saw5)
	}
	if !saw10 {
		t.Fatal("streak 10 stayed silent - periodic re-warn missing")
	}
}
