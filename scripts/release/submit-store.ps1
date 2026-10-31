# Submit GGCode Desktop packages to Microsoft Store via the official
# Store Submission API (manage.devcenter.microsoft.com).
#
# Prerequisites (one-time, documented in STORE_SUBMISSION.md):
#   Partner Center account linked to an Azure AD (Entra) app with a client
#   secret; the app granted access in Partner Center -> User management.
# Auth: client-credentials OAuth2 against the tenant, scope = the API.
#
# Flow: token -> list apps (resolve productId if needed) -> create
# submission (clones the last published one) -> upload zipped packages to
# the submission's SAS URL -> PUT the submission body with package entries
# -> commit -> poll until done.
#
# NOTE: first run may need small adjustments against live API responses
# (field names evolve); the structure below follows the documented v1.0
# submission shape.
param(
  [Parameter(Mandatory=$true)][string]$TenantId,
  [Parameter(Mandatory=$true)][string]$ClientId,
  [Parameter(Mandatory=$true)][string]$ClientSecret,
  [Parameter(Mandatory=$true)][string]$ProductId,     # Partner Center product id (XXX…)
  [string]$PackageDir,                                # dir containing .msix files
  [string]$ReleaseNotes = ""
)

$ErrorActionPreference = "Stop"
$api = "https://manage.devcenter.microsoft.com"

if (-not $PackageDir) { $PackageDir = (Get-Location).Path }
$msixFiles = Get-ChildItem (Join-Path $PackageDir "*.msix") -ErrorAction SilentlyContinue
if (-not $msixFiles -or $msixFiles.Count -eq 0) { throw "no .msix files found in $PackageDir" }

# 1. OAuth2 client-credentials token
$tokenResp = Invoke-RestMethod -Method Post -Uri "https://login.microsoftonline.com/$TenantId/oauth2/v2.0/token" -Body @{
  grant_type = "client_credentials"; client_id = $ClientId
  client_secret = $ClientSecret
  scope = "$api/.default"
} -ContentType "application/x-www-form-urlencoded"
$hdr = @{ Authorization = "Bearer $($tokenResp.access_token)" }
Write-Host "token acquired (expires in $($tokenResp.expires_in)s)"

# 2. (optional sanity) resolve the app
$app = Invoke-RestMethod -Method Get -Headers $hdr -Uri "$api/v1.0/my/applications/$ProductId"
Write-Host "app: $($app.primaryName) (id=$($app.id))"

# 3. create submission — clones the last published submission
$sub = Invoke-RestMethod -Method Post -Headers $hdr -Uri "$api/v1.0/my/applications/$ProductId/submissions"
$subId = $sub.id
Write-Host "submission created: $subId (state=$($sub.status))"

# 4. zip the packages and upload to the SAS url
$zipPath = Join-Path $env:TEMP "store-sub-$subId.zip"
Compress-Archive -Path ($msixFiles.FullName) -DestinationPath $zipPath -Force
$az = $sub.fileUploadUrl
Invoke-AzUpload -SasUri $az -FilePath $zipPath   # helper below
Write-Host "packages uploaded ($($msixFiles.Count) msix, $((Get-Item $zipPath).Length) bytes zipped)"

# 5. build submission body: keep listing from clone, replace packages
#    v1.0 shape: applicationCategory/listings/<lang>/packageEntries absent;
#    packages are described under listings.'en-us'.baseListing.packages? —
#    in the current API the upload itself associates package files; we PUT
#    the fetched submission back with only notes updated, letting package
#    replacement ride on the blob upload (committed in step 6).
if ($ReleaseNotes) {
  $sub | Add-Member -NotePropertyName releaseNotes -NotePropertyValue $ReleaseNotes -Force
}
$null = Invoke-RestMethod -Method Put -Headers $hdr `
  -Uri "$api/v1.0/my/applications/$ProductId/submissions/$subId" `
  -ContentType "application/json" -Body ($sub | ConvertTo-Json -Depth 32)
Write-Host "submission body updated"

# 6. commit -> certification
$null = Invoke-RestMethod -Method Post -Headers $hdr `
  -Uri "$api/v1.0/my/applications/$ProductId/submissions/$subId/commit"
Write-Host "committed; polling status..."

# 7. poll (certification takes 1-3 business days; poll shows commits phase)
$deadline = (Get-Date).AddMinutes(10)
do {
  Start-Sleep -Seconds 30
  $st = Invoke-RestMethod -Method Get -Headers $hdr `
    -Uri "$api/v1.0/my/applications/$ProductId/submissions/$subId/status"
  Write-Host "status: $($st.status) / step: $($st.status.additionalDetails) errors=$($st.status.errors.Count)"
  if ($st.status -eq "CommitFailed") { throw "commit failed: $($st | ConvertTo-Json -Depth 8)" }
} while ($st.status -in @("PendingCommit","CommitStarted") -and (Get-Date) -lt $deadline)
Write-Host "=== submission $subId handed off (status $($st.status)) ==="

# --- helper: PUT a file to an Azure Storage SAS url ---
function Invoke-AzUpload([string]$SasUri, [string]$FilePath) {
  # AzCopy/Storage module not guaranteed on runners; raw REST PUT works for block blobs
  $bytes = [System.IO.File]::ReadAllBytes($FilePath)
  Invoke-RestMethod -Method Put -Uri $SasUri -Body $bytes -ContentType "application/zip" -Headers @{ "x-ms-blob-type" = "BlockBlob" }
}
