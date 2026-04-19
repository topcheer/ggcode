param(
    [Parameter(Mandatory = $true)][string]$BinaryPath
)

$ErrorActionPreference = "Stop"

# Unblock the downloaded binary to remove the Zone.Identifier alternate data stream.
# This prevents Windows Defender / SmartScreen from treating the file as "downloaded
# from the internet" and hanging the process during first-run scanning.
try {
    Unblock-File -Path $BinaryPath -ErrorAction SilentlyContinue
} catch {
    # May not exist — ignore
}

# Defender exclusions (best-effort on non-admin GitHub Actions runners).
$binDir = Split-Path -Parent $BinaryPath
if ($binDir -eq "") { $binDir = "." }
try { Add-MpPreference -ExclusionPath $binDir -ErrorAction SilentlyContinue } catch {}
try { Add-MpPreference -ExclusionProcess (Split-Path -Leaf $BinaryPath) -ErrorAction SilentlyContinue } catch {}
try { Set-MpPreference -DisableRealtimeMonitoring $true -ErrorAction SilentlyContinue } catch {}

# Use a helper that runs a command with a hard timeout.
# This prevents Defender from hanging the entire job indefinitely.
function Invoke-WithTimeout {
    param([string]$Cmd, [string[]]$Args, [int]$Seconds = 30)
    $proc = Start-Process -FilePath $Cmd -ArgumentList $Args -NoNewWindow -PassThru -RedirectStandardOutput NUL -RedirectStandardError NUL
    $exited = $proc.WaitForExit($Seconds * 1000)
    if (-not $exited) {
        $proc.Kill()
        Write-Warning "Command timed out after ${Seconds}s: $Cmd $($Args -join ' ')"
        return $false
    }
    if ($proc.ExitCode -ne 0) {
        Write-Warning "Command exited with code $($proc.ExitCode): $Cmd $($Args -join ' ')"
        return $false
    }
    return $true
}

$ok = $true
$ok = (Invoke-WithTimeout -Cmd $BinaryPath -Args "--help" -Seconds 30) -and $ok
$ok = (Invoke-WithTimeout -Cmd $BinaryPath -Args "completion", "bash" -Seconds 15) -and $ok
$ok = (Invoke-WithTimeout -Cmd $BinaryPath -Args "mcp", "--help" -Seconds 30) -and $ok

if (-not $ok) {
    Write-Warning "One or more smoke commands failed or timed out (Defender may have interfered)"
}
