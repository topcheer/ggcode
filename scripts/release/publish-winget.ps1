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

if ($InstallerUrlArm64) {
    & $wingetCreate update $PackageId `
        --version $releaseVersion `
        --urls "$InstallerUrl|x64" "$InstallerUrlArm64|arm64" `
        --submit `
        --token $GitHubToken `
        --no-open
} else {
    & $wingetCreate update $PackageId `
        --version $releaseVersion `
        --urls "$InstallerUrl|x64" `
        --submit `
        --token $GitHubToken `
        --no-open
}

if ($LASTEXITCODE -ne 0) {
    throw "wingetcreate update failed for $PackageId"
}
