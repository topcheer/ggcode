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

$isCli = $PackageId -like "*-cli"

$scope = "user"
Write-Host "Package: $PackageId (CLI=$isCli, Scope=$scope)"

Invoke-WebRequest https://aka.ms/wingetcreate/latest -OutFile $wingetCreate

$env:WINGET_CREATE_GITHUB_TOKEN = $GitHubToken

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

Write-Host "Checking for stale PRs (older versions of $PackageId)..."
try {
    $staleQuery = "repo:microsoft/winget-pkgs type:pr state:open $PackageId in:title"
    $staleResponse = Invoke-RestMethod -Uri "https://api.github.com/search/issues?q=$([uri]::EscapeDataString($staleQuery))" -Headers @{ Authorization = "token $GitHubToken"; Accept = "application/vnd.github+json" } -ErrorAction Stop
    $closedCount = 0
    foreach ($item in $staleResponse.items) {
        if ($item.title -match "version $releaseVersion") {
            continue
        }
        Write-Host "  Closing stale PR #$($item.number): $($item.title)"
        $headers = @{ Authorization = "token $GitHubToken"; Accept = "application/vnd.github+json" }
        Invoke-RestMethod -Uri "https://api.github.com/repos/microsoft/winget-pkgs/issues/$($item.number)" -Method Patch -Headers $headers -Body (@{ state = "closed" } | ConvertTo-Json) -ErrorAction SilentlyContinue | Out-Null
        $commentBody = "Closing stale PR — superseded by version $releaseVersion. Only the latest version PR is kept active."
        Invoke-RestMethod -Uri "https://api.github.com/repos/microsoft/winget-pkgs/issues/$($item.number)/comments" -Method Post -Headers $headers -Body (@{ body = $commentBody } | ConvertTo-Json) -ErrorAction SilentlyContinue | Out-Null
        $closedCount++
    }
    if ($closedCount -gt 0) {
        Write-Host "  Closed $closedCount stale PR(s)."
    } else {
        Write-Host "  No stale PRs found."
    }
} catch {
    Write-Warning "Stale PR cleanup failed: $_. Continuing with submission."
}

& $wingetCreate show $PackageId | Out-Null
$packageExists = $LASTEXITCODE -eq 0

if (-not $packageExists) {
    Write-Host "Package '$PackageId' does not exist yet. Creating new manifest..."
    $allUrls = @($InstallerUrl)
    if ($InstallerUrlArm64) { $allUrls += $InstallerUrlArm64 }
    & $wingetCreate new $allUrls --token $GitHubToken --no-open
    exit 0
}

Write-Host "Step 1: Generating base manifest..."

$urls = @("${InstallerUrl}|x64|${scope}")
if ($InstallerUrlArm64) {
    $urls += "${InstallerUrlArm64}|arm64|${scope}"
}
Write-Host "  Installer URLs: $($urls -join ', ')"

& $wingetCreate update $PackageId --version $releaseVersion --urls $urls --out $manifestDir --token $GitHubToken

if ($LASTEXITCODE -ne 0) {
    Write-Host "::Error::Base manifest generation failed (exit $LASTEXITCODE). This never self-heals - going red so the release is visibly missing its winget PR."
    exit 1
}

Write-Host "Step 2a: Pinning every version-pinned repo URL to this release..."

# wingetcreate update inherits every field it is not asked to change from the
# previously published manifest, so any URL that embeds a release tag freezes at
# the tag the package was FIRST created with. That is how the 1.3.296 desktop
# manifest came to advertise https://github.com/topcheer/ggcode/blob/v1.3.52/LICENSE
# (winget-pkgs #491070): ~200 releases of stale links, and the validator fetches
# them, so one moved tag fails the submission.
#
# Rather than special-casing LicenseUrl (the field that happened to rot first),
# rewrite every github.com/topcheer/ggcode/(blob|tree)/v<semver> URL in every
# generated manifest to the release being published, then VERIFY that none is
# left over - a future inherited field cannot silently ship a stale link.
$releaseTag = "v$releaseVersion"
# Metadata links (blob/tree/releases tag pages) are safe to re-point: they carry
# no asset identity. Asset links (releases/download) are NOT rewritten - a tag
# bump alone would name a file that does not exist at that version - so a stale
# installer URL fails the run instead (the caller passed the wrong release URL).
$metaShape = "(blob|tree|releases/tag)"
$assetShape = "releases/download"
# Two metadata families: the page URLs under github.com and the static-file
# service used for LicenseUrl (raw.githubusercontent.com does not render HTML,
# which is the path that answered 503 to the winget validator twice on
# 2026-11-27 while the same tag was 200 everywhere else).
$repoURLPattern = "(?i)(https://github\.com/topcheer/ggcode/($metaShape|$assetShape)/v\d+\.\d+\.\d+([-.\w]*)|https://raw\.githubusercontent\.com/topcheer/ggcode/v\d+\.\d+\.\d+([-.\w]*))"
$rewritePattern = [regex]::new("(?i)(https://github\.com/topcheer/ggcode/$metaShape/v\d+\.\d+\.\d+|https://raw\.githubusercontent\.com/topcheer/ggcode/v\d+\.\d+\.\d+)")

