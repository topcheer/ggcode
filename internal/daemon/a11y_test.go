package daemon

import (
	"os"
	"testing"
)

func TestColorEnabledRespectsNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if colorEnabled() {
		t.Fatal("NO_COLOR set but colorEnabled() still true")
	}
}

func TestColorEnabledNonTTY(t *testing.T) {

	t.Setenv("NO_COLOR", "")
	os.Unsetenv("NO_COLOR")
	if fi, err := os.Stdout.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		t.Skip("stdout is a real terminal; non-TTY branch not exercisable")
	}
	if colorEnabled() {
		t.Fatal("non-TTY stdout but colorEnabled() still true")
	}
}
