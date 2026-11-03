package agent

import "testing"

func TestIssue3743StripCodeFenceProseThenFence(t *testing.T) {
	cases := map[string]string{

		"建议运行：\n```bash\ngo test ./internal/agent/\n```": "go test ./internal/agent/",
		"建议运行：\n```bash\ngo build ./...\n```":            "go build ./...",

		"Run this:\n```sh\ngo vet ./...\n```": "go vet ./...",

		"建议运行：\n```bash\ngo test ./internal/tool/": "go test ./internal/tool/",

		"```go\ngo test ./...\n```": "go test ./...",

		"go vet ./...": "go vet ./...",

		"```\ncd /x && make test\nmake lint\n```": "cd /x && make test",
	}
	for in, want := range cases {
		if got := stripCodeFence(in); got != want {
			t.Errorf("stripCodeFence(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIssue3743StripCodeFenceEmptyBlockYieldsEmpty(t *testing.T) {

	for _, in := range []string{"```", "建议运行：\n```", "```bash```"} {
		if got := stripCodeFence(in); got != "" {
			t.Errorf("stripCodeFence(%q) = %q, want \"\" (fallback)", in, got)
		}
	}
}

func TestIssue3743ProseDoesNotReachCommandPipeline(t *testing.T) {

	got := stripCodeFence("建议运行：\n```bash\ngo test ./internal/agent/\n```")
	if got != "go test ./internal/agent/" {
		t.Fatalf("repro: got %q, want the fenced command", got)
	}
	if !verifyCommandAvailable(got) {
		t.Fatalf("recovered command %q must be a runnable command shape", got)
	}
}

func TestIssue3743OddFenceTrailingProseNoLeak(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{
			"trailing prose mentions fence (zh)",
			"建议运行：\n```bash\ngo test ./internal/agent/ -count=1\n```\n注意：上文的 ``` 内代码先修复再跑",
			"go test ./internal/agent/ -count=1",
		},
		{
			"trailing prose mentions fence (en)",
			"Run this:\n```bash\ngo build ./...\n```\nsee the ``` block above for details",
			"go build ./...",
		},
		{
			"odd count via unclosed aside after complete block",
			"```bash\ngo vet ./...\n```\nnote ```go\n// aside",
			"go vet ./...",
		},
	}
	for _, c := range cases {
		if got := stripCodeFence(c.in); got != c.want {
			t.Errorf("%s: stripCodeFence = %q, want %q", c.name, got, c.want)
		}
	}
}
