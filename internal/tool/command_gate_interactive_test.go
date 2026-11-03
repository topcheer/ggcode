package tool

import (
	"testing"
)

func TestInteractiveCommand_BareREPL(t *testing.T) {
	gate := NewCommandGate()

	hanging := []string{
		"python",
		"python3",
		"node",
		"irb",
		"sqlite3",
		"mysql",
		"psql",
		"redis-cli",
		"bc",
		"lua",
		"r",
	}

	for _, cmd := range hanging {
		w := gate.InteractiveCommandWarning(cmd)
		if w == "" {
			t.Errorf("expected interactive warning for %q, got empty", cmd)
		}
	}
}

func TestInteractiveCommand_REPLWithArgsIsSafe(t *testing.T) {
	gate := NewCommandGate()

	safe := []string{
		"python script.py",
		"python3 main.py",
		"python -c 'print(1)'",
		"python -m pytest",
		"node script.js",
		"node app.js",
		"node -e 'console.log(1)'",
		"node --eval '1+1'",
		"deno run script.ts",
		"bun script.js",
		"sqlite3 db.sqlite '.tables'",
		"mysql -e 'SELECT 1'",
		"psql -c 'SELECT 1'",
		"redis-cli GET key",
		"bc <<< '1+1'",
		"lua script.lua",
		"Rscript analysis.R",
	}

	for _, cmd := range safe {
		w := gate.InteractiveCommandWarning(cmd)
		if w != "" {
			t.Errorf("expected no interactive warning for %q, got: %s", cmd, w)
		}
	}
}

func TestInteractiveCommand_Editors(t *testing.T) {
	gate := NewCommandGate()

	editors := []string{
		"vim",
		"vi",
		"nvim",
		"nano",
		"emacs",
		"vim file.txt",
		"nano config.yaml",
		"nvim src/main.go",
	}

	for _, cmd := range editors {
		w := gate.InteractiveCommandWarning(cmd)
		if w == "" {
			t.Errorf("expected interactive warning for editor %q, got empty", cmd)
		}
	}
}

func TestInteractiveCommand_Pagers(t *testing.T) {
	gate := NewCommandGate()

	pagers := []string{
		"less file.txt",
		"more file.txt",
		"cat file.txt | less",
	}

	for _, cmd := range pagers {
		w := gate.InteractiveCommandWarning(cmd)
		if w == "" {
			t.Errorf("expected interactive warning for pager %q, got empty", cmd)
		}
	}
}

func TestInteractiveCommand_PagerWithNonInteractiveFlag(t *testing.T) {
	gate := NewCommandGate()

	safe := []string{
		"less -F file.txt",
		"less -E file.txt",
		"less --quit-if-one-screen file.txt",
		"less --quit-at-eof file.txt",
	}

	for _, cmd := range safe {
		w := gate.InteractiveCommandWarning(cmd)
		if w != "" {
			t.Errorf("expected no interactive warning for %q, got: %s", cmd, w)
		}
	}
}

func TestInteractiveCommand_TopAndMonitors(t *testing.T) {
	gate := NewCommandGate()

	monitors := []string{
		"top",
		"htop",
		"btop",
	}

	for _, cmd := range monitors {
		w := gate.InteractiveCommandWarning(cmd)
		if w == "" {
			t.Errorf("expected interactive warning for monitor %q, got empty", cmd)
		}
	}
}

func TestInteractiveCommand_TopBatchMode(t *testing.T) {
	gate := NewCommandGate()

	safe := []string{
		"top -b -n 1",
		"top -n 1",
		"htop -C",
	}

	for _, cmd := range safe {
		w := gate.InteractiveCommandWarning(cmd)

		if cmd == "top -b -n 1" || cmd == "top -n 1" {
			if w != "" {
				t.Errorf("expected no interactive warning for %q, got: %s", cmd, w)
			}
		}
	}
}

func TestInteractiveCommand_TailFollow(t *testing.T) {
	gate := NewCommandGate()

	hanging := []string{
		"tail -f log.txt",
		"tail --follow log.txt",
		"tail -F log.txt",
		"tail -fq log.txt",
	}

	for _, cmd := range hanging {
		w := gate.InteractiveCommandWarning(cmd)
		if w == "" {
			t.Errorf("expected interactive warning for %q, got empty", cmd)
		}
	}

	safe := []string{
		"tail -n 100 log.txt",
		"tail log.txt",
		"tail -n 50 file.log",
	}

	for _, cmd := range safe {
		w := gate.InteractiveCommandWarning(cmd)
		if w != "" {
			t.Errorf("expected no interactive warning for %q, got: %s", cmd, w)
		}
	}
}

