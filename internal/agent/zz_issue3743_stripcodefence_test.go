package agent

// zz_issue3743_stripcodefence_test.go -- companion tests for the
// prose-then-fence shape of the verify oracle (#3743). The old
// prefix-strip + LastIndex-truncate pipeline truncated at the CLOSING
// fence and left the leading prose, which then failed LookPath and got
// skip-amnestied to Passed=true while bypassing the deterministic
// fallback (detectBuildSystem never ran because a non-empty string was
// returned).
import "testing"

func TestIssue3743StripCodeFenceProseThenFence(t *testing.T) {
	cases := map[string]string{
		// The reported shape: prose first, fenced command after.
		"建议运行：\n```bash\ngo test ./internal/agent/\n```": "go test ./internal/agent/",
		"建议运行：\n```bash\ngo build ./...\n```":            "go build ./...",
		// English prose variant.
		"Run this:\n```sh\ngo vet ./...\n```": "go vet ./...",
		// Unterminated fence after prose: command still recovered.
		"建议运行：\n```bash\ngo test ./internal/tool/": "go test ./internal/tool/",
		// Fenced-first (the shape #1522 already pinned) must keep working.
		"```go\ngo test ./...\n```": "go test ./...",
		// Bare command, no fence: passthrough.
		"go vet ./...": "go vet ./...",
		// Multi-line command in the block: first line only (oracle contract).
		"```\ncd /x && make test\nmake lint\n```": "cd /x && make test",
	}
	for in, want := range cases {
		if got := stripCodeFence(in); got != want {
			t.Errorf("stripCodeFence(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIssue3743StripCodeFenceEmptyBlockYieldsEmpty(t *testing.T) {
	// Fence with nothing usable inside must return "" so the deterministic
	// fallback (detectBuildSystem) takes over instead of prose leaking
	// through as a bogus command.
	for _, in := range []string{"```", "建议运行：\n```", "```bash```"} {
		if got := stripCodeFence(in); got != "" {
			t.Errorf("stripCodeFence(%q) = %q, want \"\" (fallback)", in, got)
		}
	}
}

func TestIssue3743ProseDoesNotReachCommandPipeline(t *testing.T) {
	// The exact #3743 repro: before the fix the result was the bare prose
	// "建议运行：" - verifyCommandAvailable would fail LookPath on it.
	got := stripCodeFence("建议运行：\n```bash\ngo test ./internal/agent/\n```")
	if got != "go test ./internal/agent/" {
		t.Fatalf("repro: got %q, want the fenced command", got)
	}
	if !verifyCommandAvailable(got) {
		t.Fatalf("recovered command %q must be a runnable command shape", got)
	}
}
