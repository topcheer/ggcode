# Build GGCode Desktop MSIX packages for Microsoft Store submission.
# Runs on windows-latest (Windows SDK makeappx.exe available).
# Per arch (x64/arm64):
#   - wails build -> build\bin\GGCode-Desktop.exe
#   - derive Assets/*.png from build/appicon.png (System.Drawing)
#   - makeappx pack -> ggcode-desktop_<ver>_windows_<arch>.msix (Store re-signs)
# Identity placeholders are injected from parameters (values come from
# Partner Center -> App management -> App identity).
param(
  [Parameter(Mandatory=$true)][string]$Version,        # e.g. 1.3.268.0 (four parts)
  [Parameter(Mandatory=$true)][string]$OutputDir,
  [Parameter(Mandatory=$true)][string]$IdentityName,   # Package/Identity Name
  [Parameter(Mandatory=$true)][string]$Publisher,      # CN=... publisher id
  [string]$Archs = "x64,arm64"                         # comma separated
)

$ErrorActionPreference = "Stop"

$RootDir = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$WailsDir = Join-Path $RootDir "desktop\ggcode-desktop-wails"
$ManifestTemplate = Join-Path $RootDir ".github\packaging\windows\ggcode-desktop-msix.appxmanifest"
$PackageVersion = $Version.TrimStart("v")
$AbsOutputDir = if ([System.IO.Path]::IsPathRooted($OutputDir)) { $OutputDir } else { Join-Path $pwd $OutputDir }
New-Item -ItemType Directory -Force -Path $AbsOutputDir | Out-Null

$Commit = if ($env:GGCODE_COMMIT) { $env:GGCODE_COMMIT } else { (git -C $RootDir rev-parse --short HEAD) }
$BuildDate = if ($env:GGCODE_DATE) { $env:GGCODE_DATE } else { (Get-Date -Format "yyyy-MM-dd") }

# Validate four-part version (MSIX requirement)
if ($PackageVersion -notmatch '^\d+\.\d+\.\d+\.\d+$') {
  throw "MSIX Version must be four-part (e.g. 1.3.268.0), got: $PackageVersion"
}

# Locate makeappx from Windows SDK
$makeappx = Get-ChildItem "${env:ProgramFiles(x86)}\Windows Kits\10\bin\*\x64\makeappx.exe" -ErrorAction SilentlyContinue |
  Sort-Object FullName -Descending | Select-Object -First 1 -ExpandProperty FullName
if (-not $makeappx) { throw "makeappx.exe not found (Windows SDK required)" }
Write-Host "Using makeappx: $makeappx"

$Ldflags = @(
  "-s","-w",
  "-X","github.com/topcheer/ggcode/internal/version.Version=$Version",
  "-X","github.com/topcheer/ggcode/internal/version.Commit=$Commit",
  "-X","github.com/topcheer/ggcode/internal/version.Date=$BuildDate"
) -join " "

$wailsExe = Get-Command wails -ErrorAction SilentlyContinue
if (-not $wailsExe) {
  go install github.com/wailsapp/wails/v2/cmd/wails@latest
  $wailsExe = Get-Command wails -ErrorAction SilentlyContinue
  if (-not $wailsExe) { throw "wails CLI not found after install" }
}

Add-Type -AssemblyName System.Drawing

function New-StoreAsset([System.Drawing.Bitmap]$src, [string]$path, [int]$w, [int]$h) {
  $bmp = New-Object System.Drawing.Bitmap($w, $h)
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $g.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
  $g.DrawImage($src, 0, 0, $w, $h)
  $g.Dispose()
  $bmp.Save($path, [System.Drawing.Imaging.ImageFormat]::Png)
  $bmp.Dispose()
}

