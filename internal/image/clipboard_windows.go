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
	output, err := runPowerShell(windowsClipboardImageScript(outPath))
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if ok := errors.As(err, &exitErr); ok && exitErr.ExitCode() == 3 {
		return ErrClipboardImageUnavailable
	}
	return commandOutputError("reading clipboard image", err, output)
}

func windowsClipboardImageScript(outPath string) string {
	quotedOutPath := escapePowerShellSingleQuoted(outPath)
	return strings.Join([]string{
		"Add-Type -AssemblyName System.Windows.Forms",
		"Add-Type -AssemblyName System.Drawing",
		"if ([System.Windows.Forms.Clipboard]::ContainsImage()) {",
		"  $img = [System.Windows.Forms.Clipboard]::GetImage()",
		"  if ($null -eq $img) { exit 3 }",
		fmt.Sprintf("  $path = '%s'", quotedOutPath),
		"  try {",
		"    $img.Save($path, [System.Drawing.Imaging.ImageFormat]::Png)",
		"  } finally {",
		"    $img.Dispose()",
		"  }",
		"  exit 0",
		"}",
		"if ([System.Windows.Forms.Clipboard]::ContainsFileDropList()) {",
		"  foreach ($file in [System.Windows.Forms.Clipboard]::GetFileDropList()) {",
		"    if ([string]::IsNullOrWhiteSpace($file)) { continue }",
		"    $ext = [System.IO.Path]::GetExtension($file).ToLowerInvariant()",
		"    if (@('.png', '.jpg', '.jpeg', '.gif', '.webp') -contains $ext) {",
		fmt.Sprintf("      Copy-Item -LiteralPath $file -Destination '%s' -Force", quotedOutPath),
		"      exit 0",
		"    }",
		"  }",
		"}",
		"exit 3",
	}, "; ")
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
