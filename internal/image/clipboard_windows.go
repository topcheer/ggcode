//go:build windows

package image

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func ReadClipboard() (Image, error) {
	tmpDir, err := os.MkdirTemp("", "ggcode-clipboard-*")
	if err != nil {
		return Image{}, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	pngPath := filepath.Join(tmpDir, "clipboard.png")
	if err := writeWindowsClipboardImage(pngPath); err != nil {
		return Image{}, err
	}
	return ReadFile(pngPath)
}

func writeWindowsClipboardImage(outPath string) error {
	script := strings.Join([]string{
		"Add-Type -AssemblyName System.Windows.Forms",
		"Add-Type -AssemblyName System.Drawing",
		"if (-not [System.Windows.Forms.Clipboard]::ContainsImage()) { exit 3 }",
		"$img = [System.Windows.Forms.Clipboard]::GetImage()",
		"if ($null -eq $img) { exit 3 }",
		fmt.Sprintf("$path = '%s'", escapePowerShellSingleQuoted(outPath)),
		"try {",
		"  $img.Save($path, [System.Drawing.Imaging.ImageFormat]::Png)",
		"} finally {",
		"  $img.Dispose()",
		"}",
	}, "; ")

	output, err := runPowerShell(script)
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if ok := errors.As(err, &exitErr); ok && exitErr.ExitCode() == 3 {
		return ErrClipboardImageUnavailable
	}
	return commandOutputError("reading clipboard image", err, output)
}

func runPowerShell(script string) ([]byte, error) {
	for _, name := range []string{"powershell", "pwsh"} {
		if !commandAvailable(name) {
			continue
		}
		cmd := exec.Command(name, "-NoProfile", "-NonInteractive", "-STA", "-Command", script)
		output, err := cmd.CombinedOutput()
		if err == nil {
			return output, nil
		}
		return output, err
	}
	return nil, fmt.Errorf("PowerShell is not available")
}

func escapePowerShellSingleQuoted(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}