foreach ($arch in ($Archs -split ",")) {
  $arch = $arch.Trim()
  $goArch = if ($arch -eq "x64") { "amd64" } else { "arm64" }
  $stageDir = Join-Path $AbsOutputDir "ggcode-desktop-msix-stage-$arch"
  Remove-Item -Recurse -Force $stageDir -ErrorAction SilentlyContinue
  $assetsDir = Join-Path $stageDir "Assets"
  New-Item -ItemType Directory -Force -Path $assetsDir | Out-Null

  Write-Host "=== Building $arch ($goArch) ==="

  # 1. wails build
  Push-Location $WailsDir
  try {
    $env:CGO_ENABLED = "1"; $env:GOOS = "windows"; $env:GOARCH = $goArch
    wails build -tags goolm -ldflags $Ldflags -platform "windows/$goArch" -clean -skipbindings
    if ($LASTEXITCODE -ne 0) { throw "wails build failed for $goArch" }
    $builtExe = Join-Path $WailsDir "build\bin\GGCode-Desktop.exe"
    if (-not (Test-Path $builtExe)) { throw "wails output missing: $builtExe" }
    Copy-Item $builtExe (Join-Path $stageDir "GGCode-Desktop.exe")
  } finally { Pop-Location }

  # 2. derive store assets from appicon
  $src = New-Object System.Drawing.Bitmap((Join-Path $WailsDir "build\appicon.png"))
  try {
    New-StoreAsset $src (Join-Path $assetsDir "Square44x44Logo.png") 44 44
    New-StoreAsset $src (Join-Path $assetsDir "Square150x150Logo.png") 150 150
    New-StoreAsset $src (Join-Path $assetsDir "Wide310x150Logo.png") 310 150
    New-StoreAsset $src (Join-Path $assetsDir "Square310x310Logo.png") 310 310
    New-StoreAsset $src (Join-Path $assetsDir "SplashScreen.png") 620 300
    New-StoreAsset $src (Join-Path $assetsDir "StoreLogo.png") 100 100
  } finally { $src.Dispose() }

  # 3. render manifest
  $manifest = (Get-Content $ManifestTemplate -Raw) `
    -replace '\{\{IDENTITY_NAME\}\}', $IdentityName `
    -replace '\{\{PUBLISHER\}\}', ($Publisher -replace '\\', '\\') `
    -replace '\{\{VERSION\}\}', $PackageVersion `
    -replace '\{\{ARCH\}\}', $arch
  $manifestPath = Join-Path $stageDir "AppxManifest.xml"
  [System.IO.File]::WriteAllText($manifestPath, $manifest)

  # 4. mapping file + makeappx
  $mapping = Join-Path $stageDir "mapping.txt"
  $lines = @(
    "[Files]"
    "`"$((Join-Path $stageDir 'GGCode-Desktop.exe'))`" `"$((Join-Path $stageDir 'GGCode-Desktop.exe'))`""
    "`"$manifestPath`" `"AppxManifest.xml`""
    "`"$assetsDir\Square44x44Logo.png`" `"Assets\Square44x44Logo.png`""
    "`"$assetsDir\Square150x150Logo.png`" `"Assets\Square150x150Logo.png`""
    "`"$assetsDir\Wide310x150Logo.png`" `"Assets\Wide310x150Logo.png`""
    "`"$assetsDir\Square310x310Logo.png`" `"Assets\Square310x310Logo.png`""
    "`"$assetsDir\SplashScreen.png`" `"Assets\SplashScreen.png`""
    "`"$assetsDir\StoreLogo.png`" `"Assets\StoreLogo.png`""
  )
  Set-Content -Path $mapping -Value ($lines -join "`r`n") -Encoding ASCII

  $outMsix = Join-Path $AbsOutputDir "ggcode-desktop_${PackageVersion}_windows_${arch}.msix"
  & $makeappx pack /m $manifestPath /f $mapping /p $outMsix /v
  if ($LASTEXITCODE -ne 0) { throw "makeappx failed for $arch" }
  Write-Host "Built MSIX: $outMsix"

  Remove-Item -Recurse -Force $stageDir -ErrorAction SilentlyContinue
}

Write-Host "=== All MSIX packages built under $AbsOutputDir ==="
