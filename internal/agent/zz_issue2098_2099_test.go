package agent

// #2098/#2099 regression:
//   - #2098: extractJobID computed the "job id:" offset on the ToLower
//     COPY but sliced the ORIGINAL - length-changing runes (İ 2B->1B,
//     ẞ 3B->2B) in a prepended Rules block shifted the cut and the
//     "extracted" id was ":" (orphan detection blind on the real job).
//   - #2099: causalErrorFileRe matched only .go while causalVerifyRe
//     deliberately covers npm/cargo/pytest/jest/gradlew - non-Go
//     failures never produced errorFiles and the detector stayed
//     silent outside Go projects.

import (
	"testing"
)

func TestExtractJobIDUnicodeShift(t *testing.T) {
	s := "[Rules — learned from past mistakes]\n⚠ Never start jobs in /Users/İbrahim/src and ẞ chars...\n\n" +
		"Job ID: job-a1b2c3\nStatus: running\n"
	if got := extractJobID(s); got != "job-a1b2c3" {
		t.Fatalf("extractJobID = %q, want job-a1b2c3 (offset shifted by length-changing runes)", got)
	}
	// Case variants still match (the old path's intent).
	if got := extractJobID("job id: job-x9\n"); got != "job-x9" {
		t.Fatalf("lowercase variant: %q", got)
	}
	if got := extractJobID("Started. JOB ID: quoted-1\n"); got != "quoted-1" {
		t.Fatalf("uppercase variant: %q", got)
	}
}

func TestCausalErrorFilesNonGoToolchains(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want string
	}{
		{"jest ts", "FAIL src/api.test.ts\nsrc/api.ts:12:5 - error TS2322", "src/api.ts"},
		{"pytest py", "FAILED src/test_api.py::test_add\nsrc/test_api.py:12: AssertionError", "src/test_api.py"},
		{"cargo rs", "error[E0308]: mismatched types\n --> src/lib.rs:12:15", "src/lib.rs"},
		{"go control", "src/api.go:12:2: undefined: x", "src/api.go"},
		{"jsx", "src/App.jsx:4:10: Cannot find module", "src/App.jsx"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := extractErrorFiles(c.out)
			found := false
			for _, f := range files {
				if f == c.want {
					found = true
				}
			}
			if !found {
				t.Fatalf("extractErrorFiles(%q) = %v, want %q present", c.out, files, c.want)
			}
		})
	}
}
