package tool

import (
	"testing"
)

// ============================================================================
// InteractiveCommandWarning tests -- detect commands that hang on stdin/forever
// ============================================================================

func TestInteractiveCommand_BareREPL(t *testing.T) {
	gate := NewCommandGate()

	// Bare REPL invocations with no arguments hang on stdin.
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

	// REPL invocations with script files or inline code flags are safe.
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

	// less -F (quit-if-one-screen) and less -E (quit-at-eof) are non-interactive.
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

	// top -b (batch mode) and top -n 1 (one iteration) are non-interactive.
	safe := []string{
		"top -b -n 1",
		"top -n 1",
		"htop -C", // htop -C is not batch mode but doesn't hang in CI
	}

	for _, cmd := range safe {
		w := gate.InteractiveCommandWarning(cmd)
		// top -b -n 1 and top -n 1 should be safe
		if cmd == "top -b -n 1" || cmd == "top -n 1" {
			if w != "" {
				t.Errorf("expected no interactive warning for %q, got: %s", cmd, w)
			}
		}
	}
}

func TestInteractiveCommand_TailFollow(t *testing.T) {
	gate := NewCommandGate()

	// tail -f runs forever.
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

	// tail without -f exits normally.
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

	// watch and yes run forever by design.
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

	// cat with no args reads from stdin forever.
	w := gate.InteractiveCommandWarning("cat")
	if w == "" {
		t.Errorf("expected interactive warning for bare 'cat', got empty")
	}

	// cat with a file argument is fine.
	w = gate.InteractiveCommandWarning("cat file.txt")
	if w != "" {
		t.Errorf("expected no interactive warning for 'cat file.txt', got: %s", w)
	}
}

func TestInteractiveCommand_NormalCommandsSafe(t *testing.T) {
	gate := NewCommandGate()

	// Normal build/test/git commands should not trigger interactive warnings.
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

	// Compound commands where one part hangs should warn.
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

	// Compound commands where all parts are safe should not warn.
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

	// bash -c 'commands' runs one-shot and exits.
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

	// Verify that interactive warnings appear in Check().Warnings.
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

	// Safe command should not have interactive warnings.
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

	// Verify warning includes actionable suggestion.
	w := gate.InteractiveCommandWarning("vim")
	if !containsAnyStr(w, []string{"edit_file", "write_file", "read_file"}) {
		t.Errorf("expected vim warning to suggest edit_file/write_file/read_file, got: %s", w)
	}

	w = gate.InteractiveCommandWarning("tail -f log.txt")
	if !containsAnyStr(w, []string{"start_command", "-f"}) {
		t.Errorf("expected tail -f warning to suggest alternative, got: %s", w)
	}
}

// containsAnyStr checks if s contains any of the substrings.
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
