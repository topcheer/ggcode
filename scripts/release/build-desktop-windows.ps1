# Build ggcode-desktop for Windows (amd64) and a matching MSI installer.
param(
  [Parameter(Mandatory=$true)]
  [string]$Version,
  [Parameter(Mandatory=$true)]
  [string]$OutputDir
)

$ErrorActionPreference = "Stop"

$RootDir = Resolve-Path (Join-Path $PSScriptRoot "..\..")
$DesktopDir = Join-Path $RootDir "desktop\ggcode-desktop"
$WxsPath = Join-Path $RootDir ".github\packaging\windows\ggcode-desktop.wxs"
$PackageVersion = $Version -replace '^v',''
$Commit = if ($env:GGCODE_COMMIT) { $env:GGCODE_COMMIT } else { "" }
$BuildDate = if ($env:GGCODE_DATE) { $env:GGCODE_DATE } else { "" }
$UpgradeCode = "{CB2D6759-52A6-4C5E-8D56-FF21F3E3CE9D}"

# Resolve OutputDir to absolute path
$AbsOutputDir = if ([System.IO.Path]::IsPathRooted($OutputDir)) { $OutputDir } else { Join-Path $pwd $OutputDir }
New-Item -ItemType Directory -Force -Path $AbsOutputDir | Out-Null

$Ldflags = @(
  "-s", "-w",
  "-X", "github.com/topcheer/ggcode/internal/version.Version=$Version",
  "-X", "github.com/topcheer/ggcode/internal/version.Commit=$Commit",
  "-X", "github.com/topcheer/ggcode/internal/version.Date=$BuildDate",
  "-X", "main.Version=$Version"
) -join " "

Write-Host "=== Building ggcode-desktop for Windows (amd64) ==="
Write-Host "Output: $AbsOutputDir"

if (-not (Test-Path $WxsPath)) {
  throw "missing WiX source at $WxsPath"
}

$stageDir = Join-Path $AbsOutputDir "ggcode-desktop-msi-stage"
Remove-Item -Recurse -Force $stageDir -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path $stageDir | Out-Null

Push-Location $DesktopDir
  # Generate Windows resource file (icon + version info embedded in .exe)
  $winresExe = Get-Command go-winres -ErrorAction SilentlyContinue
  if (-not $winresExe) {
    Write-Host "Installing go-winres..."
    go install github.com/tc-hib/go-winres@latest
    $winresExe = Join-Path ($env:GOPATH ?? (Join-Path $env:HOME "go")) "bin" "go-winres$(if ($IsWindows -or $env:OS -eq 'Windows_NT') { '.exe' } else { '' })"
  }
  if (Test-Path $winresExe) {
    Write-Host "Generating Windows resource (.syso)..."
    & $winresExe simply --product-name "GGCode Desktop" --icon icon.png --arch amd64 --out winres 2>$null
    Write-Host "Generated .syso files"
  } else {
    Write-Host "WARNING: go-winres not found, .exe will have no embedded icon"
  }

  $env:CGO_ENABLED = "1"
  $env:GOOS = "windows"
  $env:GOARCH = "amd64"
  $outFile = Join-Path $AbsOutputDir "ggcode-desktop_${PackageVersion}_windows_amd64.exe"
  go build -tags goolm -ldflags $Ldflags -o $outFile .
  if ($LASTEXITCODE -ne 0) {
    throw "go build failed for desktop windows binary"
  }
  Write-Host "Built: $outFile"
Pop-Location

Copy-Item $outFile (Join-Path $stageDir "ggcode-desktop.exe")

$msiTarget = Join-Path $AbsOutputDir "ggcode-desktop_${PackageVersion}_windows_x64.msi"
& wix build `
  -d "Version=$PackageVersion" `
  -d "UpgradeCode=$UpgradeCode" `
  -d "SourceDir=$stageDir" `
  -arch x64 `
  -o $msiTarget `
  $WxsPath
if ($LASTEXITCODE -ne 0) {
  throw "wix build failed for desktop windows installer"
}
Write-Host "Built: $msiTarget"

Remove-Item -Recurse -Force $stageDir

Write-Host "=== Done ==="
