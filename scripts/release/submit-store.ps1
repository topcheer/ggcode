
param(
  [Parameter(Mandatory=$true)][string]$TenantId,
  [Parameter(Mandatory=$true)][string]$ClientId,
  [Parameter(Mandatory=$true)][string]$ClientSecret,
  [Parameter(Mandatory=$true)][string]$ProductId,
  [string]$PackageDir,
  [string]$ReleaseNotes = ""
)

$ErrorActionPreference = "Stop"
$api = "https://manage.devcenter.microsoft.com"

if (-not $PackageDir) { $PackageDir = (Get-Location).Path }
$msixFiles = Get-ChildItem (Join-Path $PackageDir "*.msix") -ErrorAction SilentlyContinue
if (-not $msixFiles -or $msixFiles.Count -eq 0) { throw "no .msix files found in $PackageDir" }

$tokenResp = Invoke-RestMethod -Method Post -Uri "https://login.microsoftonline.com/$TenantId/oauth2/v2.0/token" -Body @{
  grant_type = "client_credentials"; client_id = $ClientId
  client_secret = $ClientSecret
  scope = "$api/.default"
} -ContentType "application/x-www-form-urlencoded"
$hdr = @{ Authorization = "Bearer $($tokenResp.access_token)" }
Write-Host "token acquired (expires in $($tokenResp.expires_in)s)"

$app = Invoke-RestMethod -Method Get -Headers $hdr -Uri "$api/v1.0/my/applications/$ProductId"
Write-Host "app: $($app.primaryName) (id=$($app.id))"

$sub = Invoke-RestMethod -Method Post -Headers $hdr -Uri "$api/v1.0/my/applications/$ProductId/submissions" -ContentType "application/json" -Body "{}"
$subId = $sub.id
Write-Host "submission created: $subId (state=$($sub.status))"

$zipPath = Join-Path $env:TEMP "store-sub-$subId.zip"
Compress-Archive -Path ($msixFiles.FullName) -DestinationPath $zipPath -Force
$az = $sub.fileUploadUrl
Invoke-AzUpload -SasUri $az -FilePath $zipPath
Write-Host "packages uploaded ($($msixFiles.Count) msix, $((Get-Item $zipPath).Length) bytes zipped)"

if ($ReleaseNotes) {
  $sub | Add-Member -NotePropertyName releaseNotes -NotePropertyValue $ReleaseNotes -Force
}
$null = Invoke-RestMethod -Method Put -Headers $hdr `
  -Uri "$api/v1.0/my/applications/$ProductId/submissions/$subId" `
  -ContentType "application/json" -Body ($sub | ConvertTo-Json -Depth 32)
Write-Host "submission body updated"

$null = Invoke-RestMethod -Method Post -Headers $hdr `
  -Uri "$api/v1.0/my/applications/$ProductId/submissions/$subId/commit"
Write-Host "committed; polling status..."

$deadline = (Get-Date).AddMinutes(10)
do {
  Start-Sleep -Seconds 30
  $st = Invoke-RestMethod -Method Get -Headers $hdr `
    -Uri "$api/v1.0/my/applications/$ProductId/submissions/$subId/status"
  Write-Host "status: $($st.status) / step: $($st.status.additionalDetails) errors=$($st.status.errors.Count)"
  if ($st.status -eq "CommitFailed") { throw "commit failed: $($st | ConvertTo-Json -Depth 8)" }
} while ($st.status -in @("PendingCommit","CommitStarted") -and (Get-Date) -lt $deadline)
Write-Host "=== submission $subId handed off (status $($st.status)) ==="

function Invoke-AzUpload([string]$SasUri, [string]$FilePath) {

  $bytes = [System.IO.File]::ReadAllBytes($FilePath)
  Invoke-RestMethod -Method Put -Uri $SasUri -Body $bytes -ContentType "application/zip" -Headers @{ "x-ms-blob-type" = "BlockBlob" }
}
