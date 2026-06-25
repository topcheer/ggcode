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

# Detect package type — CLI and Desktop have different manifest structures
$isCli = $PackageId -like "*-cli"
# Both CLI and Desktop MSI are perUser (LocalAppData, no admin required)
$scope = "user"
Write-Host "Package: $PackageId (CLI=$isCli, Scope=$scope)"

Invoke-WebRequest https://aka.ms/wingetcreate/latest -OutFile $wingetCreate

$env:WINGET_CREATE_GITHUB_TOKEN = $GitHubToken

# --- Idempotency: skip if a PR for this version already exists ---
Write-Host "Checking for existing PRs for $PackageId version $releaseVersion..."
try {
    $searchQuery = "repo:microsoft/winget-pkgs type:pr state:open $PackageId version $releaseVersion in:title"
    $searchResponse = Invoke-RestMethod -Uri "https://api.github.com/search/issues?q=$([uri]::EscapeDataString($searchQuery))" -Headers @{ Authorization = "token $GitHubToken"; Accept = "application/vnd.github+json" } -ErrorAction Stop
    if ($searchResponse.total_count -gt 0) {
        Write-Host "Found $($searchResponse.total_count) existing open PR(s) for $PackageId $releaseVersion. Skipping."
        foreach ($item in $searchResponse.items) {
            Write-Host "  PR #$($item.number): $($item.html_url)"
        }
        exit 0
    }
} catch {
    Write-Warning "PR search failed: $_. Proceeding with submission."
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

# --- Step 1: Generate base manifest from existing package ---
Write-Host "Step 1: Generating base manifest..."

# Build URL list — must match the number of installers in the existing manifest
$urls = @("${InstallerUrl}|x64|${scope}")
if ($InstallerUrlArm64) {
    $urls += "${InstallerUrlArm64}|arm64|${scope}"
}
Write-Host "  Installer URLs: $($urls -join ', ')"

& $wingetCreate update $PackageId --version $releaseVersion --urls $urls --out $manifestDir --token $GitHubToken

if ($LASTEXITCODE -ne 0) {
    Write-Warning "Base manifest generation failed."
    exit 0
}

# --- Step 2: Download installers and extract MSI properties ---
Write-Host "Step 2: Downloading installers and extracting MSI properties..."

$installers = @()

# Helper: extract ProductCode, UpgradeCode, DisplayName from MSI
function Get-MsiProperties {
    param([string]$MsiPath)

    $props = @{}
    try {
        $windowsInstaller = New-Object -ComObject WindowsInstaller.Installer
        $database = $windowsInstaller.OpenDatabase($MsiPath, 0)
        $view = $database.OpenView("SELECT `Property`, `Value` FROM Property WHERE `Property` IN ('ProductCode', 'UpgradeCode', 'ProductName', 'ProductVersion', 'Manufacturer')")
        $view.Execute()
        while ($true) {
            $record = $view.Fetch()
            if ($null -eq $record) { break }
            $props[$record.StringData(1)] = $record.StringData(2)
        }
    } catch {
        Write-Warning "Failed to extract MSI properties: $_"
    }
    return $props
}

# x64 installer
$x64Path = Join-Path $PWD "x64.msi"
Invoke-WebRequest -Uri $InstallerUrl -OutFile $x64Path -UseBasicParsing
$x64Hash = (Get-FileHash $x64Path -Algorithm SHA256).Hash.ToUpper()
$x64Props = Get-MsiProperties -MsiPath $x64Path
Write-Host "  x64 SHA256: $x64Hash"
Write-Host "  x64 ProductCode: $($x64Props['ProductCode'])"
Write-Host "  x64 UpgradeCode: $($x64Props['UpgradeCode'])"
$installers += @{
    Arch         = "x64"
    Url          = $InstallerUrl
    Sha256       = $x64Hash
    ProductCode  = $x64Props['ProductCode']
    UpgradeCode  = $x64Props['UpgradeCode']
}

# arm64 installer
if ($InstallerUrlArm64) {
    $arm64Path = Join-Path $PWD "arm64.msi"
    Invoke-WebRequest -Uri $InstallerUrlArm64 -OutFile $arm64Path -UseBasicParsing
    $arm64Hash = (Get-FileHash $arm64Path -Algorithm SHA256).Hash.ToUpper()
    $arm64Props = Get-MsiProperties -MsiPath $arm64Path
    Write-Host "  arm64 SHA256: $arm64Hash"
    Write-Host "  arm64 ProductCode: $($arm64Props['ProductCode'])"
    $installers += @{
        Arch         = "arm64"
        Url          = $InstallerUrlArm64
        Sha256       = $arm64Hash
        ProductCode  = $arm64Props['ProductCode']
        UpgradeCode  = $arm64Props['UpgradeCode']
    }
}

# --- Step 3: Extract shared metadata ---
$installerFile = Get-ChildItem -Path $manifestDir -Filter "*.installer.yaml" -Recurse | Select-Object -First 1
if (-not $installerFile) {
    Write-Warning "Could not find installer YAML template."
    exit 0
}

# Use first installer's metadata for top-level fields
$topProductCode = $installers[0].ProductCode
$topUpgradeCode = $installers[0].UpgradeCode
$displayName = if ($isCli) { "ggcode" } else { "GGCode Desktop" }
if ($x64Props['ProductName']) { $displayName = $x64Props['ProductName'] }
if (-not $isCli -and $displayName -eq "ggcode") { $displayName = "GGCode Desktop" }
$publisher = "GG AI Studio"
if ($x64Props['Manufacturer']) { $publisher = $x64Props['Manufacturer'] }

Write-Host "Step 3: ProductCode=$topProductCode UpgradeCode=$topUpgradeCode DisplayName=$displayName Publisher=$publisher"

# --- Step 4: Write complete installer YAML ---
Write-Host "Step 4: Writing complete installer YAML..."

# Build top-level fields — CLI and Desktop have different required properties
$sharedFields = @"
PackageIdentifier: $PackageId
PackageVersion: $releaseVersion
Platform:
- Windows.Desktop
InstallerType: wix
Scope: $scope
InstallModes:
- interactive
- silent
- silentWithProgress
UpgradeBehavior: install
"@

if ($isCli) {
    # CLI: machine scope, has Commands, no MinimumOSVersion/InstallerSwitches
    $sharedFields += @"
Commands:
- ggcode
"@
} else {
    # Desktop: user scope, has MinimumOSVersion, InstallerSwitches, ProductCode, AppsAndFeaturesEntries
    $sharedFields += @"
MinimumOSVersion: 10.0.17763.0
InstallerSwitches:
  Silent: /qn
  SilentWithProgress: /qb
ProductCode: '$topProductCode'
AppsAndFeaturesEntries:
- DisplayName: $displayName
  Publisher: $publisher
  ProductCode: '$topProductCode'
  UpgradeCode: '$topUpgradeCode'
  InstallerType: wix
"@
}

$yaml = @"
# Created using wingetcreate
# yaml-language-server: `$schema=https://aka.ms/winget-manifest.installer.1.12.0.schema.json

$sharedFields
Installers:
"@

foreach ($inst in $installers) {
    $yaml += @"

- Architecture: $($inst.Arch)
  InstallerUrl: $($inst.Url)
  InstallerSha256: $($inst.Sha256)
  InstallerType: wix
  Scope: $scope
  ProductCode: '$($inst.ProductCode)'
  AppsAndFeaturesEntries:
  - DisplayName: $displayName
    Publisher: $publisher
    ProductCode: '$($inst.ProductCode)'
    UpgradeCode: '$($inst.UpgradeCode)'
    InstallerType: wix
"@
}

$yaml += @"

ManifestType: installer
ManifestVersion: 1.12.0
ReleaseDate: $(Get-Date -Format "yyyy-MM-dd")
"@

Set-Content -Path $installerFile.FullName -Value $yaml -NoNewline
Write-Host "  Wrote complete installer YAML with $($installers.Count) installer(s)"

# --- Step 5: Submit the manifest ---
$yamlDir = Split-Path -Parent $installerFile.FullName
Write-Host "Step 5: Submitting manifest from $yamlDir to winget-pkgs..."
& $wingetCreate submit $yamlDir --token $GitHubToken --no-open

if ($LASTEXITCODE -eq 0) {
    Write-Host "Manifest submitted successfully!"
} else {
    Write-Warning "Manifest submission failed. The release assets are still available at the GitHub Release URL."
}

exit 0
