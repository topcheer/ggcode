//go:build !windows

package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCommandPersistsCwdAcrossCalls(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	rc := RunCommand{WorkingDir: root, cwdState: newShellCwdState()}

	// First command: cd into sub. The sentinel must be stripped from the
	// output the model sees.
	res, err := rc.Execute(context.Background(), json.RawMessage(`{"command":"cd sub && pwd"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("first command failed: %s", res.Content)
	}
	if strings.Contains(res.Content, cwdMarker) {
		t.Fatalf("sentinel leaked into output: %q", res.Content)
	}
	if !strings.Contains(res.Content, "sub") {
		t.Fatalf("expected %q in first output: %q", sub, res.Content)
	}

	// Second command: no cd — must still run in sub (persistence).
	res2, err := rc.Execute(context.Background(), json.RawMessage(`{"command":"pwd"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res2.IsError {
		t.Fatalf("second command failed: %s", res2.Content)
	}
	want, err := filepath.EvalSymlinks(sub)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res2.Content, want) {
		t.Fatalf("cwd not persisted: got %q, want %q", res2.Content, want)
	}
}

func TestRunCommandCwdPersistenceDisabledWithoutState(t *testing.T) {
	root := t.TempDir()
	rc := RunCommand{WorkingDir: root} // nil cwdState: legacy behavior
	res, err := rc.Execute(context.Background(), json.RawMessage(`{"command":"pwd"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("command failed: %s", res.Content)
	}
	if strings.Contains(res.Content, cwdMarker) {
		t.Fatalf("sentinel appended despite nil state: %q", res.Content)
	}
}

func TestRunCommandFailedCommandKeepsPreviousCwd(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	rc := RunCommand{WorkingDir: root, cwdState: newShellCwdState()}

	if _, err := rc.Execute(context.Background(), json.RawMessage(`{"command":"cd sub && pwd"}`)); err != nil {
		t.Fatal(err)
	}
	// A command that exits the shell before the sentinel runs (here: explicit
	// exit) must not update the persisted directory.
	res, err := rc.Execute(context.Background(), json.RawMessage(`{"command":"exit 3"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("expected error result for exit 3, got: %s", res.Content)
	}
	// Still in sub: the failed command did not reset the session cwd.
	res2, err := rc.Execute(context.Background(), json.RawMessage(`{"command":"pwd"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res2.IsError {
		t.Fatalf("third command failed: %s", res2.Content)
	}
	want, err := filepath.EvalSymlinks(sub)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res2.Content, want) {
		t.Fatalf("failed command must not reset persisted cwd: got %q, want %q", res2.Content, want)
	}
}
