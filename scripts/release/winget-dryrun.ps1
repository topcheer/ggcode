#!/usr/bin/env pwsh
# Dry-run: validates winget publish parameters without submitting.
# Usage: ./scripts/release/winget-dryrun.ps1 [-Tag v1.3.67] [-Repo topcheer/ggcode]

param(
    [string]$Tag = "v1.3.67",
    [string]$Repo = "topcheer/ggcode"
)

$ErrorActionPreference = "Stop"
$packageVersion = $Tag.TrimStart("v")
$baseUrl = "https://github.com/$Repo/releases/download/$Tag"

$packages = @(
    @{ Id = "gg.ai.ggcode-cli"; X64 = "ggcode_${packageVersion}_windows_x64.msi"; Arm64 = "ggcode_${packageVersion}_windows_arm64.msi" },
    @{ Id = "gg.ai.ggcode-desktop"; X64 = "ggcode-desktop_${packageVersion}_windows_x64.msi"; Arm64 = $null }
)

foreach ($pkg in $packages) {
    Write-Host "`n--- $($pkg.Id) $packageVersion ---"

    # Check each installer URL exists (HTTP HEAD)
    $urls = @()
    foreach ($arch in @("X64", "Arm64")) {
        $file = $pkg[$arch]
        if (-not $file) { continue }
        $url = "$baseUrl/$file"
        Write-Host -NoNewline "  $arch : $url ... "
        try {
            $resp = Invoke-WebRequest -Method Head -Uri $url -UseBasicParsing
            if ($resp.StatusCode -eq 200) {
                Write-Host "OK ($([int]$resp.Headers['Content-Length']) bytes)"
                $urls += "$url|$arch.ToLower()"
            }
        } catch {
            Write-Host "MISSING ($($_.Exception.Response.StatusCode))"
        }
    }

    if ($urls.Count -eq 0) {
        Write-Host "  ERROR: no installer URLs found!"
        continue
    }

    # Check existing manifest to compare installer count
    Write-Host -NoNewline "  Existing manifest installers: "
    try {
        $manifests = Invoke-RestMethod "https://api.github.com/repos/microsoft/winget-pkgs/contents/manifests/$($pkg.Id -replace '\.','/')/$($pkg.Id)/$packageVersion"
        $installerFile = $manifests | Where-Object { $_.name -like "*.installer.yaml" }
        if ($installerFile) {
            $content = [System.Text.Encoding]::UTF8.GetString([System.Convert]::FromBase64String($installerFile.content))
            $installerCount = ([regex]::Matches($content, "InstallerUrl:")).Count
            Write-Host "$installerCount"
            if ($installerCount -ne $urls.Count) {
                Write-Host "  WARNING: manifest has $installerCount installers but we provide $($urls.Count)!"
            } else {
                Write-Host "  OK: installer count matches"
            }
        } else {
            # No existing version, check latest
            $versions = Invoke-RestMethod "https://api.github.com/repos/microsoft/winget-pkgs/contents/manifests/$($pkg.Id -replace '\.','/')/$($pkg.Id)"
            $latestVersion = ($versions | Sort-Object name -Descending | Select-Object -First 1).name
            Write-Host "No manifest for $packageVersion, latest is $latestVersion"
            $latestManifests = Invoke-RestMethod "https://api.github.com/repos/microsoft/winget-pkgs/contents/manifests/$($pkg.Id -replace '\.','/')/$($pkg.Id)/$latestVersion"
            $latestInstaller = $latestManifests | Where-Object { $_.name -like "*.installer.yaml" }
            if ($latestInstaller) {
                $content = [System.Text.Encoding]::UTF8.GetString([System.Convert]::FromBase64String($latestInstaller.content))
                $installerCount = ([regex]::Matches($content, "InstallerUrl:")).Count
                Write-Host "  Latest manifest has $installerCount installers, we provide $($urls.Count)"
                if ($installerCount -ne $urls.Count) {
                    Write-Host "  WARNING: mismatch!"
                } else {
                    Write-Host "  OK: installer count matches"
                }
            }
        }
    } catch {
        Write-Host "Could not check manifest: $_"
    }

    Write-Host "  wingetcreate command: wingetcreate update $($pkg.Id) --version $packageVersion --urls $($urls -join ' ') --submit --token <TOKEN> --no-open"
}
