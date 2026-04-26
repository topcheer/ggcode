//go:build unix

package restart

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

func writePlatformScript(req Request) (string, error) {
	td := templateData{
		PID:     req.PID,
		Binary:  bashEscape(req.Binary),
		WorkDir: bashEscape(req.WorkDir),
		Args:    req.Args,
	}

	var argParts []string
	for _, a := range req.Args {
		argParts = append(argParts, bashEscape(a))
	}
	td.ArgsBash = strings.Join(argParts, " ")

	tmpl, err := template.New("restart").Parse(scriptTemplate)
	if err != nil {
		return "", err
	}

	scriptPath := filepath.Join(os.TempDir(), fmt.Sprintf("ggcode-restart-%d.sh", req.PID))

	f, err := os.Create(scriptPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	if err := os.Chmod(scriptPath, 0o755); err != nil {
		_ = os.Remove(scriptPath)
		return "", err
	}

	if err := tmpl.Execute(f, td); err != nil {
		_ = os.Remove(scriptPath)
		return "", err
	}

	return scriptPath, nil
}

func launchPlatformScript(scriptPath string, req Request) error {
	cmd := exec.Command("/bin/bash", scriptPath)
	cmd.Env = os.Environ()
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = req.WorkDir

	if err := cmd.Start(); err != nil {
		_ = os.Remove(scriptPath)
		return fmt.Errorf("start restart script: %w", err)
	}
	return nil
}
