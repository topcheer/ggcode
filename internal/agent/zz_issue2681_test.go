package agent

import "testing"

// #2681: depModInitThenGet discarded consumerArgs, so ANY run_command after a
// `go mod init/tidy/download` producer matched - including read-only commands
// (git status, ls, cat go.mod) that consume none of the module command's side
// effects. Every sibling command-class argMatch checks the consumer
// (depWriteThenBuild et al. -> commandIsBuildLike(consumerArgs)); this was the
// only one that didn't.
func TestIssue2681_ModInitThenGetChecksConsumer(t *testing.T) {
	// Read-only consumers after a module command must NOT match.
	for _, consumer := range []string{"git status", "ls -la", "cat go.mod", "echo done"} {
		if depModInitThenGet(map[string]interface{}{"command": "go mod tidy"}, map[string]interface{}{"command": consumer}) {
			t.Errorf("depModInitThenGet matched read-only consumer %q (#2681 false positive)", consumer)
		}
	}
	// Module-consuming consumers must still match.
	for _, consumer := range []string{
		"go get example.com/pkg@latest",
		"go build ./...",
		"go install ./cmd/x",
		"go run ./cmd/y",
		"go test ./...",
		"go vet ./...",
	} {
		if !depModInitThenGet(map[string]interface{}{"command": "go mod tidy"}, map[string]interface{}{"command": consumer}) {
			t.Errorf("depModInitThenGet must match module consumer %q (#2681 true positive lost)", consumer)
		}
	}
	// Producer side unchanged: non-module producers never match.
	if depModInitThenGet(map[string]interface{}{"command": "go build ./..."}, map[string]interface{}{"command": "go get x"}) {
		t.Error("non-module producer must not match")
	}
	// Missing consumer command (nil-safe) must not match.
	if depModInitThenGet(map[string]interface{}{"command": "go mod init m"}, nil) {
		t.Error("nil consumer args must not match")
	}
}
