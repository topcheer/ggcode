//go:build darwin

package tool

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func sandboxWrap(cmd *exec.Cmd, workspace string, p *SandboxPolicy) (bool, error) {
	bin, err := exec.LookPath("sandbox-exec")
	if err != nil {
		return false, fmt.Errorf("%w: sandbox-exec not found in PATH", errSandboxUnavailable)
	}
	profile, err := seatbeltProfile(workspace, p)
	if err != nil {
		return false, err
	}
	shellPath := cmd.Path
	shellArgs := append([]string{}, cmd.Args[1:]...)
	cmd.Path = bin
	cmd.Args = append([]string{bin, "-p", profile, shellPath}, shellArgs...)
	return true, nil
}

func seatbeltProfile(workspace string, p *SandboxPolicy) (string, error) {
	allowPaths := []string{"/dev/null"}

	addPath := func(path string) error {
		if path == "" {
			return nil
		}
		if !seatbeltEscaped(path) {
			return fmt.Errorf("%w: path %q contains profile-unsafe characters", errSandboxUnavailable, path)
		}
		resolved, cleaned := resolveSandboxPath(path)
		for _, form := range []string{resolved, cleaned} {
			if form != "" && !containsSandboxArg(allowPaths, form) {
				allowPaths = append(allowPaths, form)
			}
		}
		return nil
	}

	if err := addPath(workspace); err != nil {
		return "", err
	}

	if err := addPath(os.TempDir()); err != nil {
		return "", err
	}
	if err := addPath("/tmp"); err != nil {
		return "", err
	}
	for _, extra := range p.ExtraWritePaths {
		if err := addPath(extra); err != nil {
			return "", err
		}
	}

	var sb strings.Builder
	sb.WriteString("(version 1)\n")
	sb.WriteString("(allow default)\n")
	sb.WriteString("(deny file-write*)\n")
	sb.WriteString("(allow file-write*")
	for _, ap := range allowPaths {
		sb.WriteString(fmt.Sprintf(" (subpath %q)", ap))
	}
	sb.WriteString(")\n")
	if !p.AllowNetwork {
		sb.WriteString("(deny network*)\n")
	}
	return sb.String(), nil
}

func containsSandboxArg(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
