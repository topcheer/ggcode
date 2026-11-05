
param(
  [Parameter(Mandatory=$true)][string]$TenantId,
  [Parameter(Mandatory=$true)][string]$ClientId,
  [Parameter(Mandatory=$true)][string]$ClientSecret,
  [Parameter(Mandatory=$true)][string]$ProductId,
  [string]$PackageDir,
  [string]$ReleaseNotes = "",
  [string]$ResumeSubmissionId = ""
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

# If a submission is already in flight, resume it instead of creating a new
# one (the Ingestion API rejects creating on top of an in-flight submission).
# Resuming makes reruns idempotent: a draft left dangling by an earlier failed
# step (e.g. the pre-#5048 Invoke-AzUpload crash) gets its package uploaded and
# committed instead of blocking every future run until 30-day expiry.
if ($ResumeSubmissionId) {
  $subId = $ResumeSubmissionId
  Write-Host "resuming explicit submission $subId (-ResumeSubmissionId)"
  $sub = Invoke-RestMethod -Method Get -Headers $hdr -Uri "$api/v1.0/my/applications/$ProductId/submissions/$subId"
} elseif ($app.pendingApplicationSubmission) {
  # Store API surfaces a blocked draft as "pendingApplicationSubmission" on
  # the application resource (not "inFlightSubmissionId") - verified against
  # the app JSON logged by run 41968716223. Resuming it instead of creating
  # avoids the 409 Conflict create-submission rejects with.
  $subId = $app.pendingApplicationSubmission.id
  Write-Host "pending submission $subId found; resuming it instead of creating a new one"
  $sub = Invoke-RestMethod -Method Get -Headers $hdr -Uri "$api/v1.0/my/applications/$ProductId/submissions/$subId"
} elseif ($app.inFlightSubmissionId) {
  $subId = $app.inFlightSubmissionId
  Write-Host "in-flight submission $subId found; resuming it instead of creating a new one"
  $sub = Invoke-RestMethod -Method Get -Headers $hdr -Uri "$api/v1.0/my/applications/$ProductId/submissions/$subId"
} else {
  # Create-submission takes no request body; a non-empty JSON body would be
  # parsed as a draft submission and rejected ("The size of Listings must be
  # 1 or more"). PS 7 keeps the explicit JSON content-type on a bodyless POST.
  # On Conflict (a draft exists that GET application did not surface via
  # inFlightSubmissionId) there is nothing to recover the id from, so surface
  # the app JSON to make diagnosis possible and fail loudly.
  try {
    $sub = Invoke-RestMethod -Method Post -Headers $hdr -Uri "$api/v1.0/my/applications/$ProductId/submissions" -ContentType "application/json"
  } catch {
    Write-Host "create-submission failed; app JSON for diagnosis:"
    $app | ConvertTo-Json -Depth 8 | Write-Host
    throw
  }
  $subId = $sub.id
  Write-Host "submission created: $subId (state=$($sub.status))"
}

$zipPath = Join-Path $env:TEMP "store-sub-$subId.zip"
Compress-Archive -Path ($msixFiles.FullName) -DestinationPath $zipPath -Force
$az = $sub.fileUploadUrl
# Upload via plain Azure Blob REST PUT against the submission's SAS URL —
# no Az PowerShell module (the old Invoke-AzUpload call never existed and
# crashed the run after the submission draft was created).
Invoke-RestMethod -Method Put -Uri $az -Headers @{ "x-ms-blob-type" = "BlockBlob" } `
  -InFile $zipPath -ContentType "application/zip"
Write-Host "packages uploaded ($($msixFiles.Count) msix, $((Get-Item $zipPath).Length) bytes zipped)"

if ($ReleaseNotes) {
  $sub | Add-Member -NotePropertyName releaseNotes -NotePropertyValue $ReleaseNotes -Force
}
# Draft submissions created via the API can carry placeholder market pricing
# (priceId "Base"), which the Ingestion API rejects on PUT ("'Base' is not a
# valid PriceId for base price."). Normalize to the Free tier ("0") before
# round-tripping the submission body.
#
# Round 9 (runs 41954855268/41958211337): three shape assumptions in a row
# (flattened listing -> market map -> baseListing nesting) each missed, and the
# round-8 log probe ("normalizing priceId" never printed) proved the loop body
# never executed - so the shape is none of the three. Stop guessing: dump the
# raw listings subtree (evidence for THIS run's log) and normalize recursively,
# shape-agnostic: walk every object/array node under listings and rewrite any
# string priceId that is not "0". Covers object-map AND array marketPricings,
# baseListing nesting, and any future depth change.
$rawShape = ($sub.listings | ConvertTo-Json -Depth 12 -Compress)
if ($rawShape.Length -gt 4000) { $rawShape = $rawShape.Substring(0, 4000) + "...(truncated)" }
Write-Host "listings shape (raw, pre-normalize): $rawShape"

$script:priceIdFixCount = 0
function Normalize-PriceId($node, [string]$path) {
  if ($null -eq $node) { return }
  if ($node -is [System.Management.Automation.PSCustomObject]) {
    foreach ($p in @($node.PSObject.Properties)) {
      if ($p.Name -eq "priceId" -and $p.Value -is [string] -and $p.Value -ne "0") {
        Write-Host "normalizing priceId '$($p.Value)' -> '0' at $($path).priceId"
        $p.Value = "0"
        $script:priceIdFixCount++
      } else {
        Normalize-PriceId $p.Value "$path.$($p.Name)"
      }
    }
  } elseif ($node -is [System.Collections.IEnumerable] -and $node -isnot [string]) {
    $i = 0
    foreach ($el in @($node)) { Normalize-PriceId $el "$path[$i]"; $i++ }
  }
}
Normalize-PriceId $sub.listings "listings"
Write-Host "priceId normalization complete ($($script:priceIdFixCount) fix(es) applied)"
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
