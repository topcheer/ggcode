param(
    [Parameter(Mandatory = $true)][string]$PackageId,
    [Parameter(Mandatory = $true)][string]$Version,
    [Parameter(Mandatory = $true)][string]$InstallerUrl,
    [Parameter(Mandatory = $false)][string]$InstallerUrlArm64,
    [Parameter(Mandatory = $true)][string]$GitHubToken
)

$ErrorActionPreference = "Stop"

$releaseVersion = $Version.TrimStart("v")
$wingetCreate = Join-Path $PWD "wingetcreate.exe"
$manifestDir = Join-Path $PWD "manifest-output"

Invoke-WebRequest https://aka.ms/wingetcreate/latest -OutFile $wingetCreate

$env:WINGET_CREATE_GITHUB_TOKEN = $GitHubToken

# --- Idempotency: skip if a PR for this version already exists ---
Write-Host "Checking for existing PRs for $PackageId version $releaseVersion..."
$existingPRs = gh search prs --repo microsoft/winget-pkgs "$PackageId version $releaseVersion" --state open --json number,url 2>$null | ConvertFrom-Json
if ($existingPRs -and $existingPRs.Count -gt 0) {
    Write-Host "Found $($existingPRs.Count) existing open PR(s) for $PackageId $releaseVersion. Skipping."
    foreach ($pr in $existingPRs) {
        Write-Host "  PR #$($pr.number): $($pr.url)"
    }
    exit 0
}

# Check if package already exists
& $wingetCreate show $PackageId | Out-Null
$packageExists = $LASTEXITCODE -eq 0

if (-not $packageExists) {
    Write-Host "Package '$PackageId' does not exist yet. Creating new manifest..."
    $allUrls = @($InstallerUrl)
    if ($InstallerUrlArm64) { $allUrls += $InstallerUrlArm64 }
    & $wingetCreate new $allUrls --token $GitHubToken --no-open
    exit 0
}

# --- Determine existing installer count from latest manifest ---
$existingManifest = & $wingetCreate show $PackageId 2>&1 | Out-String
$existingCount = ([regex]::Matches($existingManifest, "InstallerUrl:")).Count
if ($existingCount -eq 0) { $existingCount = 1 }

$desiredUrls = @("${InstallerUrl}|x64|user")
if ($InstallerUrlArm64) {
    $desiredUrls += "${InstallerUrlArm64}|arm64|user"
}

Write-Host "Existing installers: $existingCount, Desired installers: $($desiredUrls.Count)"

if ($desiredUrls.Count -eq $existingCount) {
    # --- Same count: direct update + submit ---
    Write-Host "Installer count matches. Running wingetcreate update --submit..."
    & $wingetCreate update $PackageId --version $releaseVersion --urls $desiredUrls --submit --token $GitHubToken --no-open
    if ($LASTEXITCODE -eq 0) {
        Write-Host "wingetcreate update succeeded."
        exit 0
    }
    Write-Warning "wingetcreate update failed despite matching count."
    exit 0
}

# --- Different count: generate complete installer YAML from scratch ---
Write-Host "Installer count changed ($existingCount -> $($desiredUrls.Count)). Generating complete manifest..."

# Step 1: Generate base manifest with existing URL count (for locale + version YAML)
$matchingUrls = $desiredUrls[0..([Math]::Min($existingCount, $desiredUrls.Count) - 1)]
Write-Host "Step 1: Generating base manifest with $existingCount installer(s)..."
& $wingetCreate update $PackageId --version $releaseVersion --urls $matchingUrls --out $manifestDir --token $GitHubToken

if ($LASTEXITCODE -ne 0) {
    Write-Warning "Base manifest generation failed."
    exit 0
}

# Step 2: Download all installers, compute SHA256
Write-Host "Step 2: Computing installer hashes..."

$installers = @()

