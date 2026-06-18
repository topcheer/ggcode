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
if ($existingCount -eq 0) { $existingCount = 1 }  # fallback

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

# --- Different count: need to add/remove installer entries ---
Write-Host "Installer count changed ($existingCount -> $($desiredUrls.Count)). Using multi-step approach..."

# Step 1: Update with only the URLs that match existing count (x64 first)
$matchingUrls = $desiredUrls[0..([Math]::Min($existingCount, $desiredUrls.Count) - 1)]
Write-Host "Step 1: Generating base manifest with $existingCount installer(s)..."
& $wingetCreate update $PackageId --version $releaseVersion --urls $matchingUrls --out $manifestDir --token $GitHubToken

if ($LASTEXITCODE -ne 0) {
    Write-Warning "Base manifest generation failed."
    exit 0
}

# Step 2: If we have more installers than existing, add the extra entries
if ($desiredUrls.Count -gt $existingCount -and $InstallerUrlArm64) {
    Write-Host "Step 2: Adding arm64 installer entry to manifest..."

    # Download arm64 installer to get its hash
    $arm64InstallerPath = Join-Path $PWD "arm64.msi"
    Write-Host "  Downloading arm64 MSI for hashing..."
    Invoke-WebRequest -Uri $InstallerUrlArm64 -OutFile $arm64InstallerPath -UseBasicParsing
    $arm64Hash = (Get-FileHash $arm64InstallerPath -Algorithm SHA256).Hash.ToLower()
    Write-Host "  arm64 SHA256: $arm64Hash"

    # Find the installer YAML file (saved in nested dirs like manifests/g/gg/ai/<id>/<version>/)
    $installerFile = Get-ChildItem -Path $manifestDir -Filter "*.installer.yaml" -Recurse | Select-Object -First 1
    if (-not $installerFile) {
        Write-Warning "Could not find installer YAML to modify."
        exit 0
    }

    $yaml = Get-Content $installerFile.FullName -Raw

    # Extract architecture from the URL or default to arm64
    $arch = "arm64"

    # Find the last Installer block and duplicate it with new values
    # The installer YAML looks like:
    # - Architecture: x64
    #   InstallerUrl: https://...
    #   InstallerSha256: ABC...
    #   InstallerType: wix
    #   Scope: user
    #   ProductCode: '{...}'
    #   InstallerSwitches:
    #     Silent: /qn
    #     SilentWithProgress: /qb

    # Parse existing installer entry to get ProductCode pattern and InstallerType
    $installerType = "wix"
    if ($yaml -match "InstallerType:\s*(\w+)") { $installerType = $matches[1] }

    # Generate a new ProductCode GUID for arm64
    $newGuid = [System.Guid]::NewGuid().ToString().ToUpper()
    $productCode = "{$newGuid}"

    # Build the new installer entry by cloning the existing installer section.
    # The installer YAML structure is:
    #   Installers:
    #   - Architecture: x64
    #     InstallerUrl: ...
    #     AppsAndFeaturesEntries:
    #     - DisplayVersion: ...
    #   ManifestType: installer
    #
    # We split at ManifestType to get the full installer block, clone it,
    # modify values, and reassemble. This avoids fragile per-entry regex.
    $manifestTypeIdx = $yaml.IndexOf("ManifestType:")
    if ($manifestTypeIdx -lt 0) {
        Write-Warning "Could not find ManifestType in YAML."
        exit 0
    }

    $beforeManifest = $yaml.Substring(0, $manifestTypeIdx)
    $fromManifest = $yaml.Substring($manifestTypeIdx)

    # Extract the installer entry (everything from "- Architecture:" to ManifestType)
    $archIdx = $beforeManifest.IndexOf("- Architecture:")
    if ($archIdx -lt 0) {
        Write-Warning "Could not find any installer entry in YAML."
        exit 0
    }

    $installerBlock = $beforeManifest.Substring($archIdx)

    # Clone the installer block and modify for arm64
    $arm64Entry = $installerBlock.TrimEnd()
    $arm64Entry = $arm64Entry `
        -replace 'Architecture:\s*\w+', "Architecture: $arch" `
        -replace 'InstallerUrl:\s*.+', "InstallerUrl: $InstallerUrlArm64" `
        -replace 'InstallerSha256:\s*[A-Fa-f0-9]+', "InstallerSha256: $arm64Hash"

    # Replace ProductCode GUIDs with new ones for arm64
    $newGuid = [System.Guid]::NewGuid().ToString().ToUpper()
    $arm64Entry = $arm64Entry -replace "ProductCode:\s*'\{[^}]+\}'", "ProductCode: '{$newGuid}'"

    # Reassemble: original block + new arm64 entry + ManifestType section
    $yaml = $beforeManifest + $arm64Entry + "`n`n" + $fromManifest

    Set-Content -Path $installerFile.FullName -Value $yaml -NoNewline
    Write-Host "  Added arm64 installer entry to $($installerFile.Name)"
}

# Step 3: Submit the modified manifest
# wingetcreate submit expects the directory containing the YAML files directly,
# not the top-level output directory with nested manifest structure.
$yamlDir = Split-Path -Parent $installerFile.FullName
Write-Host "Step 3: Submitting manifest from $yamlDir to winget-pkgs..."
& $wingetCreate submit $yamlDir --token $GitHubToken --no-open

if ($LASTEXITCODE -eq 0) {
    Write-Host "Manifest submitted successfully!"
} else {
    Write-Warning "Manifest submission failed. The release assets are still available at the GitHub Release URL."
}

exit 0
