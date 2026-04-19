param(
    [Parameter(Mandatory = $true)][string]$BinaryPath
)

$ErrorActionPreference = "Stop"

# Unblock the downloaded binary to remove the Zone.Identifier alternate data stream.
# This prevents Windows Defender / SmartScreen from treating the file as "downloaded
# from the internet" and hanging the process for 30+ minutes during first-run scanning.
try {
    Unblock-File -Path $BinaryPath -ErrorAction SilentlyContinue
} catch {
    # May not exist — ignore
}

# Also try Defender exclusions (best-effort on non-admin runners).
$binDir = Split-Path -Parent $BinaryPath
if ($binDir -eq "") { $binDir = "." }
try { Add-MpPreference -ExclusionPath $binDir -ErrorAction SilentlyContinue } catch {}
try { Set-MpPreference -DisableRealtimeMonitoring $true -ErrorAction SilentlyContinue } catch {}

& $BinaryPath --help | Out-Null
& $BinaryPath completion bash | Out-Null
& $BinaryPath mcp --help | Out-Null
