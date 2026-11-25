# ggcode installer for Windows (PowerShell 5.1 or later).
#
#   irm https://ggcode.dev/install.ps1 | iex
#
# Detects the OS architecture (x86_64 / arm64), downloads the matching
# release archive, verifies its SHA-256 against the release's checksums.txt,
# installs ggcode.exe into %LOCALAPPDATA%\ggcode\bin (no admin) and adds that
# directory to the user PATH.
#
# When saved as a file, options are available:
#   .\install.ps1 -DryRun                 Print the resolved URLs, install nothing
#   .\install.ps1 -Version v1.3.292        Install a specific release (default: latest)
#   .\install.ps1 -InstallDir C:\Tools     Install directory
#
# Environment overrides (also work with `irm | iex`):
#   GGCODE_INSTALL_DIR      Install directory (default: %LOCALAPPDATA%\ggcode\bin)
#   GGCODE_VERSION          Release tag to install (default: latest)
#   GGCODE_INSTALL_DRY_RUN  Set to 1 for a dry run
#   GGCODE_DOWNLOAD_BASE    Download mirror (default: https://download.ggcode.dev/latest - a
#                           Cloudflare Worker streaming + edge-caching the GitHub release
#                           assets; every step falls back to GitHub)
#   GGCODE_CHECKSUMS_URL    Explicit checksums.txt URL (no fallback when set)
#   GGCODE_ARCHIVE_URL      Explicit archive URL (no fallback when set)