$manifestFiles = Get-ChildItem -Path $manifestDir -Filter "*.yaml" -Recurse
if (-not $manifestFiles) {
    Write-Warning "No manifests found in $manifestDir; nothing to pin."
}
foreach ($file in $manifestFiles) {
    $content = Get-Content -Path $file.FullName -Raw
    $matchesFound = [regex]::Matches($content, "(?i)(https://github\.com/topcheer/ggcode/$metaShape/v\d+\.\d+\.\d+([-.\w]*)|https://raw\.githubusercontent\.com/topcheer/ggcode/v\d+\.\d+\.\d+([-.\w]*))")
    if (-not $matchesFound) { continue }
    $rewritten = $content
    foreach ($m in $matchesFound) {
        $old = $m.Value
        if ($old -like "*/$releaseTag*") { continue }
        if ($old -like "https://raw.githubusercontent.com/*") {
            $new = [regex]::Replace($old, "(?i)^(https://raw\.githubusercontent\.com/topcheer/ggcode/)v\d+\.\d+\.\d+", "`${1}$releaseTag")
        } else {
            $new = [regex]::Replace($old, "(?i)^(https://github\.com/topcheer/ggcode/(?:blob|tree|releases/tag)/)v\d+\.\d+\.\d+", "`${1}$releaseTag")
        }
        Write-Host "  $($file.Name): $old -> $new"
        $rewritten = $rewritten.Replace($old, $new)
    }
    if ($rewritten -ne $content) {
        Set-Content -Path $file.FullName -Value $rewritten -NoNewline
    }
}

# Fail closed: a manifest must not reach the validator with a tag-pinned repo URL
# that points at any release but this one.
$stale = @()
foreach ($file in (Get-ChildItem -Path $manifestDir -Filter "*.yaml" -Recurse)) {
    foreach ($m in [regex]::Matches((Get-Content -Path $file.FullName -Raw), $repoURLPattern)) {
        if ($m.Value -notlike "*/$releaseTag*") {
            $stale += "$($file.Name): $($m.Value)"
        }
    }
}
if ($stale.Count -gt 0) {
    foreach ($s in $stale) { Write-Host "::Error::stale tag-pinned URL in manifest - $s" }
    Write-Host "::Error::$($stale.Count) manifest URL(s) still point at a release other than $releaseTag. Not submitting (stale installer/asset URLs are never rewritten - pass the current release URL)."
    exit 1
}
Write-Host "  All version-pinned repo URLs point at $releaseTag"

# LicenseUrl must also be present - winget rejects a locale manifest without it,
# and a missing field is the other way this class of bug shows up.
$localeFile = Get-ChildItem -Path $manifestDir -Filter "*.locale.en-US.yaml" -Recurse | Select-Object -First 1
if ($localeFile) {
    $locale = Get-Content -Path $localeFile.FullName -Raw
    if ($locale -notmatch "(?m)^LicenseUrl:") {
        $licenseUrl = "https://raw.githubusercontent.com/topcheer/ggcode/$releaseTag/LICENSE"
        Write-Host "  LicenseUrl missing; inserting $licenseUrl"
        if ($locale -match "(?m)^License:") {
            $locale = [regex]::new("(?m)^License:.*$").Replace($locale, "`$0`nLicenseUrl: $licenseUrl", 1)
        } else {
            $locale = [regex]::new("(?m)^PackageLocale:.*$").Replace($locale, "`$0`nLicense: MIT`nLicenseUrl: $licenseUrl", 1)
        }
        Set-Content -Path $localeFile.FullName -Value $locale -NoNewline
    }
}

