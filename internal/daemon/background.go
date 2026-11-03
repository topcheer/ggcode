package daemon

import (
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/topcheer/ggcode/internal/debug"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

var osStartProcess = os.StartProcess

var testProcessCmdline func(int) string

type DaemonInfo struct {
	PID        int       `json:"pid"`
	SessionID  string    `json:"session_id"`
	WorkingDir string    `json:"working_dir"`
	StartedAt  time.Time `json:"started_at"`
}

func daemonDir() (string, error) {
	dir := filepath.Join(config.ConfigDir(), "daemon")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating daemon dir: %w", err)
	}
	return dir, nil
}

func workDirHash(workingDir string) string {
	h := md5.Sum([]byte(workingDir))
	return fmt.Sprintf("%x", h)[:12]
}

func PIDFilePath(workingDir string) (string, error) {
	dir, err := daemonDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, workDirHash(workingDir)+".pid"), nil
}

func LogFilePath(workingDir string) (string, error) {
	dir, err := daemonDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, workDirHash(workingDir)+".log"), nil
}

func WritePIDFile(path string, pid int, sessionID, workingDir string) error {

	f, err := openPIDFile(path)
	if err != nil {
		return err
	}

	if err := flockNonBlocking(f); err != nil {
		f.Close()
		return fmt.Errorf("daemon slot already locked (concurrent fork): %w", err)
	}

	info := DaemonInfo{
		PID:        pid,
		SessionID:  sessionID,
		WorkingDir: workingDir,
		StartedAt:  time.Now(),
	}
	data, err := json.Marshal(info)
	if err != nil {
		f.Close()
		return err
	}

	if _, err := f.Seek(0, 0); err != nil {
		f.Close()
		return err
	}
	if err := f.Truncate(0); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}

	return nil
}

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

func CheckExistingDaemon(workingDir string) (int, error) {
	pidPath, err := PIDFilePath(workingDir)
	if err != nil {
		return 0, err
	}
	info, err := ReadPIDFile(pidPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		var syntaxErr *json.SyntaxError
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &syntaxErr) || errors.As(err, &typeErr) {

			_ = os.Remove(pidPath)
			return 0, nil
		}

		debug.Log("daemon", "PID file %s unreadable (transient/permission); NOT removing: %v", pidPath, err)
		return 0, fmt.Errorf("reading PID file %s: %w", pidPath, err)
	}

	proc, err := os.FindProcess(info.PID)
	if err != nil {
		return 0, nil
	}

	if err := checkProcessAlive(proc); err != nil {

		_ = os.Remove(pidPath)
		return 0, nil
	}

	if !daemonIdentityMatches(info.PID, workingDir) {
		debug.Log("daemon", "PID %d alive but not a ggcode daemon (recycled PID); cleaning stale PID file %s", info.PID, pidPath)
		_ = os.Remove(pidPath)
		return 0, nil
	}
	return info.PID, nil
}

func daemonIdentityMatches(pid int, workingDir string) bool {
	var cmdline string
	if testProcessCmdline != nil {
		cmdline = testProcessCmdline(pid)
	} else {
		cmdline = processCmdline(pid)
	}
	if cmdline != "" {

		if strings.Contains(cmdline, "--__daemonized") || strings.Contains(cmdline, "ggcode[") {
			return true
		}

		base := strings.ToLower(strings.TrimSpace(cmdline))
		if strings.HasSuffix(base, ".exe") {
			base = strings.TrimSuffix(base, ".exe")
		}
		return base == "ggcode" || strings.HasPrefix(base, "ggcode-")
	}

	_ = workingDir
	proc, err := os.FindProcess(pid)
	if err != nil {

		debug.Log("daemon", "daemonIdentityMatches: PID %d not found (os.FindProcess error)", pid)
		return false
	}
	if err := checkProcessAlive(proc); err != nil {

		debug.Log("daemon", "daemonIdentityMatches: PID %d signal-0 check failed (process dead)", pid)
		return false
	}

	debug.Log("daemon", "daemonIdentityMatches: PID %d alive, PID file present; assuming daemon (cmdline unavailable)", pid)
	return true
}

func EnsureDaemonSlot(workingDir string) error {
	pid, err := CheckExistingDaemon(workingDir)
	if err != nil {
		return fmt.Errorf("checking existing daemon: %w", err)
	}
	if pid != 0 {
		return fmt.Errorf("daemon already running for %s (pid %d)", workingDir, pid)
	}
	return nil
}

func backgroundStdin() (*os.File, error) {
	return os.OpenFile(os.DevNull, os.O_RDWR, 0)
}

func ForkIntoBackground(cfgFile, workingDir, sessionID string, extraArgs ...string) (int, error) {
	executable, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("finding executable: %w", err)
	}

	dirname := filepath.Base(workingDir)
	displayName := "ggcode[" + dirname + "]"

	args := make([]string, len(os.Args), len(os.Args)+1+len(extraArgs))
	copy(args, os.Args)
	args = append(args, "--__daemonized")
	args = append(args, extraArgs...)

	args[0] = displayName

	logPath, err := LogFilePath(workingDir)
	if err != nil {
		return 0, fmt.Errorf("resolving log path: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, fmt.Errorf("opening log file: %w", err)
	}

	pidPath, err := PIDFilePath(workingDir)
	if err != nil {
		return 0, fmt.Errorf("resolving PID path: %w", err)
	}

	stdin, err := backgroundStdin()
	if err != nil {
		logFile.Close()
		return 0, fmt.Errorf("opening %s for daemon stdin: %w", os.DevNull, err)
	}

	procAttr := &os.ProcAttr{
		Dir: workingDir,
		Env: os.Environ(),
		Files: []*os.File{
			stdin,
			logFile,
			logFile,
		},
		Sys: newBackgroundSysProcAttr(),
	}

	process, err := osStartProcess(executable, args, procAttr)
	stdin.Close()
	logFile.Close()
	if err != nil {

		_ = os.Remove(pidPath)
		return 0, fmt.Errorf("starting background process: %w", err)
	}

	if err := WritePIDFile(pidPath, process.Pid, sessionID, workingDir); err != nil {

		debug.Log("daemon", "could not write PID file: %v", err)
	}

	return process.Pid, nil
}

func CleanupDaemon(workingDir string) {
	pidPath, err := PIDFilePath(workingDir)
	if err != nil {
		return
	}
	info, err := ReadPIDFile(pidPath)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		var syntaxErr *json.SyntaxError
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &syntaxErr) || errors.As(err, &typeErr) {

			_ = os.Remove(pidPath)
			return
		}

		debug.Log("daemon", "CleanupDaemon: PID file %s unreadable, NOT removing: %v", pidPath, err)
		return
	}
	if info.PID != os.Getpid() {
		debug.Log("daemon", "CleanupDaemon: PID file %s owned by pid %d (not us %d); keeping it", pidPath, info.PID, os.Getpid())
		return
	}
	_ = os.Remove(pidPath)
}
