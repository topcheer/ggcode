//go:build windows

package image

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// CaptureScreen captures a screenshot on Windows using PowerShell.
func CaptureScreen(opts ScreenshotOptions) (ScreenshotResult, error) {
	applyDelay(opts.DelayMs)

	rawPath, cleanup := createTempScreenshotPath(opts)
	defer cleanup()

	script := buildWindowsScreenshotScript(rawPath, opts)

	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		return ScreenshotResult{}, fmt.Errorf("powershell screenshot failed: %w\n%s",
			err, strings.TrimSpace(string(out)))
	}

	img, err := finalizeImage(rawPath, opts)
	if err != nil {
		return ScreenshotResult{}, err
	}

	result := ScreenshotResult{Image: img}
	if opts.OutputPath != "" {
		result.SavedPath = opts.OutputPath
	}
	return result, nil
}

func buildWindowsScreenshotScript(outPath string, opts ScreenshotOptions) string {
	var sb strings.Builder
	sb.WriteString("Add-Type -AssemblyName System.Windows.Forms\n")
	sb.WriteString("Add-Type -AssemblyName System.Drawing\n")

	if opts.Window != "" {
		q := escapePowerShell(opts.Window)
		sb.WriteString(fmt.Sprintf(`
$p = Get-Process | Where-Object { $_.MainWindowTitle -like '*%s*' -or $_.ProcessName -like '*%s*' } | Where-Object { $_.MainWindowHandle -ne 0 } | Select-Object -First 1
if (-not $p) { Write-Error 'Window not found'; exit 1 }
Add-Type @'
using System;
using System.Runtime.InteropServices;
public class Win32 {
    [StructLayout(LayoutKind.Sequential)]
    public struct RECT { public int Left, Top, Right, Bottom; }
    [DllImport("user32.dll")]
    public static extern bool GetWindowRect(IntPtr hWnd, out RECT lpRect);
}
'@
$rect = New-Object Win32+RECT
[Win32]::GetWindowRect($p.MainWindowHandle, [ref]$rect) | Out-Null
$w = $rect.Right - $rect.Left
$h = $rect.Bottom - $rect.Top
$bmp = New-Object System.Drawing.Bitmap($w, $h)
$g = [System.Drawing.Graphics]::FromImage($bmp)
$g.CopyFromScreen($rect.Left, $rect.Top, 0, 0, $bmp.Size)
`, q, q))
	} else if opts.Region != nil {
		r := opts.Region
		sb.WriteString(fmt.Sprintf(
			"$bmp = New-Object System.Drawing.Bitmap(%d, %d)\n", r.Width, r.Height))
		sb.WriteString("$g = [System.Drawing.Graphics]::FromImage($bmp)\n")
		sb.WriteString(fmt.Sprintf(
			"$g.CopyFromScreen(%d, %d, 0, 0, (New-Object System.Drawing.Size(%d, %d)))\n",
			r.X, r.Y, r.Width, r.Height))
	} else {
		sb.WriteString("$screen = [System.Windows.Forms.Screen]::PrimaryScreen\n")
		sb.WriteString("$bounds = $screen.Bounds\n")
		sb.WriteString("$bmp = New-Object System.Drawing.Bitmap($bounds.Width, $bounds.Height)\n")
		sb.WriteString("$g = [System.Drawing.Graphics]::FromImage($bmp)\n")
		sb.WriteString("$g.CopyFromScreen($bounds.Location, [System.Drawing.Point]::Empty, $bounds.Size)\n")
	}

	sb.WriteString(fmt.Sprintf(
		"$bmp.Save('%s', [System.Drawing.Imaging.ImageFormat]::Png)\n",
		strings.ReplaceAll(outPath, "'", "''")))
	sb.WriteString("$g.Dispose()\n")
	sb.WriteString("$bmp.Dispose()\n")
	return sb.String()
}

// ListDisplays returns display information on Windows.
func ListDisplays() ([]DisplayInfo, error) {
	script := `
Add-Type -AssemblyName System.Windows.Forms
$screens = [System.Windows.Forms.Screen]::AllScreens
$result = @()
for ($i = 0; $i -lt $screens.Length; $i++) {
  $s = $screens[$i]
  $result += [PSCustomObject]@{
    index = $i + 1
    is_primary = $s.Primary
    width = $s.Bounds.Width
    height = $s.Bounds.Height
    x = $s.Bounds.X
    y = $s.Bounds.Y
  }
}
$result | ConvertTo-Json -Compress
`
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("listing displays: %w", err)
	}

	var displays []DisplayInfo
	if err := json.Unmarshal(out, &displays); err != nil {
		var single DisplayInfo
		if err2 := json.Unmarshal(out, &single); err2 == nil {
			displays = []DisplayInfo{single}
		}
	}
	return displays, nil
}

// ListWindows returns capturable windows on Windows.
func ListWindows() ([]WindowInfo, error) {
	script := `
Get-Process | Where-Object { $_.MainWindowHandle -ne 0 } | ForEach-Object {
  [PSCustomObject]@{
    id = $_.MainWindowHandle.ToInt64()
    title = $_.MainWindowTitle
    app = $_.ProcessName
  }
} | ConvertTo-Json -Compress
`
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("listing windows: %w", err)
	}

	var windows []WindowInfo
	if err := json.Unmarshal(out, &windows); err != nil {
		var single WindowInfo
		if err2 := json.Unmarshal(out, &single); err2 == nil {
			windows = []WindowInfo{single}
		}
	}
	return windows, nil
}

func escapePowerShell(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