# x64 installer
$x64Path = Join-Path $PWD "x64.msi"
Invoke-WebRequest -Uri $InstallerUrl -OutFile $x64Path -UseBasicParsing
$x64Hash = (Get-FileHash $x64Path -Algorithm SHA256).Hash.ToUpper()
Write-Host "  x64 SHA256: $x64Hash"
$installers += @{
    Arch        = "x64"
    Url         = $InstallerUrl
    Sha256      = $x64Hash
}

# arm64 installer
if ($InstallerUrlArm64) {
    $arm64Path = Join-Path $PWD "arm64.msi"
    Invoke-WebRequest -Uri $InstallerUrlArm64 -OutFile $arm64Path -UseBasicParsing
    $arm64Hash = (Get-FileHash $arm64Path -Algorithm SHA256).Hash.ToUpper()
    Write-Host "  arm64 SHA256: $arm64Hash"
    $installers += @{
        Arch   = "arm64"
        Url    = $InstallerUrlArm64
        Sha256 = $arm64Hash
    }
}

# Step 3: Write complete installer YAML from scratch
Write-Host "Step 3: Writing complete installer YAML..."

$installerFile = Get-ChildItem -Path $manifestDir -Filter "*.installer.yaml" -Recurse | Select-Object -First 1
if (-not $installerFile) {
    Write-Warning "Could not find installer YAML template."
    exit 0
}

# Extract shared metadata from the base manifest (locale info, etc.)
$baseYaml = Get-Content $installerFile.FullName -Raw

# Parse top-level fields we need to preserve
$productCode = ""
if ($baseYaml -match "ProductCode:\s*'(\{[^}]+\})'") { $productCode = $matches[1] }
$upgradeCode = ""
if ($baseYaml -match "UpgradeCode:\s*'(\{[^}]+\})'") { $upgradeCode = $matches[1] }
$displayName = "GGCode Desktop"
if ($baseYaml -match "DisplayName:\s*(.+)") { $displayName = $matches[1].Trim() }
$publisher = "GG AI Studio"
if ($baseYaml -match "Publisher:\s*(.+)") { $publisher = $matches[1].Trim() }

# Build the complete installer YAML
$yaml = @"
# Created using wingetcreate 1.12.8.0
# yaml-language-server: `$schema=https://aka.ms/winget-manifest.installer.1.12.0.schema.json

PackageIdentifier: $PackageId
PackageVersion: $releaseVersion
Platform:
- Windows.Desktop
MinimumOSVersion: 10.0.17763.0
InstallerType: wix
Scope: user
InstallModes:
- interactive
- silent
- silentWithProgress
InstallerSwitches:
  Silent: /qn
  SilentWithProgress: /qb
UpgradeBehavior: install
Installers:
"@

foreach ($inst in $installers) {
    $yaml += @"

- Architecture: $($inst.Arch)
  InstallerUrl: $($inst.Url)
  InstallerSha256: $($inst.Sha256)
  InstallerType: wix
  Scope: user
  ProductCode: '$productCode'
  AppsAndFeaturesEntries:
  - DisplayName: $displayName
    Publisher: $publisher
    ProductCode: '$productCode'
    UpgradeCode: '$upgradeCode'
"@
}

$yaml += @"

ManifestType: installer
ManifestVersion: 1.12.0
ReleaseDate: $(Get-Date -Format "yyyy-MM-dd")
"@

Set-Content -Path $installerFile.FullName -Value $yaml -NoNewline
Write-Host "  Wrote complete installer YAML with $($installers.Count) installer(s)"

# Step 4: Submit the modified manifest
$yamlDir = Split-Path -Parent $installerFile.FullName
Write-Host "Step 4: Submitting manifest from $yamlDir to winget-pkgs..."
& $wingetCreate submit $yamlDir --token $GitHubToken --no-open

if ($LASTEXITCODE -eq 0) {
    Write-Host "Manifest submitted successfully!"
} else {
    Write-Warning "Manifest submission failed. The release assets are still available at the GitHub Release URL."
}

exit 0
