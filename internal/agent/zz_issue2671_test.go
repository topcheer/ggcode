package agent

import "testing"

func TestIssue2671_SegmentBoundaryAtPathHead(t *testing.T) {

	for _, c := range [][2]string{
		{"authentication/config.go", "auth"},
		{"authorization/handler.go", "auth"},
		{"authorize/middleware.go", "auth"},
	} {
		if cvPathMatchesPattern(c[0], c[1]) {
			t.Errorf("cvPathMatchesPattern(%q, %q) = true, want false (segment boundary violated #2671)", c[0], c[1])
		}
	}

	for _, c := range [][2]string{
		{"auth/handler.go", "auth"},
		{"auth", "auth"},
		{"internal/auth/handler.go", "auth"},
		{"internal/auth/handler.go", "internal/auth"},
		{"internal/auth", "internal/auth/handler.go"},
		{"foo_test.go", "test"},
		{"test_bar.go", "test"},
	} {
		if !cvPathMatchesPattern(c[0], c[1]) {
			t.Errorf("cvPathMatchesPattern(%q, %q) = false, want true (legitimate match lost #2671)", c[0], c[1])
		}
	}
}
