param(
    [Parameter(Mandatory = $true)][string]$BinaryPath
)

$ErrorActionPreference = "Stop"

# Exclude the binary's directory from Windows Defender real-time scanning.
# Unsigned binaries trigger Defender/SmartScreen on first run which can hang
# the process for 30+ minutes in CI.
$binDir = Split-Path -Parent $BinaryPath
if ($binDir -eq "") { $binDir = "." }
try {
    Add-MpPreference -ExclusionPath $binDir -ErrorAction SilentlyContinue
} catch {
    # Non-admin runner — ignore and hope for the best
}

& $BinaryPath --help | Out-Null
& $BinaryPath completion bash | Out-Null
& $BinaryPath mcp --help | Out-Null
