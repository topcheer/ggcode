# ggcode interactive installer for Windows
# Usage:
#   irm https://ggcode.dev/install.ps1 | iex                    # interactive (default: user)
#   irm https://ggcode.dev/install.ps1 | iex -ArgumentList "--system"  # system-wide
#   irm https://ggcode.dev/install.ps1 | iex -ArgumentList "--user"    # current user (default)
#
# Or download and run:
#   .\install.ps1 -Scope User
#   .\install.ps1 -Scope System

param(
    [ValidateSet("User", "System", "Interactive")]
    [string]$Scope = "Interactive"
)

$ErrorActionPreference = "Stop"
$Repo = "topcheer/ggcode"
$BinaryName = "ggcode"
$ApiUrl = "https://api.github.com/repos/$Repo/releases/latest"

function Write-Info  { Write-Host "info: $args" -ForegroundColor Cyan }
function Write-Ok    { Write-Host "ok: $args" -ForegroundColor Green }
function Write-Warn  { Write-Host "warn: $args" -ForegroundColor Yellow }
function Write-Err   { Write-Host "error: $args" -ForegroundColor Red }

# --- Determine scope ---
if ($Scope -eq "Interactive" -and -not $env:GGCODE_INSTALL_SCOPE -and [Environment]::UserInteractive) {
    Write-Host ""
    Write-Host "Install ggcode for:" -ForegroundColor White
    Write-Host "  1) Current user only (no admin required)  [recommended]" -ForegroundColor Green
    Write-Host "  2) All users (requires admin)"
    Write-Host ""
    $choice = Read-Host "Choose [1]"
    if ($choice -eq "2" -or $choice -match "system|all") {
        $Scope = "System"
    } else {
        $Scope = "User"
    }
}

# Check environment variable override
if ($env:GGCODE_INSTALL_SCOPE -eq "machine") {
    $Scope = "System"
} elseif ($env:GGCODE_INSTALL_SCOPE -eq "user") {
    $Scope = "User"
}

# Default to User
if ($Scope -eq "Interactive") { $Scope = "User" }

# --- Determine install directory ---
if ($Scope -eq "System") {
    $InstallDir = Join-Path $env:ProgramFiles "ggcode"
    $NeedsAdmin = $true
} else {
    $InstallDir = Join-Path $env:LOCALAPPDATA "ggcode"
    $NeedsAdmin = $false
}

Write-Info "Installing ggcode to $InstallDir"

# --- Detect existing installation in the other scope ---
if ($Scope -eq "User") {
    $machinePath = Join-Path $env:ProgramFiles "ggcode"
    if (Test-Path (Join-Path $machinePath "ggcode.exe")) {
        Write-Warn "Found existing system-wide installation at $machinePath"
        Write-Host ""
        Write-Host "  A previous system-wide (all-users) ggcode installation was detected." -ForegroundColor Yellow
        Write-Host "  Installing per-user alongside it may cause PATH conflicts." -ForegroundColor Yellow
        Write-Host ""
        if ([Environment]::UserInteractive) {
            $migrate = Read-Host "Uninstall the system-wide version first? [Y/n]"
            if ($migrate -ne "n" -and $migrate -ne "N") {
                Write-Info "Uninstalling system-wide ggcode (requires admin)..."
                # Find the product code and uninstall
                $product = Get-Package -Name "ggcode" -ErrorAction SilentlyContinue | Where-Object { $_.ProviderName -eq "msi" }
                if ($product) {
                    $productCode = $product.FastPackageReference
                    Write-Info "Uninstalling product $productCode..."
                    Start-Process msiexec.exe -ArgumentList "/x $productCode /quiet" -Verb RunAs -Wait
                    Write-Ok "System-wide ggcode uninstalled."
                } else {
                    Write-Warn "Could not find MSI product. Please uninstall manually from Settings > Apps."
                }
            }
        }
    }
} elseif ($Scope -eq "System") {
    $userPath = Join-Path $env:LOCALAPPDATA "ggcode"
    if (Test-Path (Join-Path $userPath "ggcode.exe")) {
        Write-Warn "Found existing user installation at $userPath"
        Write-Host "  The per-user version will be left in place. You may uninstall it from Settings > Apps." -ForegroundColor Yellow
    }
}

