package restart

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/topcheer/ggcode/internal/debug"
)

// HelperRequest contains everything the detached helper process needs to
// wait for the parent to exit, optionally replace the binary, and launch
// a fresh ggcode instance.
type HelperRequest struct {
	// ParentPID is the PID of the ggcode process that spawned the helper.
	// The helper polls until this PID is gone before doing anything else.
	ParentPID int

	// Binary is the path to the ggcode binary to launch after the parent exits.
	// For /update, this should point to the newly staged binary.
	Binary string

	// Args are the CLI arguments to pass to the new ggcode process.
	Args []string

	// WorkDir is the working directory for the new process.
	WorkDir string

	// Env is the environment for the new process.
	Env []string

	// StagedBinary is the path to a newly downloaded binary that should replace
	// Binary before launching. If empty, no replacement occurs (pure restart).
	StagedBinary string
}

// RestartWithHelper launches a detached helper process that:
//  1. Waits for the current process (caller) to exit
//  2. Optionally replaces the binary (update scenario)
//  3. Resets the terminal to a clean state
//  4. Launches a fresh ggcode instance
//
// The helper is the ggcode binary itself running with the hidden
// "restart-helper" subcommand. It detaches into its own session via
// setsid (Unix) or CREATE_NEW_PROCESS_GROUP (Windows) so it never
// becomes an orphaned child — it owns the terminal after the parent exits.
//
// After calling this function, the caller should perform its normal
// shutdown (tea.Quit, terminal restore, release locks) and exit.
func RestartWithHelper(req HelperRequest) error {
	if req.ParentPID == 0 {
		req.ParentPID = os.Getpid()
	}
	if req.Binary == "" {
		b, err := ResolveBinary()
		if err != nil {
			return fmt.Errorf("resolve binary: %w", err)
		}
		req.Binary = b
	}
	if req.WorkDir == "" {
		req.WorkDir, _ = os.Getwd()
	}
	if len(req.Env) == 0 {
		req.Env = os.Environ()
	}

	// Build helper args
	helperArgs := []string{
		"restart-helper",
		"--pid", fmt.Sprintf("%d", req.ParentPID),
		"--binary", req.Binary,
		"--workdir", req.WorkDir,
	}
	if req.StagedBinary != "" {
		helperArgs = append(helperArgs, "--staged-binary", req.StagedBinary)
	}
	if len(req.Args) > 0 {
		// Pass remaining ggcode args after --
		helperArgs = append(helperArgs, "--")
		helperArgs = append(helperArgs, req.Args...)
	}

	cmd := exec.Command(req.Binary, helperArgs...)
	cmd.Dir = req.WorkDir
	cmd.Env = req.Env
	// Detach stdio — the helper must not share the parent's terminal
	// so it doesn't get SIGHUP when the parent exits.
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := detachHelper(cmd); err != nil {
		return fmt.Errorf("launch restart helper: %w", err)
	}

	debug.Log("restart", "helper launched: pid=%d parent_pid=%d binary=%s staged=%s",
		cmd.Process.Pid, req.ParentPID, req.Binary, req.StagedBinary)
	return nil
}

// RunHelper is the entry point for the "restart-helper" subcommand.
// It runs inside the detached helper process.
func RunHelper(req HelperRequest) error {
	debug.Log("restart-helper", "started: parent_pid=%d binary=%s staged=%s",
		req.ParentPID, req.Binary, req.StagedBinary)

	// 1. Wait for the parent process to exit.
	if err := waitForProcess(req.ParentPID); err != nil {
		return fmt.Errorf("wait for parent %d: %w", req.ParentPID, err)
	}
	debug.Log("restart-helper", "parent %d exited", req.ParentPID)

	// 2. Reset the terminal to a clean state (undo raw mode).
	resetTerminal()

	// 3. If a staged binary is provided, replace the target binary.
	if req.StagedBinary != "" {
		if err := replaceBinary(req.Binary, req.StagedBinary); err != nil {
			return fmt.Errorf("replace binary: %w", err)
		}
		debug.Log("restart-helper", "binary replaced: %s <- %s", req.Binary, req.StagedBinary)
	}

	// 4. Launch the new ggcode process, inheriting the terminal.
	return launchTarget(req)
}

// launchTarget execs/starts the new ggcode process. On Unix it uses
// syscall.Exec to replace itself (the helper) in-place, so the new
// ggcode inherits the session and terminal the helper set up.
// On Windows it starts a new process and exits.
