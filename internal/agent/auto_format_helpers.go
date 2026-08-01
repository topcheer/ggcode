package agent

// Helper functions for auto_format.go, separated to keep the main file
// focused on formatter selection and orchestration.
// These use os/os/exec directly but are isolated here for testability.

import (
	"os"
	"os/exec"
)

// execLookStat returns the file size or -1 on error.
func execLookStat(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return -1, err
	}
	return info.Size(), nil
}

// execLookReadFile reads the entire file, returning "" on error.
func execLookReadFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// execKillProcess kills a running process.
func execKillProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
