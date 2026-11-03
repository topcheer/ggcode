package agent

import "testing"

func TestIssue2681_ModInitThenGetChecksConsumer(t *testing.T) {

	for _, consumer := range []string{"git status", "ls -la", "cat go.mod", "echo done"} {
		if depModInitThenGet(map[string]interface{}{"command": "go mod tidy"}, map[string]interface{}{"command": consumer}) {
			t.Errorf("depModInitThenGet matched read-only consumer %q (#2681 false positive)", consumer)
		}
	}

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

	if depModInitThenGet(map[string]interface{}{"command": "go build ./..."}, map[string]interface{}{"command": "go get x"}) {
		t.Error("non-module producer must not match")
	}

	if depModInitThenGet(map[string]interface{}{"command": "go mod init m"}, nil) {
		t.Error("nil consumer args must not match")
	}
}
