param(
    [Parameter(Mandatory = $true)][string]$BinaryPath
)

$ErrorActionPreference = "Stop"

# Exclude binary directory from Windows Defender real-time scanning.
# Unsigned binaries trigger Defender/SmartScreen on first run which can hang
# the process for 30+ minutes in CI.
$binDir = Split-Path -Parent $BinaryPath
if ($binDir -eq "") { $binDir = "." }
try {
    Add-MpPreference -ExclusionPath $binDir -ErrorAction SilentlyContinue
} catch {
    # Non-admin runner — ignore
}

# Also disable real-time monitoring for the duration of this script if possible.
try {
    Set-MpPreference -DisableRealtimeMonitoring $true -ErrorAction SilentlyContinue
} catch {
    # Non-admin — ignore
}

& $BinaryPath --help | Out-Null
& $BinaryPath completion bash | Out-Null
& $BinaryPath mcp --help | Out-Null
