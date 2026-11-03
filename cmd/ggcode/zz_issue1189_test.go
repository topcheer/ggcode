package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/runfile"
)

func TestIssue1189PortFileCreatedForNewSessions(t *testing.T) {

	tmpDir := t.TempDir()

	pfEmpty := runfile.PortFile{
		Addr:      "127.0.0.1:12345",
		Token:     "test-token",
		PID:       os.Getpid(),
		SessionID: "",
		Workspace: tmpDir,
		Mode:      "auto",
	}

	err := runfile.Write(pfEmpty)
	if err == nil {
		t.Error("runfile.Write with empty SessionID should fail, but succeeded")
	}

	pfReal := runfile.PortFile{
		Addr:      "127.0.0.1:12346",
		Token:     "test-token-2",
		PID:       os.Getpid(),
		SessionID: "test-session-id-123",
		Workspace: tmpDir,
		Mode:      "supervised",
	}

	if err := runfile.Write(pfReal); err != nil {
		t.Fatalf("runfile.Write with real SessionID failed: %v", err)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("failed to get home directory: %v", err)
	}
	portFilePath := filepath.Join(homeDir, ".ggcode", "run", pfReal.SessionID+".json")
	content, err := os.ReadFile(portFilePath)
	if err != nil {
		t.Fatalf("failed to read port file: %v", err)
	}

	fileContent := string(content)
	if !strings.Contains(fileContent, pfReal.SessionID) {
		t.Errorf("port file does not contain expected session ID %s", pfReal.SessionID)
	}

	runfile.Remove(pfReal.SessionID)
}
