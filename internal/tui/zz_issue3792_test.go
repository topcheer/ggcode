package tui

import (
	"os"
	"strings"
	"testing"
)

func TestIssue3792_ForkCopiesTasksEnvJSON(t *testing.T) {
	for _, tc := range []struct {
		file string
		mark string
	}{
		{"rewind.go", "rewound.TasksEnvJSON = append("},
		{"commands_slash.go", "branched.TasksEnvJSON = append("},
	} {
		raw, err := os.ReadFile(tc.file)
		if err != nil {
			t.Fatalf("%s: %v", tc.file, err)
		}
		src := string(raw)
		if !strings.Contains(src, tc.mark) {
			t.Fatalf("%s: fork copy of the workspace fingerprint missing (want %q)", tc.file, tc.mark)
		}

		board := strings.Index(src, strings.Replace(tc.mark, "TasksEnvJSON", "TasksJSON", 1))
		env := strings.Index(src, tc.mark)
		if board < 0 || env < 0 || env < board {
			t.Fatalf("%s: fingerprint copy must follow the board copy", tc.file)
		}
	}
}
