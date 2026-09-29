package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/tool"
)

func TestSnippetCandidate(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want bool
	}{
		{"verified build", "go test -run TestRelay ./ggcode-relay/", true},
		{"too short", "ls", false},
		{"two words ok", "gofmt -l", true},
		{"destructive", "git push --force origin main", false},
		{"rm rf", "rm -rf /tmp/x && echo done", false},
		{"secret token", "curl -H 'token=abc123' https://api.example.com/v1/data", false},
		{"secret key prefix", "export KEY=sk-live-abcdef && ./run.sh", false},
		{"reset hard", "git reset --hard HEAD~1", false},
		{"long pipeline", "go test ./... 2>&1 | grep -v '^ok' | head -20", true},
	}
	for _, c := range cases {
		if got := snippetCandidate(c.cmd); got != c.want {
			t.Errorf("%s: snippetCandidate(%q) = %v, want %v", c.name, c.cmd, got, c.want)
		}
	}
}

func TestSnippetName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"go test -run X ./pkg/", "auto-go-test"},
		{"git status --short", "auto-git-status"},
		{"make   verify-ci", "auto-make-verify-ci"},
	}
	for _, c := range cases {
		if got := snippetName(c.in); got != c.want {
			t.Errorf("snippetName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// End-to-end distiller check against a real snippet store in a temp dir:
// save/cap/blacklist/idempotency.
func TestDistillCommands(t *testing.T) {
	dir := t.TempDir()
	st := &tool.CmdSnippetTool{WorkingDir: dir}

	cmds := []string{
		"go test -run TestFoo ./internal/bar/",
		"git push --force origin main",           // destructive: skipped
		"curl -s https://x.io -H 'token=secret'", // secret: skipped
		"ls",                                     // fragment: skipped
		"make verify-ci GOFLAGS=-p=1",
		"go build -tags goolm ./...",
		"gofmt -w internal/baz/", // beyond the 3-save cap
	}
	if got := distillCommands(st, cmds); got != 3 {
		t.Fatalf("distillCommands saved = %d, want 3 (cap)", got)
	}

	// Verify store contents via the tool's list action.
	res, err := st.Execute(t.Context(), mustJSON(t, map[string]any{"action": "list"}))
	if err != nil || res.IsError {
		t.Fatalf("list failed: err=%v isErr=%v", err, res.IsError)
	}
	for _, forbidden := range []string{"push --force", "token=", "gofmt -w internal/baz/"} {
		if strings.Contains(res.Content, forbidden) {
			t.Errorf("store leaked %q:\n%s", forbidden, res.Content)
		}
	}
	if !strings.Contains(res.Content, "go test -run TestFoo") {
		t.Errorf("store missing verified command:\n%s", res.Content)
	}

	// Re-running the same commands must not duplicate (SaveAutoSnippet
	// idempotency on command text).
	if got := distillCommands(st, cmds); got != 3 {
		t.Fatalf("second distill saved = %d, want 3 (refresh)", got)
	}
	res2, err := st.Execute(t.Context(), mustJSON(t, map[string]any{"action": "list"}))
	if err != nil || res2.IsError {
		t.Fatalf("second list failed: %v", err)
	}
	if got := strings.Count(res2.Content, "go test -run TestFoo"); got != 1 {
		t.Errorf("duplicated entry after re-distill: %d occurrences", got)
	}
}

func TestSuccessfulCommandsLabelPrefersVerified(t *testing.T) {
	stats := agent.RunStats{
		CommandsRun:        []string{"go test ./...", "go vet ./..."},
		SuccessfulCommands: []string{"go test ./..."},
	}
	if got := successfulCommandsLabel(stats); got != "go test ./..." {
		t.Errorf("label = %q, want verified-only", got)
	}
	stats.SuccessfulCommands = nil
	if got := successfulCommandsLabel(stats); !strings.Contains(got, "go vet") {
		t.Errorf("fallback label = %q, want all commands", got)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
