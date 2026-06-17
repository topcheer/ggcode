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

Invoke-WebRequest https://aka.ms/wingetcreate/latest -OutFile $wingetCreate

$env:WINGET_CREATE_GITHUB_TOKEN = $GitHubToken

# Check if package already exists in winget-pkgs
& $wingetCreate show $PackageId | Out-Null
$packageExists = $LASTEXITCODE -eq 0

if (-not $packageExists) {
    Write-Warning "Package '$PackageId' does not exist in winget-pkgs yet. Creating new manifest..."
    # For new packages, use wingetcreate new which downloads installers,
    # extracts metadata, and submits a PR to winget-pkgs.
    $allUrls = @($InstallerUrl)
    if ($InstallerUrlArm64) {
        $allUrls += $InstallerUrlArm64
    }
    & $wingetCreate new $allUrls --out "$PWD/new-manifest" --token $GitHubToken --no-open
    exit 0
}

# Build the URL list with architecture and scope overrides.
# Format: <url>|<arch>|<scope>
$urls = @("${InstallerUrl}|x64|user")
if ($InstallerUrlArm64) {
    $urls += "${InstallerUrlArm64}|arm64|user"
}

# --- Attempt 1: wingetcreate update (works when installer count matches) ---
$updateArgs = @("update", $PackageId, "--version", $releaseVersion, "--urls") + $urls + @("--submit", "--token", $GitHubToken, "--no-open")

Write-Host "Attempting wingetcreate update with $($urls.Count) installer(s)..."
& $wingetCreate @updateArgs

if ($LASTEXITCODE -eq 0) {
    Write-Host "wingetcreate update succeeded."
    exit 0
}

Write-Warning "wingetcreate update failed (likely installer count mismatch). Falling back to full manifest regeneration..."

# --- Attempt 2: Generate complete new manifest and submit ---
# wingetcreate new downloads all installers, extracts metadata
# (including version), and submits a PR to winget-pkgs.
# No --version flag needed — version is auto-detected from installer metadata.
$allUrls = @($InstallerUrl)
if ($InstallerUrlArm64) {
    $allUrls += $InstallerUrlArm64
}

Write-Host "Generating new manifest with $($allUrls.Count) installer(s)..."
& $wingetCreate new $allUrls --out "$PWD/new-manifest" --token $GitHubToken --no-open

if ($LASTEXITCODE -ne 0) {
    Write-Warning "wingetcreate new also failed. This may require manual manifest update to add the new installer architecture."
    Write-Warning "The release assets are still available at the GitHub Release URL — only winget manifest submission failed."
    exit 0  # Don't fail the release — MSI files are already published
}

Write-Host "New manifest generated and PR submitted successfully."
exit 0
