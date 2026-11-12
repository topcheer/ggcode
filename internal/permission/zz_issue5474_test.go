package permission

import "testing"

// Issue #5474 pin: the permission-layer rm anchor must recognize wrapper
// chains the agent layer already handles (#5456/#5460): valueless flags
// (xargs -0), inline-valued flags (sudo --preserve-env=PATH), numeric flags
// (nice -n 5) and KEY=VALUE env assignments (env VAR=1 nice -n 5).
func TestIssue5474RmAnchorWrapperParity(t *testing.T) {
	d := NewDangerousDetector()

	cases := []struct {
		cmd  string
		want bool
	}{
		{"xargs -0 rm -rf /tmp/x", true},                 // valueless flag
		{"sudo --preserve-env=PATH rm -rf /tmp/x", true}, // inline value
		{"env VAR=1 nice -n 5 rm -rf /tmp/x", true},      // env assignment chain
		{"echo hi && rm -rf /tmp/x", true},               // regression: old anchor shape
		{"find . -name '*.tmp' | xargs rm -rf", true},    // regression: pipe shape
		{"sudo rm -rf /", true},                          // regression: direct wrapper
		{"grep -r pattern dir", false},                   // negative: non-rm
		{"rm mydoc.txt", false},                          // negative: plain rm (no r/f flags) not in critical list
		{"ls -la && cat notes/one.txt", false},           // negative: benign chain
	}
	for _, c := range cases {
		if got := d.IsDangerous(c.cmd); got != c.want {
			t.Errorf("IsDangerous(%q) = %v, want %v", c.cmd, got, c.want)
		}
	}
}
