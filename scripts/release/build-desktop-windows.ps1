# Build ggcode-desktop for Windows (amd64)
param(
  [Parameter(Mandatory=$true)]
  [string]$Version,
  [Parameter(Mandatory=$true)]
  [string]$OutputDir
)

$ErrorActionPreference = "Stop"

$RootDir = Resolve-Path (Join-Path $PSScriptRoot "..\..")
$DesktopDir = Join-Path $RootDir "desktop\ggcode-desktop"
$PackageVersion = $Version -replace '^v',''
$Commit = if ($env:GGCODE_COMMIT) { $env:GGCODE_COMMIT } else { "" }
$BuildDate = if ($env:GGCODE_DATE) { $env:GGCODE_DATE } else { "" }

New-Item -ItemType Directory -Force -Path $OutputDir | Out-Null

$Ldflags = @(
  "-s", "-w",
  "-X", "github.com/topcheer/ggcode/internal/version.Version=$Version",
  "-X", "github.com/topcheer/ggcode/internal/version.Commit=$Commit",
  "-X", "github.com/topcheer/ggcode/internal/version.Date=$BuildDate"
) -join " "

Write-Host "=== Building ggcode-desktop for Windows (amd64) ==="

Push-Location $DesktopDir
  $env:CGO_ENABLED = "1"
  $env:GOOS = "windows"
  $env:GOARCH = "amd64"
  go build -tags goolm -ldflags $Ldflags -o (Join-Path $OutputDir "ggcode-desktop_${PackageVersion}_windows_amd64.exe") .
Pop-Location

Write-Host "=== Done ==="
