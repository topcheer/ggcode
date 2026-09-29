package agent

import (
	"testing"
)

// zz_issue2810_test.go guards against the go-test scope-gaming blind spot
// (#2810): .go file arguments were collected as pkg: tokens and isNarrower
// used exact set comparison, so package → single-file narrowing (fail→pass)
// never fired.

// TestIssue2810GoFileClassifiedAsFile: a .go argument must be a file: token.
func TestIssue2810GoFileClassifiedAsFile(t *testing.T) {
	got := extractGoTestScope("go test ./internal/agent/bar_test.go")
	if want := "file:./internal/agent/bar_test.go"; got != want {
		t.Errorf("extractGoTestScope(.go arg) = %q, want %q (#2810 recurrence: still pkg:)", got, want)
	}
	// Package form unchanged.
	got2 := extractGoTestScope("go test ./internal/agent/")
	if want2 := "pkg:./internal/agent/"; got2 != want2 {
		t.Errorf("extractGoTestScope(pkg) = %q, want %q", got2, want2)
	}
}

// TestIssue2810PkgToFileNarrowing: package dir → file under it is narrower
// (both slash spellings).
func TestIssue2810PkgToFileNarrowing(t *testing.T) {
	cases := []struct{ b, a string }{
		{"file:./internal/agent/bar_test.go", "pkg:./internal/agent/"},
		{"file:./internal/agent/bar_test.go", "pkg:./internal/agent"}, // trailing slash
		{"pkg:./internal/agent/sub", "pkg:./internal/agent"},          // dir under dir
		{"pkg:./internal/agent/bar_test.go", "pkg:./internal/agent/"}, // pre-fix shape
	}
	for _, tc := range cases {
		if !isNarrower(tc.b, tc.a) {
			t.Errorf("isNarrower(%q, %q) = false, want true (#2810 blind spot)", tc.b, tc.a)
		}
	}
}

// TestIssue2810NoFalsePositiveUnrelatedOrWidening: unrelated paths and
// widening directions must NOT be flagged.
func TestIssue2810NoFalsePositiveUnrelatedOrWidening(t *testing.T) {
	neg := []struct{ b, a string }{
		{"pkg:./internal/tool/", "pkg:./internal/agent/"},           // unrelated dirs
		{"file:./internal/tool/x_test.go", "pkg:./internal/agent/"}, // unrelated file
		{"pkg:./internal/agentx/t.go", "pkg:./internal/agent"},      // segment prefix only
		{"pkg:./internal/", "pkg:./internal/agent/"},                // widening (parent)
		{"file:./cmd/a.go", ""},                                     // empty a
	}
	for _, tc := range neg {
		if isNarrower(tc.b, tc.a) {
			t.Errorf("isNarrower(%q, %q) = true, want false", tc.b, tc.a)
		}
	}
}

// TestIssue2810GamingChainFires: the canonical three-round gaming sequence
// from the issue must now trigger the detector.
func TestIssue2810GamingChainFires(t *testing.T) {
	s1 := extractGoTestScope("go test ./internal/agent/")            // pkg
	s2 := extractGoTestScope("go test ./internal/agent/bar_test.go") // file
	s3 := extractGoTestScope("go test ./internal/agent/bar_test.go -run TestBar")
	if !isNarrower(s2, s1) {
		t.Fatalf("round2 vs round1: isNarrower(%q, %q) = false - gaming chain still blind", s2, s1)
	}
	if !isNarrower(s3, s2) {
		t.Fatalf("round3 vs round2: isNarrower(%q, %q) = false", s3, s2)
	}
}

// TestIssue2810PathSegmentPrefix: segment semantics of the containment helper.
func TestIssue2810PathSegmentPrefix(t *testing.T) {
	pos := [][2]string{
		{"./internal/agent/bar_test.go", "./internal/agent/"},
		{"./internal/agent/bar_test.go", "./internal/agent"},
		{"./internal/agent/sub", "./internal/agent"},
		{"./internal/agent", "./internal/agent"},
	}
	for _, p := range pos {
		if !pathSegmentPrefix(p[0], p[1]) {
			t.Errorf("pathSegmentPrefix(%q, %q) = false, want true", p[0], p[1])
		}
	}
	if pathSegmentPrefix("./internal/agentx/t.go", "./internal/agent") {
		t.Error("pathSegmentPrefix must not match partial segments")
	}
}
