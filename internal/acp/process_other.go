//go:build !unix

package acp

import "os/exec"

func configureACPCommandProcess(cmd *exec.Cmd) {
}

func killACPProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
