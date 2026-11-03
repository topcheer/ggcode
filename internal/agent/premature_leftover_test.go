package agent

import "testing"

func TestPsIsVerifyCommandFilenameArgs(t *testing.T) {

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

	trueCmds := []string{
		"go test ./...",
		"go vet ./...",
		"ninja check-all",
		"go build ./...",
		"python -m pytest tests",
		"cargo test",
	}

	trueCmds = []string{
		"go test ./...",
		"go vet ./...",
		"go build ./...",
		"python -m pytest tests",
		"cargo test",
		"check-all",
		"test-flight --quick",
	}
	for _, c := range trueCmds {
		if !psIsVerifyCommand(c) {
			t.Errorf("psIsVerifyCommand(%q) = false — expected verification", c)
		}
	}
}

func TestWeNormalizePathAndMatch(t *testing.T) {
	cases := []struct {
		consumed, found string
		want            bool
	}{

		{"/Volumes/new/root/internal/agent/foo.go", "./internal/agent/foo.go", true},

		{"internal/agent/foo.go", "./internal/agent/foo.go", true},

		{"internal/agent/foo.go", "internal/agent/bar.go", false},

		{"./cmd/ggcode/main.go", "/other/path/main.go", true},

		{"./a/go.mod", "/b/go.mod", true},
	}
	for _, c := range cases {
		if got := wePathsMatch(c.consumed, c.found); got != c.want {
			t.Errorf("wePathsMatch(%q, %q) = %v, want %v", c.consumed, c.found, got, c.want)
		}
	}
}

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
