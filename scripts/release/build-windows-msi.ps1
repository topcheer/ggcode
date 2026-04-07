param(
    [Parameter(Mandatory = $true)][string]$Version,
    [Parameter(Mandatory = $true)][string]$OutputDir
)

$ErrorActionPreference = "Stop"

$rootDir = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$packageVersion = $Version.TrimStart("v")
$commit = $env:GGCODE_COMMIT
$buildDate = $env:GGCODE_DATE
$upgradeCode = "{8E0F3BA8-802A-4FEA-9EDC-25475EB74ACF}"
$wxsPath = Join-Path $rootDir ".github\packaging\windows\ggcode.wxs"
$resolvedOutputDir = (New-Item -ItemType Directory -Force -Path $OutputDir).FullName
$workDir = Join-Path ([System.IO.Path]::GetTempPath()) ("ggcode-msi-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $workDir | Out-Null

$builds = @(
    @{ GoArch = "amd64"; WixArch = "x64"; Suffix = "x64" },
    @{ GoArch = "arm64"; WixArch = "arm64"; Suffix = "arm64" }
)

try {
    foreach ($build in $builds) {
        $stageDir = Join-Path $workDir $build.Suffix
        New-Item -ItemType Directory -Force -Path $stageDir | Out-Null

        Push-Location $rootDir
        try {
            $env:CGO_ENABLED = "0"
            $env:GOOS = "windows"
            $env:GOARCH = $build.GoArch
            $ldflags = @(
                "-s",
                "-w",
                "-X github.com/topcheer/ggcode/internal/version.Version=$Version",
                "-X github.com/topcheer/ggcode/internal/version.Commit=$commit",
                "-X github.com/topcheer/ggcode/internal/version.Date=$buildDate"
            ) -join " "
            go build -ldflags $ldflags -o (Join-Path $stageDir "ggcode.exe") ./cmd/ggcode
            if ($LASTEXITCODE -ne 0) {
                throw "go build failed for $($build.GoArch)"
            }
        }
        finally {
            Pop-Location
        }

        $target = Join-Path $resolvedOutputDir ("ggcode_{0}_windows_{1}.msi" -f $packageVersion, $build.Suffix)
        & wix build `
            -d "Version=$packageVersion" `
            -d "UpgradeCode=$upgradeCode" `
            -d "SourceDir=$stageDir" `
            -arch $build.WixArch `
            -o $target `
            $wxsPath
        if ($LASTEXITCODE -ne 0) {
            throw "wix build failed for $($build.WixArch)"
        }
    }
}
finally {
    if (Test-Path $workDir) {
        Remove-Item -Recurse -Force $workDir
    }
}
