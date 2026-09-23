package agent

import "testing"

// #2671: the bare prefix short-circuit (strings.HasPrefix(p, pat) at the top
// of cvPathMatchesPattern) matched same-stem sibling directories at the path
// head: "auth" hit "authentication/config.go" and "authorization/handler.go",
// violating the function's own documented segment-boundary contract (the
// docblock's counter-example "internal/authorization" was masked because its
// prefix is "internal/", not "auth"). Consequences: false avoid-violation
// warnings and false in-scope verdicts (suppressed outside-scope warnings).
func TestIssue2671_SegmentBoundaryAtPathHead(t *testing.T) {
	// Same-stem siblings at path head must NOT match (the bug).
	for _, c := range [][2]string{
		{"authentication/config.go", "auth"},
		{"authorization/handler.go", "auth"},
		{"authorize/middleware.go", "auth"},
	} {
		if cvPathMatchesPattern(c[0], c[1]) {
			t.Errorf("cvPathMatchesPattern(%q, %q) = true, want false (segment boundary violated #2671)", c[0], c[1])
		}
	}
	// Legitimate matches that MUST survive the fix.
	for _, c := range [][2]string{
		{"auth/handler.go", "auth"},                   // pattern == full head segment
		{"auth", "auth"},                              // pattern == whole path
		{"internal/auth/handler.go", "auth"},          // mid-path full segment
		{"internal/auth/handler.go", "internal/auth"}, // directory prefix
		{"internal/auth", "internal/auth/handler.go"}, // reverse: path shorter
		{"foo_test.go", "test"},                       // compound token boundary
		{"test_bar.go", "test"},
	} {
		if !cvPathMatchesPattern(c[0], c[1]) {
			t.Errorf("cvPathMatchesPattern(%q, %q) = false, want true (legitimate match lost #2671)", c[0], c[1])
		}
	}
}
