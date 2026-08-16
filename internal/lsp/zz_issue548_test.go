package lsp

// Issue #548 Bug A (high): Windows fileURI↔uriToPath roundtrip must keep the
// drive letter. All cases are pure string construction — no GOOS dependency.

import "testing"

func TestIssue548FileURIRoundTripWindowsDrivePreserved(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{"lower drive", `C:/foo/bar.go`},
		{"upper drive", `D:\\Projects\\app\\main.go`},
		{"root only", `Q:/`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uri := fileURI(tc.path)
			got := uriToPath(uri)
			// Normalize case: only the drive letter case is permitted to differ.
			if !equalDrivePath(got, tc.path) {
				t.Errorf("roundtrip %q → %q → %q: drive letter lost or path mangled", tc.path, uri, got)
			}
		})
	}
}

func TestIssue548FileURIWindowsProducesThreeSlashes(t *testing.T) {
	uri := fileURI(`C:/foo/bar.go`)
	// The canonical Windows encoding is file:///C:/... — the drive letter must
	// sit in the path component with an empty host, never as the URI host.
	if uri != "file:///C:/foo/bar.go" {
		t.Errorf("fileURI(C:/foo/bar.go) = %q, want file:///C:/foo/bar.go", uri)
	}
}

func TestIssue548URIToPathAcceptsCanonicalThreeSlashWindowsURI(t *testing.T) {
	got := uriToPath("file:///C:/foo/bar.go")
	if got != "C:/foo/bar.go" {
		t.Errorf("uriToPath(file:///C:/foo/bar.go) = %q, want C:/foo/bar.go", got)
	}
}

func TestIssue548URIToPathAcceptsLegacyTwoSlashWindowsURI(t *testing.T) {
	// Legacy form previously produced by this codebase (drive letter parsed as
	// host). uriToPath must still recover the drive letter for back-compat.
	got := uriToPath("file://C:/foo/bar.go")
	if got != "C:/foo/bar.go" {
		t.Errorf("uriToPath(file://C:/foo/bar.go) = %q, want C:/foo/bar.go", got)
	}
}

func TestIssue548URIToPathPosixUnchanged(t *testing.T) {
	got := uriToPath("file:///tmp/x/y.go")
	want := "/tmp/x/y.go"
	if got != want {
		t.Errorf("uriToPath(file:///tmp/x/y.go) = %q, want %q", got, want)
	}
}

func TestIssue548URIToPathNonFileSchemeReturnedRaw(t *testing.T) {
	got := uriToPath("https://example.com/x")
	if got != "https://example.com/x" {
		t.Errorf("uriToPath(non-file) = %q, want raw passthrough", got)
	}
}

// equalDrivePath compares two paths, ignoring case differences of a leading
// drive letter only.
func equalDrivePath(a, b string) bool {
	if len(a) > 1 && len(b) > 1 && a[1] == ':' && b[1] == ':' {
		if (a[0] | 0x20) != (b[0] | 0x20) {
			return false
		}
		return a[2:] == b[2:]
	}
	return a == b
}
