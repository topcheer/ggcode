
param(
  [Parameter(Mandatory=$true)]
  [string]$Version,
  [Parameter(Mandatory=$true)]
  [string]$OutputDir
)

$ErrorActionPreference = "Stop"

$RootDir = Resolve-Path (Join-Path $PSScriptRoot "..\..")
$WailsDir = Join-Path $RootDir "desktop\ggcode-desktop-wails"
$WxsMachinePath = Join-Path $RootDir ".github\packaging\windows\ggcode-desktop.wxs"
$WxsUserPath = Join-Path $RootDir ".github\packaging\windows\ggcode-desktop-user.wxs"
$PackageVersion = $Version -replace '^v',''
$Commit = if ($env:GGCODE_COMMIT) { $env:GGCODE_COMMIT } else { "" }
$BuildDate = if ($env:GGCODE_DATE) { $env:GGCODE_DATE } else { "" }
$UpgradeCode = "{CB2D6759-52A6-4C5E-8D56-FF21F3E3CE9D}"

$AbsOutputDir = if ([System.IO.Path]::IsPathRooted($OutputDir)) { $OutputDir } else { Join-Path $pwd $OutputDir }
New-Item -ItemType Directory -Force -Path $AbsOutputDir | Out-Null

$Ldflags = @(
  "-s", "-w",
  "-X", "github.com/topcheer/ggcode/internal/version.Version=$Version",
  "-X", "github.com/topcheer/ggcode/internal/version.Commit=$Commit",
  "-X", "github.com/topcheer/ggcode/internal/version.Date=$BuildDate"
) -join " "

Write-Host "=== Building GGCode Desktop (Wails) for Windows (amd64 + arm64) ==="
Write-Host "Output: $AbsOutputDir"

if (-not (Test-Path $WxsMachinePath)) {
  throw "missing WiX source at $WxsMachinePath"
}
if (-not (Test-Path $WxsUserPath)) {
  throw "missing WiX source at $WxsUserPath"
}

$wailsExe = Get-Command wails -ErrorAction SilentlyContinue
if (-not $wailsExe) {
  Write-Host "Installing Wails CLI..."
  go install github.com/wailsapp/wails/v2/cmd/wails@latest
  $wailsExe = Get-Command wails -ErrorAction SilentlyContinue
  if (-not $wailsExe) {
    throw "wails CLI not found after install"
  }
}

$wailsJson = Get-Content (Join-Path $WailsDir "wails.json") -Raw | ConvertFrom-Json
$wailsJson.info.productVersion = $PackageVersion
$wailsJson | ConvertTo-Json -Depth 10 | Set-Content (Join-Path $WailsDir "wails.json")

$builds = @(
  @{ GoArch = "amd64"; WixArch = "x64";   Suffix = "x64"   },
  @{ GoArch = "arm64"; WixArch = "arm64"; Suffix = "arm64" }
)

foreach ($build in $builds) {
  $stageDir = Join-Path $AbsOutputDir "ggcode-desktop-msi-stage-$($build.Suffix)"
  Remove-Item -Recurse -Force $stageDir -ErrorAction SilentlyContinue
  New-Item -ItemType Directory -Force -Path $stageDir | Out-Null

  Write-Host ""
  Write-Host "--- Building $($build.GoArch) ---"

  Push-Location $WailsDir
    $env:CGO_ENABLED = "1"
    $env:GOOS = "windows"
    $env:GOARCH = $build.GoArch

    wails build -tags goolm -ldflags $Ldflags -platform "windows/$($build.GoArch)" -clean -skipbindings
    if ($LASTEXITCODE -ne 0) {
      throw "wails build failed for desktop windows $($build.GoArch) binary"
    }

    $builtExe = Join-Path $WailsDir "build\bin\GGCode-Desktop.exe"
    if (-not (Test-Path $builtExe)) {
      throw "Wails build output not found at $builtExe"
    }

    $outFile = Join-Path $AbsOutputDir "ggcode-desktop_${PackageVersion}_windows_$($build.GoArch).exe"
    Copy-Item $builtExe $outFile
    Write-Host "Built exe: $outFile"
  Pop-Location

  Copy-Item $outFile (Join-Path $stageDir "ggcode-desktop.exe")

  $msiUserTarget = Join-Path $AbsOutputDir "ggcode-desktop_${PackageVersion}_windows_$($build.Suffix).msi"
  & wix build `
    -d "Version=$PackageVersion" `
    -d "SourceDir=$stageDir" `
    -arch $build.WixArch `
    -o $msiUserTarget `
    $WxsUserPath
  if ($LASTEXITCODE -ne 0) {
    throw "wix build failed for desktop windows $($build.GoArch) perUser installer"
  }
  Write-Host "Built perUser MSI: $msiUserTarget"

  $msiMachineTarget = Join-Path $AbsOutputDir "ggcode-desktop_${PackageVersion}_windows_$($build.Suffix)_machine.msi"
  & wix build `
    -d "Version=$PackageVersion" `
    -d "UpgradeCode=$UpgradeCode" `
    -d "SourceDir=$stageDir" `
    -arch $build.WixArch `
    -o $msiMachineTarget `
    $WxsMachinePath
  if ($LASTEXITCODE -ne 0) {
    throw "wix build failed for desktop windows $($build.GoArch) perMachine installer"
  }
  Write-Host "Built perMachine MSI: $msiMachineTarget"

  Remove-Item -Recurse -Force $stageDir
}

Write-Host ""
Write-Host "=== Done ==="
