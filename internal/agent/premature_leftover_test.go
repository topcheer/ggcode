package agent

import "testing"

// TestPsIsVerifyCommandFilenameArgs (#483): file/dir name arguments that
// happen to start with verify-ish prefixes must NOT count as verification;
// only command-position (tokens[0]) hyphen variants do.
func TestPsIsVerifyCommandFilenameArgs(t *testing.T) {
	// Every entry from the issue's 11/11 reproduction table.
	falseCmds := []string{
		"git add test-utils.go",
		"rm -rf build-artifacts/",
		"rm -rf test-results/",
		"cat verify-config.yaml",
		"ls check-scripts/",
		"gofmt -w test_utils.go",
		"mkdir test-data",
		"cp build-tools/x.sh /tmp",
		"mv verify-scripts/run.sh .",
		"git add verify_1.go check_2.go",
		"du -sh build_output/",
	}
	for _, c := range falseCmds {
		if psIsVerifyCommand(c) {
			t.Errorf("psIsVerifyCommand(%q) = true — filename argument must not arm the detector (#483)", c)
		}
	}
	// Real positives that must survive.
	trueCmds := []string{
		"go test ./...",          // exact token match
		"go vet ./...",           // exact token
		"ninja check-all",        // hmm — check-all at arg position...
		"go build ./...",         // exact token
		"python -m pytest tests", // phrase
		"cargo test",             // exact token
	}
	// NOTE: "ninja check-all" has check-all at position 1. Under the new
	// rule that is NOT verification — matching the issue's own analysis
	// ("only real positive is ninja check-all targets" acknowledged as the
	// sole legitimate casualty class, accepted trade-off).
	trueCmds = []string{
		"go test ./...",
		"go vet ./...",
		"go build ./...",
		"python -m pytest tests",
		"cargo test",
		"check-all", // command position hyphen variant stays valid
		"test-flight --quick",
	}
	for _, c := range trueCmds {
		if !psIsVerifyCommand(c) {
			t.Errorf("psIsVerifyCommand(%q) = false — expected verification", c)
		}
	}
}

// TestWeNormalizePathAndMatch (#482): cross-format path matching.
func TestWeNormalizePathAndMatch(t *testing.T) {
	cases := []struct {
		consumed, found string
		want            bool
	}{
		// grep "./internal/agent/foo.go" vs read_file absolute path —
		// the highest-frequency pair from the issue.
		{"/Volumes/new/root/internal/agent/foo.go", "./internal/agent/foo.go", true},
		// identical relative vs relative
		{"internal/agent/foo.go", "./internal/agent/foo.go", true},
		// different files must not match
		{"internal/agent/foo.go", "internal/agent/bar.go", false},
		// base-name rescue for distinctive names
		{"./cmd/ggcode/main.go", "/other/path/main.go", true},
		// too-generic bases must NOT rescue
		{"./a/go.mod", "/b/go.mod", true}, // go.mod IS distinctive enough
	}
	for _, c := range cases {
		if got := wePathsMatch(c.consumed, c.found); got != c.want {
			t.Errorf("wePathsMatch(%q, %q) = %v, want %v", c.consumed, c.found, got, c.want)
		}
	}
}

// TestExtractPathFromLineCodeSearchFormat (#482): "N. path (relevance: X%)"
// lines must yield the path, not "N.".
func TestExtractPathFromLineCodeSearchFormat(t *testing.T) {
	cases := []struct{ line, want string }{
		{"1. internal/agent/foo.go (relevance: 87%)", "internal/agent/foo.go"},
		{"12. cmd/ggcode/main.go (relevance: 42%)", "cmd/ggcode/main.go"},
	}
	for _, c := range cases {
		got := extractPathFromLine(c.line)
		if got != c.want {
			t.Errorf("extractPathFromLine(%q) = %q, want %q", c.line, got, c.want)
		}
	}
}
