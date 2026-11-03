package memory

import (
	"encoding/json"
	"strings"
	"testing"
)

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

	if again := CapturePreferences(dir, "from now on always run tests with -p=1"); again != 0 {
		t.Fatalf("re-capture added = %d, want 0 (dedupe)", again)
	}
	got2, _ := autoMem.LoadKey("user-preferences")
	if strings.Count(got2, "-p=1") != strings.Count(got, "-p=1") {
		t.Fatal("dedupe changed entry count")
	}
}

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

func TestR414_NoFalsePositive(t *testing.T) {
	for _, text := range []string{
		"帮我用 pnpm 装依赖",
		"the port was changed to 8080",
	} {
		if got := DistillUserPreferences(text); len(got) != 0 {
			t.Fatalf("false positive on %q: %v", text, got)
		}
	}
}

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
