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

& $wingetCreate show $PackageId | Out-Null
$packageExists = $LASTEXITCODE -eq 0

if (-not $packageExists) {
    Write-Warning "Package '$PackageId' does not exist in winget-pkgs yet. Skipping automated submission until the first manifest is bootstrapped manually."
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
# This handles the case where installer count changes (e.g. adding arm64
# to an existing x64-only manifest). wingetcreate new downloads the
# installers, detects metadata, and creates a complete manifest from
# scratch. The --token enables auto-fill from GitHub release metadata.
$allUrls = @($InstallerUrl)
if ($InstallerUrlArm64) {
    $allUrls += $InstallerUrlArm64
}

# Use wingetcreate new to generate a fresh manifest with all installers.
# This is non-interactive when all required fields can be auto-filled
# from GitHub release metadata.
$newArgs = @("new") + $allUrls + @("--version", $releaseVersion, "--token", $GitHubToken, "--out", "$PWD/new-manifest", "--no-open")

Write-Host "Generating new manifest with $($allUrls.Count) installer(s)..."
& $wingetCreate @newArgs

if ($LASTEXITCODE -ne 0) {
    Write-Warning "wingetcreate new also failed. This may require manual manifest update to add the new installer architecture."
    Write-Warning "The release assets are still available at the GitHub Release URL — only winget manifest submission failed."
    exit 0  # Don't fail the release — MSI files are already published
}

Write-Host "New manifest generated successfully."

# Submit the generated manifest
$submitArgs = @("submit", "$PWD/new-manifest", "--token", $GitHubToken, "--no-open")
& $wingetCreate @submitArgs

if ($LASTEXITCODE -ne 0) {
    Write-Warning "Manifest submission failed, but release assets are available. Manual review may be needed."
}

exit 0
