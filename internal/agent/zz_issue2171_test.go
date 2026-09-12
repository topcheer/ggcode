package agent

// #2171 regression: the error-file class lacked '\' and ':' so Windows-
// native output matched NOTHING (C:\src\api.rs:12:5, src\main.rs:3:9,
// go's own .\pkg\file.go on Windows), and the tsc paren form
// path(12,3) was unmatched - a platform-wide dead zone predating the
// #2099 suffix widening.

import "testing"

func TestCausalErrorFilesWindowsPaths(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want string
	}{
		{"drive letter", "error[E0308]: mismatched\n  --> C:\\src\\api.rs:12:5", "C:/src/api.rs"},
		{"relative backslash", "error: cannot borrow\n --> src\\main.rs:3:9", "src/main.rs"},
		{"go dot-backslash", ".\\internal\\foo.go:10:2: undefined: x", "internal/foo.go"},
		{"tsc paren form", "C:\\src\\api.ts(12,3): error TS2322", "C:/src/api.ts"},
		{"posix control", "src/api.go:12:2: undefined: x", "src/api.go"},
		{"posix jsx", "src/App.jsx:4:10: Cannot find module", "src/App.jsx"},
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
				t.Fatalf("extractErrorFiles(%q) = %v, want %q (normalized) present", c.out, files, c.want)
			}
		})
	}
}
