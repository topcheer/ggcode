package memory

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDistillUserPreferences_EN(t *testing.T) {
	prefs := DistillUserPreferences("Please fix the parser. From now on always run tests with -p=1 on this machine")
	if len(prefs) != 1 {
		t.Fatalf("want 1 pref, got %d: %v", len(prefs), prefs)
	}
	if !strings.Contains(prefs[0], "always run tests with -p=1") {
		t.Errorf("pref should contain the full sentence, got %q", prefs[0])
	}
}

func TestDistillUserPreferences_CJK(t *testing.T) {
	prefs := DistillUserPreferences("帮我修复解析器。以后都不要直接改 pubspec.yaml，用脚本同步")
	if len(prefs) != 1 {
		t.Fatalf("want 1 pref, got %d: %v", len(prefs), prefs)
	}
	if !strings.Contains(prefs[0], "不要直接改 pubspec.yaml") {
		t.Errorf("CJK pref sentence mismatch: %q", prefs[0])
	}
}

func TestDistillUserPreferences_None(t *testing.T) {
	for _, in := range []string{
		"",
		"just fix the bug in parser.go",
		"the test always passes now",
		"remember me?", // interrogative split, empty remainder
	} {
		if got := DistillUserPreferences(in); len(got) != 0 {
			t.Errorf("DistillUserPreferences(%q) = %v, want none", in, got)
		}
	}
}

func TestDistillUserPreferences_CapPerRun(t *testing.T) {
	in := strings.Join([]string{
		"From now on always use verbose logging.",
		"Never use git push --force on main.",
		"Going forward, prefer worktrees for releases.",
	}, " ")
	if got := DistillUserPreferences(in); len(got) != maxPreferencesPerRun {
		t.Errorf("want cap %d, got %d", maxPreferencesPerRun, len(got))
	}
}

func TestDistillUserPreferences_Dedupe(t *testing.T) {
	got := DistillUserPreferences("From now on always run make lint. From now on always run make lint")
	if len(got) != 1 {
		t.Errorf("duplicate sentences should collapse, got %v", got)
	}
}

func TestDistillUserPreferences_LengthCap(t *testing.T) {
	long := strings.Repeat("x ", 300) + " remember to check this"
	got := DistillUserPreferences(long)
	if len(got) != 1 {
		t.Fatalf("want 1, got %d", len(got))
	}
	if utf8.RuneCountInString(got[0]) > maxPreferenceLineLen+8 { // head-trimmed + marker
		t.Errorf("over-long sentence not truncated: %d runes", utf8.RuneCountInString(got[0]))
	}
}

func TestMergePreferenceMemory_AppendAndDedupe(t *testing.T) {
	existing := "- Always run tests with -p=1\n- Never force-push main\n"
	merged, added := MergePreferenceMemory(existing, []string{
		"always run tests with -p=1", // duplicate (case differs)
		"Going forward, prefer worktrees",
	})
	if added != 1 {
		t.Errorf("want 1 added, got %d", added)
	}
	if !strings.Contains(merged, "Going forward, prefer worktrees") {
		t.Errorf("new pref missing: %q", merged)
	}
	if !strings.Contains(merged, "-p=1") || strings.Count(merged, "-p=1") != 1 {
		t.Errorf("existing entry lost or duplicated: %q", merged)
	}
}

func TestMergePreferenceMemory_EntryCap(t *testing.T) {
	var lines []string
	for i := 0; i < maxPreferenceEntries+5; i++ {
		lines = append(lines, fmt.Sprintf("rule number %d", i))
	}
	merged, added := MergePreferenceMemory(strings.Join(lines, "\n"), nil)
	if added != 0 {
		t.Fatalf("no new prefs, added=%d", added)
	}
	got := strings.Count(merged, "\n")
	if got > maxPreferenceEntries {
		t.Errorf("entries %d exceed cap %d", got, maxPreferenceEntries)
	}
	if !strings.Contains(merged, "rule number 34") || strings.Contains(merged, "rule number 0") {
		t.Errorf("oldest entries should drop, newest stay: %q", merged)
	}
}
