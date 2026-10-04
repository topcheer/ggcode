package tui

// #3278 final-review rejection probe: the first version checked
// parts[1] == "global" inside case "clear" - unreachable, because parts
// is the FULL command split (parts[0] = "/traj", parts[1] = "clear").
// The parsing now lives in trajClearArgIsGlobal with its own probe so a
// dead-branch wiring regression cannot land silently again.

import "testing"

func TestTrajClearArgIsGlobal(t *testing.T) {
	cases := []struct {
		name  string
		parts []string
		want  bool
	}{
		{"full command with global", []string{"/traj", "clear", "global"}, true},
		{"uppercase arg", []string{"/traj", "clear", "GLOBAL"}, true},
		{"padded arg", []string{"/traj", "clear", " global "}, true},
		{"bare clear", []string{"/traj", "clear"}, false},
		{"bare clear with other arg", []string{"/traj", "clear", "workspace"}, false},
		{"the original dead branch shape (parts[1]=clear never global)", []string{"/traj", "clear"}, false},
	}
	for _, c := range cases {
		if got := trajClearArgIsGlobal(c.parts); got != c.want {
			t.Fatalf("%s: trajClearArgIsGlobal(%q) = %v, want %v", c.name, c.parts, got, c.want)
		}
	}
}
