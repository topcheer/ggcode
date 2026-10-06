package daemon

import (
	"os"
	"testing"
)

// TestColorEnabledRespectsNoColor (sa-46): NO_COLOR (de-facto standard,
// no-color.org) must disable ANSI emission for the follow renderer - CI
// logs, piped output, and dumb terminals render raw escapes as garbage
// and screen readers announce them as bracket noise.
func TestColorEnabledRespectsNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if colorEnabled() {
		t.Fatal("NO_COLOR set but colorEnabled() still true")
	}
}

// TestColorEnabledNonTTY: piped stdout (os.ModeCharDevice unset) must also
// disable colors - `ggcode follow > log.txt` must not embed escapes in
// the file. In the test harness stdout is already a pipe, so colorEnabled
// exercising the real os.Stdout.Stat path proves the non-TTY branch.
func TestColorEnabledNonTTY(t *testing.T) {
	// Ensure the NO_COLOR branch is not what satisfies this test: unset
	// it (t.Setenv registers the restore) so only the Stat/ModeCharDevice
	// branch can return false.
	t.Setenv("NO_COLOR", "")
	os.Unsetenv("NO_COLOR")
	if fi, err := os.Stdout.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		t.Skip("stdout is a real terminal; non-TTY branch not exercisable")
	}
	if colorEnabled() {
		t.Fatal("non-TTY stdout but colorEnabled() still true")
	}
}
