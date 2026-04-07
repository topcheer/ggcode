param(
    [Parameter(Mandatory = $true)][string]$BinaryPath
)

$ErrorActionPreference = "Stop"

& $BinaryPath --help | Out-Null
& $BinaryPath completion bash | Out-Null
& $BinaryPath mcp --help | Out-Null
