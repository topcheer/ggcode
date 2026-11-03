package agent

import "testing"

func mkArgs3629(cmd string) string {
	return `{"command":` + quoteJSON3629(cmd) + `}`
}

func quoteJSON3629(s string) string {

	return `"` + s + `"`
}

func TestIssue3629_TightPipeTeeDetected(t *testing.T) {
	cases := []string{
		"go test ./... |tee test.log",
		"cmd |tee broken.go",
		"build;tee out.txt",
		"echo hi|tee greeting",
	}
	for _, cmd := range cases {
		if !commandMayRewriteWorkspace("run_command", mkArgs3629(cmd)) {
			t.Fatalf("tight-adjacency rewrite not detected: %q", cmd)
		}
	}
}

func TestIssue3629_ReadonlyCommandsUnchanged(t *testing.T) {
	cases := []string{
		"cat main.go | grep TODO",
		"ls -la; pwd",
		"go test ./... 2>&1",
	}
	for _, cmd := range cases {
		if commandMayRewriteWorkspace("run_command", mkArgs3629(cmd)) {
			t.Fatalf("read-only command misdetected as rewrite: %q", cmd)
		}
	}

	if commandMayRewriteWorkspace("read_file", mkArgs3629("whatever |tee x")) {
		t.Fatal("non-shell tool must not go through shell rewrite detection")
	}
}
