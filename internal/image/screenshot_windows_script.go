package image

import "strings"

// windowsDpiAwarenessSnippet is prepended to every Windows PowerShell script
// that queries screen or window geometry (#763, extended to display listing
// in #976). powershell.exe is DPI-unaware by default, so
// [System.Windows.Forms.Screen]::AllScreens and friends report virtualized
// 96-DPI logical coordinates on scaled displays (a 2560x1440 panel at 150%
// reports 1707x960). Without this preamble ListDisplays and the capture
// scripts disagree about the coordinate space, and Region captures computed
// from DisplayInfo land in the wrong place.
// #1571-C: SetProcessDPIAware is only SYSTEM-aware - on mixed-DPI setups
// (primary 100%, secondary 150%) secondary-screen bounds were virtualized
// by the primary DPI and CopyFromScreen regions landed offset. Prefer
// SetProcessDpiAwarenessContext with PER_MONITOR_AWARE_V2 (the preamble's
// own comment promises "whole chain works in physical pixels" per
// monitor); fall back to the legacy call on old Windows.
const windowsDpiAwarenessSnippet = `Add-Type @'
using System.Runtime.InteropServices;
public class Win32Dpi {
    [DllImport("user32.dll")]
    public static extern bool SetProcessDpiAwarenessContext(IntPtr value);
    [DllImport("user32.dll")]
    public static extern bool SetProcessDPIAware();
}
'@
$PMv2 = [IntPtr](-4)
# #1754 case 2: SetProcessDPIAwarenessContext returns FALSE (no exception)
# when the context is already set or the session is restricted - Out-Null
# swallowed that, the catch never ran, and the process silently stayed
# DPI-unaware (the #763 bug the preamble exists to prevent). Check the
# return; fall back to the legacy API, and keep the catch for pre-1703
# machines where the P/Invoke itself throws.
try { if (-not [Win32Dpi]::SetProcessDpiAwarenessContext($PMv2)) { [Win32Dpi]::SetProcessDPIAware() | Out-Null } } catch { [Win32Dpi]::SetProcessDPIAware() | Out-Null }
`

// buildWindowsListDisplaysScript returns the PowerShell script used by
// ListDisplays on Windows. The DPI-awareness preamble must run before
// AllScreens is queried so the reported bounds are physical pixels,
// matching the coordinate space of the capture scripts (#976).
func buildWindowsListDisplaysScript() string {
	var sb strings.Builder
	sb.WriteString("Add-Type -AssemblyName System.Windows.Forms\n")
	sb.WriteString(windowsDpiAwarenessSnippet)
	sb.WriteString(`
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
`)
	return sb.String()
}
