package agent

import "testing"

// TestFirstRmCommandSegment_ValuelessWrapperFlags pins the #5456 residue
// forms: valueless wrapper flags and inline-value flags must not swallow the
// following "rm", and env assignments after a wrapper word must strip like
// the segment-head stripper. All three were previously missed by the
// prevWasFlag consume loop.
func TestFirstRmCommandSegment_ValuelessWrapperFlags(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// #5456 residue forms (previously returned "")
		{"xargs valueless -0", "xargs -0 rm -rf /tmp/x", "rm -rf /tmp/x"},
		{"sudo inline-value flag", "sudo --preserve-env=PATH rm -rf /tmp/x", "rm -rf /tmp/x"},
		{"env assign after wrapper", "env VAR=1 nice -n 5 rm -rf /tmp/x", "rm -rf /tmp/x"},
		{"env valueless -i", "env -i rm -rf /tmp/x", "rm -rf /tmp/x"},

		// Control group (already correct before #5456): must stay pinned.
		{"sudo valued flag", "sudo -u root rm -rf /tmp/x", "rm -rf /tmp/x"},
		{"timeout numeric", "timeout 30 sudo rm -rf /tmp/x", "rm -rf /tmp/x"},
		{"path-normalized wrapper", "/usr/bin/sudo /bin/rm -rf /tmp/x", "/bin/rm -rf /tmp/x"},
		{"bare form", "rm -rf /tmp/x", "rm -rf /tmp/x"},
		// #5315 negative pins: non-wrapper commands keep their tokens.
		{"non-wrapper keeps flag value", "echo -n hello && rm -rf /tmp/x", "rm -rf /tmp/x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := firstRmCommandSegment(tc.in)
			if got != tc.want {
				t.Fatalf("input %q:\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}
