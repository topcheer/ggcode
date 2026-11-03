//go:build windows

package image

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

func CaptureScreen(opts ScreenshotOptions) (ScreenshotResult, error) {

	if opts.Region != nil && (opts.Region.Width <= 0 || opts.Region.Height <= 0) {
		return ScreenshotResult{}, fmt.Errorf("screenshot region must have positive width and height (got %dx%d)",
			opts.Region.Width, opts.Region.Height)
	}

	applyDelay(opts.DelayMs)

	rawPath, cleanup, err := createTempScreenshotPath(opts)
	if err != nil {
		return ScreenshotResult{}, err
	}
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

	sb.WriteString("$ErrorActionPreference = 'Stop'\n")
	sb.WriteString("try {\n")
	sb.WriteString("Add-Type -AssemblyName System.Windows.Forms\n")
	sb.WriteString("Add-Type -AssemblyName System.Drawing\n")

	sb.WriteString(windowsDpiAwarenessSnippet)

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
    [DllImport("dwmapi.dll")]
    public static extern int DwmGetWindowAttribute(IntPtr hWnd, int attr, out RECT rect, int size);
}
'@
$rect = New-Object Win32+RECT
# DWMWA_EXTENDED_FRAME_BOUNDS (9) returns the *visible* window rect;
# GetWindowRect includes the invisible resize borders (~7px per side on
# Win10/11). Fall back to GetWindowRect when DWM is unavailable (#976).
if ([Win32]::DwmGetWindowAttribute($p.MainWindowHandle, 9, [ref]$rect, 16) -ne 0) {
  [Win32]::GetWindowRect($p.MainWindowHandle, [ref]$rect) | Out-Null
}
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
		sb.WriteString(fmt.Sprintf("$ox = %d\n$oy = %d\n", r.X, r.Y))
		writeWindowsCursorDraw(&sb, opts)
	} else {

		if idx, ok := displayScreenIndex(opts.Display); ok {
			sb.WriteString(fmt.Sprintf(
				"$screen = [System.Windows.Forms.Screen]::AllScreens[%d]\n", idx))
			sb.WriteString("if ($null -eq $screen) { $screen = [System.Windows.Forms.Screen]::PrimaryScreen }\n")
		} else {
			sb.WriteString("$screen = [System.Windows.Forms.Screen]::PrimaryScreen\n")
		}
		sb.WriteString("$bounds = $screen.Bounds\n")
		sb.WriteString("$ox = $bounds.X\n")
		sb.WriteString("$oy = $bounds.Y\n")
		sb.WriteString("$bmp = New-Object System.Drawing.Bitmap($bounds.Width, $bounds.Height)\n")
		sb.WriteString("$g = [System.Drawing.Graphics]::FromImage($bmp)\n")
		sb.WriteString("$g.CopyFromScreen($bounds.Location, [System.Drawing.Point]::Empty, $bounds.Size)\n")
		writeWindowsCursorDraw(&sb, opts)
	}

	sb.WriteString(fmt.Sprintf(
		"$bmp.Save('%s', [System.Drawing.Imaging.ImageFormat]::Png)\n",
		strings.ReplaceAll(outPath, "'", "''")))
	sb.WriteString("$g.Dispose()\n")
	sb.WriteString("$bmp.Dispose()\n")
	sb.WriteString("} catch { [Console]::Error.WriteLine($_.Exception.Message); exit 1 }\n")
	return sb.String()
}

func ListDisplays() ([]DisplayInfo, error) {

	script := buildWindowsListDisplaysScript()
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("listing displays: %w", err)
	}

	var displays []DisplayInfo
	displays, err = parseDisplayInfos(out)
	if err != nil {
		return nil, fmt.Errorf("listing displays: %w", err)
	}
	return displays, nil
}

func parseDisplayInfos(out []byte) ([]DisplayInfo, error) {
	trimmed := bytes.TrimSpace(out)
	if len(trimmed) == 0 {
		return nil, nil
	}
	var displays []DisplayInfo
	if err := json.Unmarshal(trimmed, &displays); err != nil {
		var single DisplayInfo
		if err2 := json.Unmarshal(trimmed, &single); err2 == nil {
			return []DisplayInfo{single}, nil
		}
		return nil, fmt.Errorf("unexpected displays output (not valid JSON): %.120s", trimmed)
	}
	return displays, nil
}

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
	windows, err = parseWindowInfos(out)
	if err != nil {
		return nil, fmt.Errorf("listing windows: %w", err)
	}
	return windows, nil
}

func parseWindowInfos(out []byte) ([]WindowInfo, error) {
	trimmed := bytes.TrimSpace(out)
	if len(trimmed) == 0 {
		return nil, nil
	}
	var windows []WindowInfo
	if err := json.Unmarshal(trimmed, &windows); err != nil {
		var single WindowInfo
		if err2 := json.Unmarshal(trimmed, &single); err2 == nil {
			return []WindowInfo{single}, nil
		}
		return nil, fmt.Errorf("unexpected windows output (not valid JSON): %.120s", trimmed)
	}
	return windows, nil
}

func escapePowerShell(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

func writeWindowsCursorDraw(sb *strings.Builder, opts ScreenshotOptions) {
	if !opts.Cursor {
		return
	}
	sb.WriteString(`
$cp = [System.Windows.Forms.Cursor]::Position
$cur = [System.Windows.Forms.Cursor]::Current
if ($null -ne $cur) {
  $crect = New-Object System.Drawing.Rectangle(($cp.X - $ox), ($cp.Y - $oy), $cur.Size.Width, $cur.Size.Height)
  $cur.Draw($g, $crect)
}
`)
}
