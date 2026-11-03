package plugin

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
)

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

	if shared[0] != "echo" {
		t.Fatalf("shared prefix mutated: %q", shared[0])
	}

	full := shared[:cap(shared)]
	for i := 1; i < len(full); i++ {
		if full[i] != "" {
			t.Fatalf("shared backing cell %d written: %q (Execute appended into the shared array)", i, full[i])
		}
	}

	if len(a.args) != 1 || a.args[0] != "echo" {
		t.Errorf("tool args mutated: %v", a.args)
	}
}