func TestInteractiveCommand_WatchAndYes(t *testing.T) {
	gate := NewCommandGate()

	hanging := []string{
		"watch ls",
		"watch -n 1 'date'",
		"yes",
		"yes 'hello'",
	}

	for _, cmd := range hanging {
		w := gate.InteractiveCommandWarning(cmd)
		if w == "" {
			t.Errorf("expected interactive warning for %q, got empty", cmd)
		}
	}
}

func TestInteractiveCommand_BareCatHangs(t *testing.T) {
	gate := NewCommandGate()

	w := gate.InteractiveCommandWarning("cat")
	if w == "" {
		t.Errorf("expected interactive warning for bare 'cat', got empty")
	}

	w = gate.InteractiveCommandWarning("cat file.txt")
	if w != "" {
		t.Errorf("expected no interactive warning for 'cat file.txt', got: %s", w)
	}
}

func TestInteractiveCommand_NormalCommandsSafe(t *testing.T) {
	gate := NewCommandGate()

	safe := []string{
		"go build ./...",
		"go test ./...",
		"npm test",
		"make build",
		"git status",
		"git diff",
		"ls -la",
		"echo hello",
		"curl http://example.com",
		"docker build -t app .",
		"grep -r 'pattern' .",
		"find . -name '*.go'",
		"sed -i 's/old/new/g' file.txt",
		"awk '{print $1}' file.txt",
		"sort file.txt",
		"uniq -c",
		"wc -l file.txt",
		"head -n 10 file.txt",
		"echo 'data' | jq '.field'",
		"python -m pytest tests/",
		"node -e 'console.log(42)'",
	}

	for _, cmd := range safe {
		w := gate.InteractiveCommandWarning(cmd)
		if w != "" {
			t.Errorf("expected no interactive warning for safe command %q, got: %s", cmd, w)
		}
	}
}

func TestInteractiveCommand_CompoundCommands(t *testing.T) {
	gate := NewCommandGate()

	hanging := []string{
		"echo hello; python",
		"ls && vim",
		"cat file.txt | less",
		"echo setup; tail -f log.txt",
	}

	for _, cmd := range hanging {
		w := gate.InteractiveCommandWarning(cmd)
		if w == "" {
			t.Errorf("expected interactive warning for compound command %q, got empty", cmd)
		}
	}

	safe := []string{
		"go build && go test",
		"echo hello; echo world",
		"cat file.txt | grep pattern",
		"npm install && npm test",
	}

	for _, cmd := range safe {
		w := gate.InteractiveCommandWarning(cmd)
		if w != "" {
			t.Errorf("expected no interactive warning for safe compound command %q, got: %s", cmd, w)
		}
	}
}

func TestInteractiveCommand_ShellWithDashC(t *testing.T) {
	gate := NewCommandGate()

	safe := []string{
		"bash -c 'echo hello'",
		"sh -c 'ls -la'",
		"zsh -c 'echo test'",
		"fish -c 'echo test'",
	}

	for _, cmd := range safe {
		w := gate.InteractiveCommandWarning(cmd)
		if w != "" {
			t.Errorf("expected no interactive warning for %q, got: %s", cmd, w)
		}
	}
}

func TestInteractiveCommand_EmptyCommand(t *testing.T) {
	gate := NewCommandGate()

	w := gate.InteractiveCommandWarning("")
	if w != "" {
		t.Errorf("expected empty warning for empty command, got: %s", w)
	}
}

func TestInteractiveCommand_WiredIntoCheck(t *testing.T) {
	gate := NewCommandGate()

	result := gate.Check("python")
	found := false
	for _, w := range result.Warnings {
		if containsAnyStr(w, []string{"Interactive", "interactive", "REPL", "stdin", "hang"}) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected interactive warning in Check().Warnings for 'python', got: %v", result.Warnings)
	}

	result = gate.Check("python -c 'print(1)'")
	found = false
	for _, w := range result.Warnings {
		if containsAnyStr(w, []string{"Interactive", "interactive", "REPL", "stdin"}) {
			found = true
			break
		}
	}
	if found {
		t.Errorf("expected no interactive warning for 'python -c print(1)', got: %v", result.Warnings)
	}
}

func TestInteractiveCommand_SuggestionContent(t *testing.T) {
	gate := NewCommandGate()

	w := gate.InteractiveCommandWarning("vim")
	if !containsAnyStr(w, []string{"edit_file", "write_file", "read_file"}) {
		t.Errorf("expected vim warning to suggest edit_file/write_file/read_file, got: %s", w)
	}

	w = gate.InteractiveCommandWarning("tail -f log.txt")
	if !containsAnyStr(w, []string{"start_command", "-f"}) {
		t.Errorf("expected tail -f warning to suggest alternative, got: %s", w)
	}
}

func containsAnyStr(s string, subs []string) bool {
	for _, sub := range subs {
		if len(s) >= len(sub) && (s == sub ||
			(len(s) > 0 && indexOfStr(s, sub) >= 0)) {
			return true
		}
	}
	return false
}

func indexOfStr(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
