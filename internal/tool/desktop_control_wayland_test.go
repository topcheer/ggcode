package tool

import (
	"strings"
	"testing"
)

// The Wayland argv builders are pure and untagged so the Linux path is
// unit-testable on every platform (CI runs on darwin).

func TestWindowTitleMatches(t *testing.T) {
	cases := []struct {
		title, needle string
		want          bool
	}{
		{"Visual Studio Code — main.go", "code", true}, // case-insensitive substring
		{"Terminal — zsh", "ZSH", true},                // case-insensitive
		{"Safari", "saf", true},                        // prefix substring
		{"Safari", "firefox", false},                   // no match
		{"Anything", "", false},                        // empty target NEVER matches
		{"Anything", "   ", false},                     // whitespace-only never matches
	}
	for _, c := range cases {
		if got := windowTitleMatches(c.title, c.needle); got != c.want {
			t.Errorf("windowTitleMatches(%q, %q) = %v, want %v", c.title, c.needle, got, c.want)
		}
	}
}

func TestATSPIFindAndFormat(t *testing.T) {
	els := []ATSPIElement{
		{Role: "push button", Name: "Save", X: 10, Y: 20, W: 60, H: 30},
		{Role: "text", Name: "", X: 0, Y: 0, W: 0, H: 0}, // unnamed: never matches
		{Role: "label", Name: "AutoSave interval", X: 5, Y: 5, W: 90, H: 12},
	}
	// Case-insensitive substring.
	m := atspiFind(els, "save")
	if len(m) != 2 {
		t.Fatalf("expected 2 matches for 'save', got %d", len(m))
	}
	// Empty needle matches nothing (windowTitleMatches guard).
	if m := atspiFind(els, ""); len(m) != 0 {
		t.Fatalf("empty needle must match nothing, got %d", len(m))
	}
	// Center math: 10+60/2, 20+30/2.
	cx, cy := atspiCenter(m[0])
	if cx != 40 || cy != 35 {
		t.Errorf("center = (%d,%d), want (40,35)", cx, cy)
	}
	// Format contains role, quoted name, and coords.
	s := atspiFormat(m)
	if !strings.Contains(s, `push button "Save" at (40,35)`) {
		t.Errorf("format missing expected line: %s", s)
	}
}

func TestYdoMoveArgs(t *testing.T) {
	got := ydoMoveArgs(100, 200)
	want := []string{"ydotool", "move", "-a", "100", "200"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("ydoMoveArgs = %v, want %v", got, want)
	}
}

func TestYdoClickArgs(t *testing.T) {
	// Left, single click.
	got := ydoClickArgs("left", 1)
	if len(got) != 1 || strings.Join(got[0], " ") != "ydotool click 0xC0" {
		t.Fatalf("left single = %v", got)
	}
	// Right button uses BTN_RIGHT.
	got = ydoClickArgs("right", 1)
	if len(got) != 1 || strings.Join(got[0], " ") != "ydotool click 0xC1" {
		t.Fatalf("right single = %v", got)
	}
	// Middle button uses BTN_MIDDLE.
	if got := ydoClickArgs("middle", 1); strings.Join(got[0], " ") != "ydotool click 0xC2" {
		t.Fatalf("middle single = %v", got)
	}
	// Triple click repeats three times.
	got = ydoClickArgs("left", 3)
	if len(got) != 3 {
		t.Fatalf("triple should produce 3 commands, got %d", len(got))
	}
	// Degenerate clicks<=0 clamps to 1.
	if got := ydoClickArgs("left", 0); len(got) != 1 {
		t.Fatalf("zero clicks should clamp to 1, got %d", len(got))
	}
}

func TestYdoModifierKeyArgs(t *testing.T) {
	cases := map[string]string{
		"ctrl":  "ydotool key 29:1",
		"alt":   "ydotool key 56:1",
		"shift": "ydotool key 42:1",
		"cmd":   "ydotool key 125:1",
	}
	for mod, wantPress := range cases {
		press, release, ok := ydoModifierKeyArgs(mod)
		if !ok {
			t.Fatalf("modifier %q: expected mapping", mod)
		}
		if strings.Join(press, " ") != wantPress {
			t.Errorf("mod %q press = %v, want %q", mod, press, wantPress)
		}
		// Release must be the same key with :0.
		wantRel := strings.Replace(wantPress, ":1", ":0", 1)
		if strings.Join(release, " ") != wantRel {
			t.Errorf("mod %q release = %v, want %q", mod, release, wantRel)
		}
	}
	// fn has no evdev modifier mapping — must report not-ok, not mis-click.
	if _, _, ok := ydoModifierKeyArgs("fn"); ok {
		t.Fatal("fn should have no ydotool mapping")
	}
}

func TestYdoDragArgsSequence(t *testing.T) {
	cmds := ydoDragArgs(10, 20, 30, 40)
	if len(cmds) != 4 {
		t.Fatalf("drag should be 4 commands (move, down, move, up), got %d", len(cmds))
	}
	// Starts at the source, ends at the destination.
	if !strings.HasSuffix(strings.Join(cmds[0], " "), "10 20") {
		t.Errorf("first move should target source (10,20): %v", cmds[0])
	}
	if !strings.HasSuffix(strings.Join(cmds[2], " "), "30 40") {
		t.Errorf("second move should target destination (30,40): %v", cmds[2])
	}
	// Down before up, via ydotool click bitmask (0x40 = down-only,
	// 0x80 = up-only — the -d/-u flags are silently ignored upstream, #191).
	if !(len(cmds[1]) > 2 && cmds[1][2] == "0x40") {
		t.Errorf("second command should be button-down (click 0x40): %v", cmds[1])
	}
	if !(len(cmds[3]) > 2 && cmds[3][2] == "0x80") {
		t.Errorf("fourth command should be button-up (click 0x80): %v", cmds[3])
	}
}

// TestXdModifierName covers the X11 modifier mapping used by the
// keydown/click/keyup chain in modifier_click (#216: xdotool click has
// no modifier syntax, so "ctrl+1" parsed as button 0 and every combo
// failed).
func TestXdModifierName(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"cmd", "super", true},
		{"ctrl", "ctrl", true},
		{"alt", "alt", true},
		{"shift", "shift", true},
		{"super", "super", true},
		{"fn", "", false}, // no X mapping — must error, not silently mis-click
		{"meta", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := xdModifierName(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("xdModifierName(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}
