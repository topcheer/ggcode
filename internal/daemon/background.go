package daemon

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// DaemonInfo holds metadata about a running daemon process.
type DaemonInfo struct {
	PID        int       `json:"pid"`
	SessionID  string    `json:"session_id"`
	WorkingDir string    `json:"working_dir"`
	StartedAt  time.Time `json:"started_at"`
}

// daemonDir returns ~/.ggcode/daemon/, creating it if needed.
func daemonDir() (string, error) {
	dir := filepath.Join(config.ConfigDir(), "daemon")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating daemon dir: %w", err)
	}
	return dir, nil
}

// workDirHash returns a short hex hash for the working directory path.
func workDirHash(workingDir string) string {
	h := md5.Sum([]byte(workingDir))
	return fmt.Sprintf("%x", h)[:12]
}

// PIDFilePath returns the PID file path for a given working directory.
func PIDFilePath(workingDir string) (string, error) {
	dir, err := daemonDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, workDirHash(workingDir)+".pid"), nil
}

// LogFilePath returns the log file path for a given working directory.
func LogFilePath(workingDir string) (string, error) {
	dir, err := daemonDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, workDirHash(workingDir)+".log"), nil
}

// WritePIDFile writes a PID file with daemon metadata.
func WritePIDFile(path string, pid int, sessionID, workingDir string) error {
	info := DaemonInfo{
		PID:        pid,
		SessionID:  sessionID,
		WorkingDir: workingDir,
		StartedAt:  time.Now(),
	}
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// RemovePIDFile deletes the PID file.
func RemovePIDFile(path string) error {
	return os.Remove(path)
}

// ReadPIDFile reads daemon info from a PID file.
func ReadPIDFile(path string) (*DaemonInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var info DaemonInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// CheckExistingDaemon checks if a daemon is already running for the given working directory.
// Returns the PID if a running daemon is found, or 0 if none.
func CheckExistingDaemon(workingDir string) (int, error) {
	pidPath, err := PIDFilePath(workingDir)
	if err != nil {
		return 0, err
	}
	info, err := ReadPIDFile(pidPath)
	if err != nil {
		return 0, nil // no PID file = no daemon
	}
	// Check if process is still alive
	proc, err := os.FindProcess(info.PID)
	if err != nil {
		return 0, nil
	}
	// Send signal 0 to check existence
	if err := checkProcessAlive(proc); err != nil {
		// Process doesn't exist, clean up stale PID file
		_ = os.Remove(pidPath)
		return 0, nil
	}
	return info.PID, nil
}

// ForkIntoBackground re-execs the current binary as a background daemon.
// The child process argv[0] is set to "ggcode[dirname]".
// stdout/stderr are redirected to a log file.
// Returns the child PID; the caller (parent) should os.Exit(0).
func ForkIntoBackground(cfgFile, workingDir, sessionID string, extraArgs ...string) (int, error) {
	executable, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("finding executable: %w", err)
	}

	dirname := filepath.Base(workingDir)
	displayName := "ggcode[" + dirname + "]"

	// Build args: original args + hidden flag + extra args
	args := make([]string, len(os.Args), len(os.Args)+1+len(extraArgs))
	copy(args, os.Args)
	args = append(args, "--__daemonized")
	args = append(args, extraArgs...)
	// Set argv[0] to display name
	args[0] = displayName

	// Open log file
	logPath, err := LogFilePath(workingDir)
	if err != nil {
		return 0, fmt.Errorf("resolving log path: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, fmt.Errorf("opening log file: %w", err)
	}

	// Write PID file path
	pidPath, err := PIDFilePath(workingDir)
	if err != nil {
		return 0, fmt.Errorf("resolving PID path: %w", err)
	}

	procAttr := &os.ProcAttr{
		Dir: workingDir,
		Env: os.Environ(),
		Files: []*os.File{
			os.Stdin, // keep stdin for potential keyboard reads (will be /dev/null in background)
			logFile,  // stdout → log
			logFile,  // stderr → log
		},
		Sys: newBackgroundSysProcAttr(),
	}

	process, err := os.StartProcess(executable, args, procAttr)
	logFile.Close()
	if err != nil {
		return 0, fmt.Errorf("starting background process: %w", err)
	}

	// Write PID file
	if err := WritePIDFile(pidPath, process.Pid, sessionID, workingDir); err != nil {
		// Non-fatal: the daemon is running but we couldn't record it
		fmt.Fprintf(os.Stderr, "warning: could not write PID file: %v\n", err)
	}

	return process.Pid, nil
}

// CleanupDaemon removes the PID file for the given working directory.
func CleanupDaemon(workingDir string) {
	pidPath, err := PIDFilePath(workingDir)
	if err != nil {
		return
	}
	_ = os.Remove(pidPath)
}

// FormatPID returns a human-readable PID string.
func FormatPID(pid int) string {
	return strconv.Itoa(pid)
}
