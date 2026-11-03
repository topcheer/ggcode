package image

import "strings"

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
