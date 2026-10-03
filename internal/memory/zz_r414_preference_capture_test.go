package memory

// r414 probes: CapturePreferences shared run-terminal hook (TUI/pipe/IM)
// and the widened durable-intent marker set.

import (
	"encoding/json"
	"strings"
	"testing"
)

// End-to-end: stated preference lands in the user-preferences key and a
// second identical capture is a no-op (merge dedupe).
func TestR414_CapturePreferencesEndToEnd(t *testing.T) {
	dir := t.TempDir()
	added := CapturePreferences(dir, "from now on always run tests with -p=1")
	if added != 1 {
		t.Fatalf("added = %d, want 1", added)
	}
	autoMem := NewProjectAutoMemory(dir)
	if autoMem == nil {
		t.Fatal("NewProjectAutoMemory nil after capture")
	}
	got, err := autoMem.LoadKey("user-preferences")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "-p=1") {
		t.Fatalf("preference not persisted:\n%s", got)
	}

	// Re-capture of the same statement must not duplicate.
	if again := CapturePreferences(dir, "from now on always run tests with -p=1"); again != 0 {
		t.Fatalf("re-capture added = %d, want 0 (dedupe)", again)
	}
	got2, _ := autoMem.LoadKey("user-preferences")
	if strings.Count(got2, "-p=1") != strings.Count(got, "-p=1") {
		t.Fatal("dedupe changed entry count")
	}
}

// Late-position preference (after 300+ chars of log noise) is still
// captured - the truncation-loss scenario the entry points now avoid by
// feeding full input instead of the 200-char UserPrompt.
func TestR414_LatePositionPreference(t *testing.T) {
	noise := strings.Repeat("build log line with output content here\n", 12)
	text := noise + "\n以后改用 pnpm，不要再用 npm 了"
	prefs := DistillUserPreferences(text)
	if len(prefs) == 0 {
		t.Fatal("late-position preference lost")
	}
	if !strings.Contains(prefs[0], "pnpm") {
		t.Fatalf("captured = %q, want the pnpm statement", prefs[0])
	}
}

// New markers: 以后改用 / 以后用 / from here on.
func TestR414_NewMarkers(t *testing.T) {
	for _, text := range []string{
		"以后改用 pnpm",
		"以后用 tab 缩进",
		"from here on, quote all paths",
	} {
		if got := DistillUserPreferences(text); len(got) != 1 {
			t.Fatalf("DistillUserPreferences(%q) = %v, want 1", text, got)
		}
	}
}

// Non-preference text stays uncaptured (precision guard for the widened
// marker set: "用" alone must not fire via 以后用 etc.).
func TestR414_NoFalsePositive(t *testing.T) {
	for _, text := range []string{
		"帮我用 pnpm 装依赖",                 // 用 without 以后
		"the port was changed to 8080", // change statement, not preference
	} {
		if got := DistillUserPreferences(text); len(got) != 0 {
			t.Fatalf("false positive on %q: %v", text, got)
		}
	}
}

// Entry cap still enforced through the capture path.
func TestR414_CaptureEntryCap(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 20; i++ {
		CapturePreferences(dir, "from now on prefer option "+string(rune('a'+i)))
	}
	autoMem := NewProjectAutoMemory(dir)
	got, err := autoMem.LoadKey("user-preferences")
	if err != nil {
		t.Fatal(err)
	}
	// maxPreferenceEntries bounds stored entries; bullet lines are entries.
	lines := 0
	for _, l := range strings.Split(got, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "- ") {
			lines++
		}
	}
	if lines > maxPreferenceEntries {
		t.Fatalf("stored %d entries, cap is %d", lines, maxPreferenceEntries)
	}
}

// The persisted doc must remain plain text (no JSON escaping surprises
// for CJK content).
func TestR414_CJKPersistedVerbatim(t *testing.T) {
	dir := t.TempDir()
	CapturePreferences(dir, "以后都不要动 docs/ 下的文件")
	autoMem := NewProjectAutoMemory(dir)
	got, err := autoMem.LoadKey("user-preferences")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "docs/") {
		t.Fatalf("CJK preference not verbatim:\n%s", got)
	}
	if json.Valid([]byte(got)) && strings.Contains(got, `\u`) {
		t.Fatalf("CJK escaped in storage:\n%s", got)
	}
}