Write-Host "Step 2: Patching installer YAML with top-level fields..."

$installerFile = Get-ChildItem -Path $manifestDir -Filter "*.installer.yaml" -Recurse | Select-Object -First 1
if (-not $installerFile) {
    Write-Warning "Could not find installer YAML."
    exit 0
}

$yaml = Get-Content -Path $installerFile.FullName -Raw
Write-Host "  Generated installer YAML is $($yaml.Length) bytes"

$productCodeMatch = [regex]::Match($yaml, "ProductCode:\s*['""]?(\{[0-9A-Fa-f-]+\})['""]?")
$productCode = if ($productCodeMatch.Success) { $productCodeMatch.Groups[1].Value } else { "" }
Write-Host "  Extracted ProductCode from installer entry: $productCode"

$upgradeCodeMatch = [regex]::Match($yaml, "UpgradeCode:\s*['""]?(\{[0-9A-Fa-f-]+\})['""]?")
$upgradeCode = if ($upgradeCodeMatch.Success) { $upgradeCodeMatch.Groups[1].Value } else { "" }
Write-Host "  Extracted UpgradeCode: $upgradeCode"

$hasTopLevelProductCode = $yaml -match "(?m)^ProductCode:"
$hasTopLevelAppsAndFeatures = $yaml -match "(?m)^AppsAndFeaturesEntries:"
$hasCommands = $yaml -match "(?m)^Commands:"

Write-Host "  Top-level ProductCode: $(if ($hasTopLevelProductCode) {'present'} else {'MISSING'})"
Write-Host "  Top-level AppsAndFeaturesEntries: $(if ($hasTopLevelAppsAndFeatures) {'present'} else {'MISSING'})"
Write-Host "  Commands: $(if ($hasCommands) {'present'} else {'MISSING'})"

$displayName = if ($isCli) { "ggcode" } else { "GGCode Desktop" }
$publisher = "GG AI Studio"

$lines = $yaml -split "`n"
$result = @()
$insertedTopLevel = $false

foreach ($line in $lines) {

    if ($line -match "^Scope:\s*(.+)$") {
        $oldScope = $matches[1].Trim()
        if ($oldScope -ne $scope) {
            Write-Host "  Overriding top-level Scope: $oldScope -> $scope"
            $result += "Scope: $scope"
            continue
        }
    }

    if ($line -match "^Installers:" -and -not $insertedTopLevel) {

        if ($isCli -and -not $hasCommands) {
            $result += "Commands:"
            $result += "- ggcode"
        }

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

$yamlDir = Split-Path -Parent $installerFile.FullName
Write-Host "Step 3: Submitting manifest from $yamlDir to winget-pkgs..."
& $wingetCreate submit $yamlDir --token $GitHubToken --no-open

if ($LASTEXITCODE -eq 0) {
    Write-Host "Manifest submitted successfully!"
} else {

    Write-Warning "Manifest submission failed (exit code $LASTEXITCODE). Re-syncing fork and retrying once..."

    Start-Sleep -Seconds 20
    try {
        $syncHeaders = @{ Authorization = "token $GitHubToken"; Accept = "application/vnd.github+json" }
        Invoke-RestMethod -Uri "https://api.github.com/repos/topcheer/winget-pkgs/merge-upstream" -Method Post -Headers $syncHeaders -Body (@{ branch = "master" } | ConvertTo-Json) -ErrorAction Stop | Out-Null
        Write-Host "Fork re-synced. Retrying submission..."
    } catch {
        Write-Warning "Fork re-sync failed: $_"
    }
    & $wingetCreate submit $yamlDir --token $GitHubToken --no-open
    if ($LASTEXITCODE -eq 0) {
        Write-Host "Manifest submitted successfully on retry!"
    } else {
        Write-Host "::Error::Manifest submission failed twice (last exit code $LASTEXITCODE). Winget PR NOT created - re-run this job (gh run rerun --job <publish-winget job id>) or run scripts/release/publish-winget.ps1 locally with a token that can write topcheer/winget-pkgs."
        Write-Host "  Dumping patched YAML for debugging:"
        Write-Host "  ---"
        Get-Content $installerFile.FullName | ForEach-Object { Write-Host "  $_" }
        Write-Host "  ---"
        exit 1
    }
}

exit 0
