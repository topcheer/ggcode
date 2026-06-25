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
# All MSI installers are perUser (LocalAppData, no admin required)
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

# --- Step 1: Generate base manifest via wingetcreate update ---
# wingetcreate downloads each MSI, computes SHA256, extracts ProductCode/UpgradeCode,
# and preserves the manifest structure from the previous version.
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

# --- Step 2: Patch the generated installer YAML with missing top-level fields ---
# wingetcreate update --urls only populates per-installer fields. We need to add
# top-level ProductCode, AppsAndFeaturesEntries (Desktop) and Commands (CLI) to
# match the structure wingetbot validation expects.
Write-Host "Step 2: Patching installer YAML with top-level fields..."

$installerFile = Get-ChildItem -Path $manifestDir -Filter "*.installer.yaml" -Recurse | Select-Object -First 1
if (-not $installerFile) {
    Write-Warning "Could not find installer YAML."
    exit 0
}

$yaml = Get-Content -Path $installerFile.FullName -Raw
Write-Host "  Generated installer YAML is $($yaml.Length) bytes"

# Extract the first per-installer ProductCode (wingetcreate already extracted it from the MSI)
$productCodeMatch = [regex]::Match($yaml, "ProductCode:\s*['""]?(\{[0-9A-Fa-f-]+\})['""]?")
$productCode = if ($productCodeMatch.Success) { $productCodeMatch.Groups[1].Value } else { "" }
Write-Host "  Extracted ProductCode from installer entry: $productCode"

# Extract UpgradeCode if present
$upgradeCodeMatch = [regex]::Match($yaml, "UpgradeCode:\s*['""]?(\{[0-9A-Fa-f-]+\})['""]?")
$upgradeCode = if ($upgradeCodeMatch.Success) { $upgradeCodeMatch.Groups[1].Value } else { "" }
Write-Host "  Extracted UpgradeCode: $upgradeCode"

# Check if top-level ProductCode already exists (wingetcreate may add it in newer versions)
$hasTopLevelProductCode = $yaml -match "(?m)^ProductCode:"
$hasTopLevelAppsAndFeatures = $yaml -match "(?m)^AppsAndFeaturesEntries:"
$hasCommands = $yaml -match "(?m)^Commands:"

Write-Host "  Top-level ProductCode: $(if ($hasTopLevelProductCode) {'present'} else {'MISSING'})"
Write-Host "  Top-level AppsAndFeaturesEntries: $(if ($hasTopLevelAppsAndFeatures) {'present'} else {'MISSING'})"
Write-Host "  Commands: $(if ($hasCommands) {'present'} else {'MISSING'})"

# Determine display name and publisher
$displayName = if ($isCli) { "ggcode" } else { "GGCode Desktop" }
$publisher = "GG AI Studio"

$lines = $yaml -split "`n"
$result = @()
$insertedTopLevel = $false

foreach ($line in $lines) {
    # Insert top-level fields just before "Installers:" section
    if ($line -match "^Installers:" -and -not $insertedTopLevel) {
        # Add Commands for CLI if missing
        if ($isCli -and -not $hasCommands) {
            $result += "Commands:"
            $result += "- ggcode"
        }

        # Add top-level ProductCode and AppsAndFeaturesEntries for Desktop if missing
        if (-not $isCli) {
            if (-not $hasTopLevelProductCode -and $productCode) {
                $result += "ProductCode: '$productCode'"
            }
            if (-not $hasTopLevelAppsAndFeatures -and $productCode) {
                $result += "AppsAndFeaturesEntries:"
                $result += "- DisplayName: $displayName"
                $result += "  Publisher: $publisher"
                $result += "  ProductCode: '$productCode'"
                if ($upgradeCode) {
                    $result += "  UpgradeCode: '$upgradeCode'"
                }
                $result += "  InstallerType: wix"
            }
        }

        $insertedTopLevel = $true
    }

    $result += $line
}

$patchedYaml = ($result -join "`n")
Set-Content -Path $installerFile.FullName -Value $patchedYaml -NoNewline
Write-Host "  Patched installer YAML written ($(($patchedYaml -split "`n").Count) lines)"

# --- Step 3: Submit the manifest ---
$yamlDir = Split-Path -Parent $installerFile.FullName
Write-Host "Step 3: Submitting manifest from $yamlDir to winget-pkgs..."
& $wingetCreate submit $yamlDir --token $GitHubToken --no-open

if ($LASTEXITCODE -eq 0) {
    Write-Host "Manifest submitted successfully!"
} else {
    Write-Warning "Manifest submission failed (exit code $LASTEXITCODE)."
    Write-Host "  Dumping patched YAML for debugging:"
    Write-Host "  ---"
    Get-Content $installerFile.FullName | ForEach-Object { Write-Host "  $_" }
    Write-Host "  ---"
}

exit 0
