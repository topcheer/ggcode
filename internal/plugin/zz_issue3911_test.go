package plugin

// #3911: CommandTool.Execute must not append onto the yaml-decoded shared
// backing array (cap > len). Two assertions: (a) Execute never mutates the
// tool's own args slice header/backing array beyond appending into a copy,
// (b) concurrent Executes observe no cross-talk.

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
)

// TestIssue3911_ExecuteDoesNotWriteSharedBackingArray proves the fix: with
// cap(args) > len(args) (as yaml.v3 append-growth leaves it), Execute must
// not write into the shared backing cells. Before the fix, the appended
// user args landed in the shared array; two tools sharing one backing array
// then cross-contaminated.
func TestIssue3911_ExecuteDoesNotWriteSharedBackingArray(t *testing.T) {
	shared := make([]string, 1, 8)
	shared[0] = "echo"

	a := &CommandTool{args: shared[0:1], execute: "/bin/echo", name: "a", description: "d"}
	b := &CommandTool{args: shared[0:1], execute: "/bin/echo", name: "b", description: "d"}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = a.Execute(context.Background(), json.RawMessage(`{"args":"x y z"}`)) }()
		go func() { defer wg.Done(); _, _ = b.Execute(context.Background(), json.RawMessage(`{"args":"p q r"}`)) }()
	}
	wg.Wait()

	// The shared prefix must be untouched.
	if shared[0] != "echo" {
		t.Fatalf("shared prefix mutated: %q", shared[0])
	}
	// Cells past len must not have been written by Execute (they hold zero
	// values before any append; pre-fix concurrent appends wrote into them).
	full := shared[:cap(shared)]
	for i := 1; i < len(full); i++ {
		if full[i] != "" {
			t.Fatalf("shared backing cell %d written: %q (Execute appended into the shared array)", i, full[i])
		}
	}
	// Tool's own slice is unchanged.
	if len(a.args) != 1 || a.args[0] != "echo" {
		t.Errorf("tool args mutated: %v", a.args)
	}
}