# Check admin for system install
if ($NeedsAdmin) {
    $isAdmin = ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
    if (-not $isAdmin) {
        Write-Warn "System-wide install requires admin. Re-launching elevated..."
        $scriptPath = $MyInvocation.MyCommand.Definition
        if (-not $scriptPath) {
            Write-Err "Cannot re-launch for elevation when piped. Please download install.ps1 and run as admin."
            exit 1
        }
        $args = "-Scope System"
        Start-Process powershell -Verb RunAs -ArgumentList "-ExecutionPolicy Bypass -File `"$scriptPath`" $args"
        exit 0
    }
}

# --- Detect architecture ---
$Arch = if ([Environment]::Is64BitOperatingSystem) {
    if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "x86_64" }
} else {
    Write-Err "32-bit Windows is not supported."
    exit 1
}

Write-Info "Architecture: windows/$Arch"

# --- Get latest version ---
Write-Info "Fetching latest release..."
try {
    $release = Invoke-RestMethod -Uri $ApiUrl -UseBasicParsing
    $Tag = $release.tag_name
} catch {
    Write-Err "Could not fetch latest release: $_"
    exit 1
}

$Version = $Tag.TrimStart("v")
Write-Info "Latest version: $Tag"

# --- Download ---
$ArchiveName = "ggcode_windows_${Arch}.zip"
$DownloadUrl = "https://github.com/$Repo/releases/download/$Tag/$ArchiveName"
$TempDir = Join-Path $env:TEMP "ggcode-install-$([guid]::NewGuid().ToString('N'))"
New-Item -ItemType Directory -Force -Path $TempDir | Out-Null

try {
    Write-Info "Downloading $ArchiveName..."
    $ZipPath = Join-Path $TempDir $ArchiveName
    try {
        Invoke-WebRequest -Uri $DownloadUrl -OutFile $ZipPath -UseBasicParsing
    } catch {
        Write-Err "Download failed: $_"
        exit 1
    }

    Write-Info "Extracting..."
    Expand-Archive -Path $ZipPath -DestinationPath $TempDir -Force

    # Find the binary
    $BinaryPath = Get-ChildItem -Path $TempDir -Recurse -Filter "$BinaryName.exe" | Select-Object -First 1
    if (-not $BinaryPath) {
        Write-Err "Could not find $BinaryName.exe in archive."
        exit 1
    }

    # --- Install ---
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    $InstallTarget = Join-Path $InstallDir "$BinaryName.exe"
    Copy-Item $BinaryPath.FullName $InstallTarget -Force

    Write-Ok "Installed $BinaryName $Tag to $InstallTarget"

    # --- PATH setup ---
    $pathScope = if ($Scope -eq "System") { "Machine" } else { "User" }
    $currentPath = [Environment]::GetEnvironmentVariable("PATH", $pathScope)
    if ($currentPath -notlike "*$InstallDir*") {
        $newPath = if ($currentPath) { "$currentPath;$InstallDir" } else { $InstallDir }
        [Environment]::SetEnvironmentVariable("PATH", $newPath, $pathScope)
        Write-Ok "Added $InstallDir to $pathScope PATH"
        Write-Warn "Open a new terminal for PATH changes to take effect."
    } else {
        Write-Info "$InstallDir is already in PATH"
    }

    # --- Verify ---
    Write-Host ""
    Write-Ok "ggcode $Tag is ready."
    Write-Host ""
    Write-Host "  Run 'ggcode' to start." -ForegroundColor White
    Write-Host "  Update with: ggcode /update" -ForegroundColor White

} finally {
    if (Test-Path $TempDir) {
        Remove-Item -Recurse -Force $TempDir
    }
}
