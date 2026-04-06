//go:build unix

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const detachedHelperEnv = "GGCODE_TEST_DETACHED_HELPER"
const detachedChildEnv = "GGCODE_TEST_DETACHED_CHILD"

func TestRunCommandDetachedHelper(t *testing.T) {
	if os.Getenv(detachedHelperEnv) != "1" {
		t.Skip("helper process only")
	}

	pidFile := os.Args[len(os.Args)-1]
	if os.Getenv(detachedChildEnv) == "1" {
		if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			t.Fatalf("write child pid: %v", err)
		}
		time.Sleep(60 * time.Second)
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestRunCommandDetachedHelper", "--", pidFile)
	cmd.Env = append(os.Environ(), detachedHelperEnv+"=1", detachedChildEnv+"=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start detached child: %v", err)
	}
	time.Sleep(60 * time.Second)
}

func TestRunCommand_ContextCancelStopsDetachedDescendants(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}

	rc := RunCommand{WorkingDir: t.TempDir()}
	pidFile := rc.WorkingDir + "/detached.pid"
	command := fmt.Sprintf("%s=1 %q -test.run=TestRunCommandDetachedHelper -- %q", detachedHelperEnv, exe, pidFile)
	input := json.RawMessage(fmt.Sprintf(`{"command": %q}`, command))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan Result, 1)
	go func() {
		result, _ := rc.Execute(ctx, input)
		done <- result
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(pidFile); err == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if _, err := os.Stat(pidFile); err != nil {
		t.Fatalf("expected detached child pid file to appear: %v", err)
	}

	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read detached pid file: %v", err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil {
		t.Fatalf("parse detached pid: %v", err)
	}
	defer func() {
		_ = syscall.Kill(childPID, syscall.SIGKILL)
	}()

	cancel()

	select {
	case result := <-done:
		if !result.IsError {
			t.Fatalf("expected canceled command to report an error result")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("expected canceled command with detached descendants to stop promptly")
	}

	time.Sleep(150 * time.Millisecond)
	if err := syscall.Kill(childPID, 0); err == nil {
		t.Fatalf("expected detached child %d to be terminated", childPID)
	} else if err != syscall.ESRCH {
		t.Fatalf("expected detached child lookup to return ESRCH, got %v", err)
	}
}