param(
    [string]$Version = "",
    [string]$InstallDir = "",
    [switch]$DryRun
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"   # the 5.1 progress bar makes large downloads very slow

function Write-Info { param([string]$Message) Write-Host "info: $Message" -ForegroundColor Cyan }
function Write-Ok   { param([string]$Message) Write-Host "ok: $Message" -ForegroundColor Green }
function Write-Warn { param([string]$Message) Write-Host "warn: $Message" -ForegroundColor Yellow }
function Write-Err  { param([string]$Message) Write-Host "error: $Message" -ForegroundColor Red }

# Print the message, then abort. The caller decides whether `exit` is safe
# (`exit` inside `iex` would close the user's PowerShell window).
function Fail {
    param([string]$Message)
    Write-Err $Message
    throw "INSTALL-FAILED: $Message"
}

function Install-Ggcode {
    $repo = "topcheer/ggcode"
    $downloadBase = if ($env:GGCODE_DOWNLOAD_BASE) { $env:GGCODE_DOWNLOAD_BASE.TrimEnd("/") } else { "https://download.ggcode.dev/latest" }
    $githubBase = "https://github.com/$repo"
    $apiUrl = "https://api.github.com/repos/$repo/releases/latest"

    if (-not $Version) { $Version = if ($env:GGCODE_VERSION) { $env:GGCODE_VERSION } else { "latest" } }
    # ---- Platform ------------------------------------------------------------
    if ($env:OS -ne "Windows_NT" -and -not $DryRun) {
        Fail "This installer is for Windows. On macOS/Linux use: curl -fsSL https://ggcode.dev/install.sh | bash"
    }
    if (-not $InstallDir) {
        if ($env:GGCODE_INSTALL_DIR) { $InstallDir = $env:GGCODE_INSTALL_DIR }
        elseif ($env:LOCALAPPDATA) { $InstallDir = Join-Path $env:LOCALAPPDATA "ggcode\bin" }
        elseif ($DryRun) { $InstallDir = "%LOCALAPPDATA%\ggcode\bin" }   # display only
        else { Fail "LOCALAPPDATA is not set; pass -InstallDir <dir> or set GGCODE_INSTALL_DIR" }
    }
    if ($env:GGCODE_INSTALL_DRY_RUN -and $env:GGCODE_INSTALL_DRY_RUN -ne "0") { $DryRun = $true }
    $checksumsUrlOverride = $env:GGCODE_CHECKSUMS_URL
    $archiveUrlOverride = $env:GGCODE_ARCHIVE_URL

    # Windows PowerShell 5.1 on older .NET may not negotiate TLS 1.2 by default.
    try {
        [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    } catch { }
    $osArch = $null
    try { $osArch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString() } catch { }
    if (-not $osArch) {
        # Fallback for .NET < 4.7.1: PROCESSOR_ARCHITEW6432 is set for a 32-bit process on a 64-bit OS.
        $envArch = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
        switch -Regex ($envArch) {
            "^ARM64$" { $osArch = "Arm64" }
            "^(AMD64|x64)$" { $osArch = "X64" }
            default { $osArch = $envArch }
        }
    }
    if ($osArch -eq "Arm64") { $arch = "arm64" }
    elseif ($osArch -eq "X64") { $arch = "x86_64" }
    else { Fail "unsupported Windows architecture: $osArch. ggcode ships for x86_64 and arm64." }
    $archiveName = "ggcode_windows_$arch.zip"

    # ---- Release resolution: mirror manifest -> GitHub API -------------------
    $manifest = $null
    $manifestUrl = "$downloadBase/manifest.json"
    if ($Version -eq "latest") {
        try {
            # Parse explicitly: Windows PowerShell 5.1 only deserialises when the server sends a JSON content type.
            $resp = Invoke-WebRequest -Uri $manifestUrl -UseBasicParsing -TimeoutSec 30
            $content = $resp.Content
            if ($content -is [byte[]]) { $content = [System.Text.Encoding]::UTF8.GetString($content) }
            $manifest = $content | ConvertFrom-Json
            if (-not $manifest -or -not $manifest.version) { $manifest = $null }
        } catch { $manifest = $null }
    }
    $tag = $null
    if ($Version -ne "latest") {
        if ($Version -notmatch "^v?\d+\.\d+\.\d+") { Fail "invalid version: $Version (expected e.g. v1.3.292)" }
        $tag = if ($Version -match "^v") { $Version } else { "v$Version" }
    } elseif ($manifest) {
        $tag = "$($manifest.version)"
    } else {
        # https://github.com/<owner>/<repo>/releases/latest redirects to .../releases/tag/<tag> (no API rate limit).
        $location = $null
        try {
            $resp = Invoke-WebRequest -Uri "$githubBase/releases/latest" -Method Head -MaximumRedirection 0 -UseBasicParsing -TimeoutSec 30 -ErrorAction Stop
            $location = $resp.Headers["Location"]
        } catch {
            $r = $_.Exception.Response
            # PowerShell 7 exposes System.Net.Http.HttpResponseMessage, Windows PowerShell 5.1 a
            # HttpWebResponse. Compare by type NAME: the type literal [System.Net.Http.HttpResponseMessage]
            # itself fails on PS 5.1 ("type not found") because System.Net.Http is not loaded there.
            if ($r -and "$($r.GetType().FullName)" -eq "System.Net.Http.HttpResponseMessage") { $location = "$($r.Headers.Location)" }
            elseif ($r) { $location = $r.Headers["Location"] }
        }
        if ($location) { $tag = "$location".TrimEnd("/").Split("/")[-1] }
        if (-not $tag) {
            try {
                $tag = (Invoke-RestMethod -Uri $apiUrl -TimeoutSec 30 -ErrorAction Stop).tag_name
            } catch {
                Fail "could not determine the latest release (tried $manifestUrl, $githubBase/releases/latest and $apiUrl): $_"
            }
        }
    }
    if (-not $tag -or $tag -notmatch "^v\d+\.\d+\.\d+") { Fail "unexpected release tag: '$tag'" }

    # ---- Resolve archive + checksums URLs ------------------------------------
    $archiveUrl = $archiveUrlOverride
    if (-not $archiveUrl -and $manifest -and $manifest.files) {
        $entry = @($manifest.files) | Where-Object { $_.name -eq $archiveName } | Select-Object -First 1
        if ($entry -and $entry.url) { $archiveUrl = "$($entry.url)" }
    }
    if (-not $archiveUrl) { $archiveUrl = "$githubBase/releases/download/$tag/$archiveName" }

    $githubChecksumsUrl = "$githubBase/releases/download/$tag/checksums.txt"
    if ($checksumsUrlOverride) { $checksumUrls = @($checksumsUrlOverride) }
    elseif ($Version -ne "latest") { $checksumUrls = @($githubChecksumsUrl) }   # the mirror only mirrors "latest"
    else { $checksumUrls = @("$downloadBase/checksums.txt", $githubChecksumsUrl) }

    Write-Info "Platform: windows/$arch"
    Write-Info "Version: $tag"

    if ($DryRun) {
        Write-Info "archive:   $archiveUrl"
        Write-Info "checksums: $($checksumUrls[0])"
        for ($i = 1; $i -lt $checksumUrls.Count; $i++) { Write-Info "           (fallback: $($checksumUrls[$i]))" }
        Write-Info "install:   $InstallDir\ggcode.exe"
        Write-Ok "dry run - nothing was downloaded or installed"
        return
    }

    # ---- Download + verify ---------------------------------------------------
    $tempBase = if ($env:TEMP) { $env:TEMP } else { [System.IO.Path]::GetTempPath() }
    $workDir = Join-Path $tempBase ("ggcode-install-" + [guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Force -Path $workDir | Out-Null
    try {
        $zipPath = Join-Path $workDir $archiveName
        Write-Info "Downloading $archiveUrl"
        try {
            Invoke-WebRequest -Uri $archiveUrl -OutFile $zipPath -UseBasicParsing -TimeoutSec 900
        } catch {
            Fail "download failed: $archiveUrl. Check your connection, or download $githubBase/releases/download/$tag/$archiveName manually. ($_)"
        }
        if (-not (Test-Path $zipPath) -or (Get-Item $zipPath).Length -eq 0) { Fail "downloaded archive is empty: $archiveUrl" }

        $checksumsPath = Join-Path $workDir "checksums.txt"
        $checksumsSource = $null
        foreach ($url in $checksumUrls) {
            Write-Info "Downloading $url"
            try {
                Invoke-WebRequest -Uri $url -OutFile $checksumsPath -UseBasicParsing -TimeoutSec 60
                $checksumsSource = $url
                break
            } catch { }
        }
        if (-not $checksumsSource) { Fail "could not download checksums.txt from: $($checksumUrls -join ', ')" }

        $expected = $null
        foreach ($line in (Get-Content -Path $checksumsPath)) {
            $parts = $line.Trim() -split "\s+"
            if ($parts.Count -ge 2 -and $parts[1].TrimStart("*") -eq $archiveName) {
                $expected = $parts[0].ToLowerInvariant()
                break
            }
        }
        if (-not $expected) { Fail "no SHA-256 entry for $archiveName in $checksumsSource" }
        $actual = (Get-FileHash -Path $zipPath -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($actual -ne $expected) {
            Fail "checksum mismatch for ${archiveName}: expected $expected, got $actual. Refusing to install."
        }
        Write-Ok "Checksum verified ($archiveName)"

        # ---- Extract + install ---------------------------------------------
        Write-Info "Extracting..."
        $extractDir = Join-Path $workDir "extract"
        Expand-Archive -Path $zipPath -DestinationPath $extractDir -Force
        $exe = Get-ChildItem -Path $extractDir -Recurse -Filter "ggcode.exe" -File | Select-Object -First 1
        if (-not $exe) { Fail "could not find ggcode.exe in $archiveName" }

        New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
        $InstallDir = (Get-Item $InstallDir).FullName
        $target = Join-Path $InstallDir "ggcode.exe"
        $tmpTarget = Join-Path $InstallDir ("ggcode." + [guid]::NewGuid().ToString("N") + ".tmp.exe")
        Copy-Item $exe.FullName $tmpTarget -Force
        try {
            Move-Item -Path $tmpTarget -Destination $target -Force
        } catch {
            Remove-Item $tmpTarget -Force -ErrorAction SilentlyContinue
            Fail "could not replace $target (is ggcode running? close it and retry): $_"
        }
        Write-Ok "Installed ggcode $tag to $target"

        # ---- PATH (user scope); a failure here must not fail the install --------------
        try {
            $userPath = [Environment]::GetEnvironmentVariable("PATH", "User")
            $entries = @()
            if ($userPath) { $entries = @($userPath -split ";" | ForEach-Object { $_.Trim().TrimEnd("\") }) }
            if (-not ($entries -contains $InstallDir.TrimEnd("\"))) {
                $newPath = if ($userPath) { "$($userPath.TrimEnd(';'));$InstallDir" } else { $InstallDir }
                [Environment]::SetEnvironmentVariable("PATH", $newPath, "User")
                Write-Ok "Added $InstallDir to the user PATH (new terminals pick this up automatically)"
            } else {
                Write-Info "$InstallDir is already in the user PATH"
            }
        } catch {
            Write-Warn "could not update the user PATH: $_. Add $InstallDir to your PATH manually."
        }
        if (-not (@($env:PATH -split ";" | ForEach-Object { $_.Trim().TrimEnd("\") }) -contains $InstallDir.TrimEnd("\"))) {
            $env:PATH = "$InstallDir;$env:PATH"   # usable in this session right away
        }

        # ---- Version ---------------------------------------------------------
        $global:LASTEXITCODE = 0
        $prevEap = $ErrorActionPreference
        $ErrorActionPreference = "Continue"   # native stderr must not throw under 5.1
        try {
            $versionOutput = (& $target --version 2>&1 | Out-String).Trim()
        } finally {
            $ErrorActionPreference = $prevEap
        }
        if ($LASTEXITCODE -ne 0) {
            Fail "installed, but '$target --version' failed: $versionOutput"
        }
        Write-Ok "ggcode is ready: $versionOutput"
        Write-Host ""
        Write-Host "  Run 'ggcode' to start (open a new terminal if PATH was updated)." -ForegroundColor White
    } finally {
        if (Test-Path $workDir) { Remove-Item -Recurse -Force $workDir -ErrorAction SilentlyContinue }
    }
}

if ($MyInvocation.InvocationName -ne ".") {
    # Dot-sourcing only defines the functions (used by tests); otherwise run.
    try {
        Install-Ggcode
    } catch {
        $message = $_.Exception.Message
        if (-not $message.StartsWith("INSTALL-FAILED:")) { Write-Err $message }   # unexpected error: surface it
        if ($PSCommandPath) { exit 1 } else { return }   # `exit` inside `iex` would close the console
    }
}
