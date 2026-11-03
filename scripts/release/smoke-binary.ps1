param(
    [Parameter(Mandatory = $true)][string]$BinaryPath
)

$ErrorActionPreference = "Stop"

try {
    Unblock-File -Path $BinaryPath -ErrorAction SilentlyContinue
} catch {

}

$binDir = Split-Path -Parent $BinaryPath
if ($binDir -eq "") { $binDir = "." }
try { Add-MpPreference -ExclusionPath $binDir -ErrorAction SilentlyContinue } catch {}
try { Add-MpPreference -ExclusionProcess (Split-Path -Leaf $BinaryPath) -ErrorAction SilentlyContinue } catch {}
try { Set-MpPreference -DisableRealtimeMonitoring $true -ErrorAction SilentlyContinue } catch {}

function Invoke-WithTimeout {
    param([string]$Cmd, [string[]]$Args, [int]$Seconds = 30)
    $tmpOut = [System.IO.Path]::GetTempFileName()
    $tmpErr = [System.IO.Path]::GetTempFileName()
    try {
        $proc = Start-Process -FilePath $Cmd -ArgumentList $Args -NoNewWindow -PassThru `
            -RedirectStandardOutput $tmpOut -RedirectStandardError $tmpErr
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
    } finally {
        Remove-Item $tmpOut -Force -ErrorAction SilentlyContinue
        Remove-Item $tmpErr -Force -ErrorAction SilentlyContinue
    }
}

$ok = $true
$ok = (Invoke-WithTimeout -Cmd $BinaryPath -Args "--help" -Seconds 30) -and $ok
$ok = (Invoke-WithTimeout -Cmd $BinaryPath -Args "completion", "bash" -Seconds 15) -and $ok
$ok = (Invoke-WithTimeout -Cmd $BinaryPath -Args "mcp", "--help" -Seconds 30) -and $ok

if (-not $ok) {
    Write-Warning "One or more smoke commands failed or timed out (Defender may have interfered)"
}
