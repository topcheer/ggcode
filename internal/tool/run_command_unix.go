//go:build unix

package tool

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	commandTerminateGracePeriod = 100 * time.Millisecond
	commandWaitDelay            = 250 * time.Millisecond
)

type commandTargets struct {
	rootPID      int
	processGroup int
	descendants  []int
}

func configureCommandCancellation(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		targets := snapshotCommandTargets(cmd.Process.Pid)
		err := signalCommandTargets(targets, syscall.SIGTERM)
		time.Sleep(commandTerminateGracePeriod)
		killErr := signalCommandTargets(targets, syscall.SIGKILL)
		if err != nil {
			return err
		}
		return killErr
	}
	cmd.WaitDelay = commandWaitDelay
}

func signalCommandTree(rootPID int, sig syscall.Signal) error {
	return signalCommandTargets(snapshotCommandTargets(rootPID), sig)
}

func snapshotCommandTargets(rootPID int) commandTargets {
	targets := commandTargets{
		rootPID:     rootPID,
		descendants: descendantPIDs(rootPID),
	}
	if pgid, err := syscall.Getpgid(rootPID); err == nil {
		targets.processGroup = pgid
	}
	return targets
}

func signalCommandTargets(targets commandTargets, sig syscall.Signal) error {
	rootPID := targets.rootPID
	if rootPID <= 0 {
		return nil
	}

	var firstErr error
	recordErr := func(err error) {
		if err == nil || errors.Is(err, os.ErrProcessDone) || err == syscall.ESRCH {
			return
		}
		if firstErr == nil {
			firstErr = err
		}
	}

	if targets.processGroup > 0 {
		recordErr(syscall.Kill(-targets.processGroup, sig))
	}

	for _, pid := range targets.descendants {
		recordErr(syscall.Kill(pid, sig))
	}
	recordErr(syscall.Kill(rootPID, sig))

	return firstErr
}

func descendantPIDs(rootPID int) []int {
	out, err := exec.Command("ps", "-eo", "pid=,ppid=").Output()
	if err != nil {
		return nil
	}

	children := make(map[int][]int)
	lines := bytes.Split(out, []byte{'\n'})
	for _, line := range lines {
		fields := strings.Fields(string(line))
		if len(fields) != 2 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil || pid <= 0 || ppid <= 0 {
			continue
		}
		children[ppid] = append(children[ppid], pid)
	}

	var outPIDs []int
	queue := append([]int(nil), children[rootPID]...)
	seen := make(map[int]struct{}, len(queue))
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if _, ok := seen[pid]; ok {
			continue
		}
		seen[pid] = struct{}{}
		outPIDs = append(outPIDs, pid)
		queue = append(queue, children[pid]...)
	}
	return outPIDs
}
