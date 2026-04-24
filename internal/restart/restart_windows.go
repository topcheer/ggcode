//go:build windows

package restart

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

const winScriptTemplate = `@echo off
REM ggcode self-restart helper - auto-generated, self-deleting.
setlocal enabledelayedexpansion

set PARENT_PID={{.PID}}
set BINARY={{.BinaryWinEscaped}}
set WORK_DIR={{.WorkDirWinEscaped}}

echo [ggcode restart] waiting for process %PARENT_PID% to exit...

REM 1. Wait for parent to exit (poll with tasklist, 30s timeout)
set /a DEADLINE=0
:waitloop
tasklist /FI "PID eq %PARENT_PID%" /NH 2>nul | findstr /I "%PARENT_PID%" >nul
if errorlevel 1 goto exited
set /a DEADLINE+=1
if %DEADLINE% GEQ 300 (
    echo [ggcode restart] ERROR: process %PARENT_PID% did not exit within 30s
    del /f "%~f0" 2>nul
    exit /b 1
)
ping -n 1 127.0.0.1 >nul
goto waitloop

:exited
echo [ggcode restart] process %PARENT_PID% exited

REM 2. Brief pause
ping -n 1 127.0.0.1 >nul

REM 3. cd to original working directory
cd /d "%WORK_DIR%" 2>nul

REM 4. Self-delete (schedule removal after exit)
start "" /b cmd /c "del /f /q "%~f0" 2>nul"

REM 5. Launch new process
echo [ggcode restart] starting %BINARY% {{.ArgsWin}}
%BINARY% {{.ArgsWin}}
`

type winTemplateData struct {
	PID               int
	BinaryWinEscaped  string
	WorkDirWinEscaped string
	ArgsWin           string
}

func writePlatformScript(req Request) (string, error) {
	td := winTemplateData{
		PID:               req.PID,
		BinaryWinEscaped:  winEscape(req.Binary),
		WorkDirWinEscaped: winEscape(req.WorkDir),
	}

	var argParts []string
	for _, a := range req.Args {
		argParts = append(argParts, winEscape(a))
	}
	td.ArgsWin = strings.Join(argParts, " ")

	tmpl, err := template.New("restart").Parse(winScriptTemplate)
	if err != nil {
		return "", err
	}

	scriptPath := filepath.Join(os.TempDir(), fmt.Sprintf("ggcode-restart-%d.cmd", req.PID))

	f, err := os.Create(scriptPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	if err := tmpl.Execute(f, td); err != nil {
		_ = os.Remove(scriptPath)
		return "", err
	}

	return scriptPath, nil
}

func winEscape(s string) string {
	// Escape double quotes for batch files.
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func launchPlatformScript(scriptPath string, req Request) error {
	cmd := exec.Command("cmd", "/c", scriptPath)
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
