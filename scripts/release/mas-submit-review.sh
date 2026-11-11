#!/usr/bin/env bash
# mas-submit-review.sh — poll an ASC build through processing, then submit
# the app version for review via the reviewSubmissions API.
#
# Why this exists: the classic appStoreVersionSubmissions endpoint is
# deprecated and answers 403 with a key that can only DELETE (observed
# 2026-11-06 on both iOS and macOS lanes). The reviewSubmissions flow
# (POST reviewSubmissions -> POST items -> PATCH submitted:true) is the
# supported replacement and only needs a normal API key.
#
# Usage:
#   mas-submit-review.sh --bundle-id gg.ai.ggcode --version 1.0.0 \
#     --platform MAC_OS --key-path ~/private_keys/AuthKey_X.p8 \
#     --key-id X --issuer Y [--max-wait 40] [--no-submit]
#
# Env: none beyond the flags. Requires jq and openssl (present on GH
# macos runners).

set -euo pipefail

BUNDLE_ID="" VERSION="" BUILD_VERSION="" APP_ID_ARG="" PLATFORM="MAC_OS" KEY_PATH="" KEY_ID="" ISSUER=""
MAX_WAIT=40 NO_SUBMIT=0 API_BASE="https://api.appstoreconnect.apple.com"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --bundle-id) BUNDLE_ID="$2"; shift 2;;
    --version) VERSION="$2"; shift 2;;
    --platform) PLATFORM="$2"; shift 2;;
    --key-path) KEY_PATH="$2"; shift 2;;
    --key-id) KEY_ID="$2"; shift 2;;
    --issuer) ISSUER="$2"; shift 2;;
    --max-wait) MAX_WAIT="$2"; shift 2;;
    --build) BUILD_VERSION="$2"; shift 2;;
    --app-id) APP_ID_ARG="$2"; shift 2;;
    --no-submit) NO_SUBMIT=1; shift;;
    *) echo "unknown flag: $1" >&2; exit 2;;
  esac
done

[[ -n "$BUNDLE_ID" && -n "$VERSION" && -n "$KEY_PATH" && -n "$KEY_ID" && -n "$ISSUER" ]] || {
  echo "missing required flags" >&2; exit 2; }
echo "config: bundle=$BUNDLE_ID version=$VERSION build=${BUILD_VERSION:-<latest>} platform=$PLATFORM app_id_hint=${APP_ID_ARG:-none} issuer=$ISSUER key_id=$KEY_ID"

# ---------- JWT (ES256, the only scheme ASC accepts for API keys) ----------
b64url() { openssl base64 -A | tr '+/' '-_' | tr -d '='; }

# Convert a DER ECDSA-Signature (stdin) to the raw 64-byte r||s form JWT needs.
# Minimal DER walk: SEQUENCE { INTEGER r, INTEGER s } — strip tag/len, drop
# leading zero bytes, left-pad each integer to exactly 32 bytes.
der_to_raw() {
python3 -c '
import sys
der = sys.stdin.buffer.read()
def read_int(i):
    assert der[i] == 0x02, "expected INTEGER tag"
    ln = der[i+1]
    return der[i+2:i+2+ln].lstrip(b"\x00").rjust(32, b"\x00"), i + 2 + ln
i = 2
r, i = read_int(i)
s2, i = read_int(i)
sys.stdout.buffer.write(r + s2)
'
}
make_jwt() {
  # ASC API keys: exp - iat must be <= 20 min. The poll loop runs up to
  # MAX_WAIT (40) minutes, so a token minted once at script start expires
  # mid-loop and every subsequent call 401s (run 42333602022: poll #80 401
  # after 40 min, early polls were 200/NOT-YET-VISIBLE). Mint lazily and
  # refresh with a 5-minute safety margin before each asc() call.
  local now hdr payload sig sig_input
  now=$(date +%s)
  JWT_EXP=$((now + 1200))
  hdr=$(printf '{"alg":"ES256","kid":"%s","typ":"JWT"}' "$KEY_ID" | b64url)
  payload=$(printf '{"iss":"%s","iat":%d,"exp":%d,"aud":"appstoreconnect-v1"}' "$ISSUER" "$now" "$JWT_EXP" | b64url)
  sig_input="${hdr}.${payload}"
  # ES256 per RFC 7515 requires the RAW 64-byte r||s signature. `openssl dgst
  # -sign` emits DER (~70 bytes) — signing with DER verbatim yields ASC
  # 401 NOT_AUTHORIZED (run 42224235910 verbose evidence). Strip DER to r||s:
  # NOTE: DER bytes must NEVER pass through a shell variable — command
  # substitution strips NUL/newline bytes. Pipe openssl -> der_to_raw -> b64url.
  sig=$(printf '%s' "$sig_input" | openssl dgst -sha256 -sign "$KEY_PATH" -binary | der_to_raw | b64url)
  JWT="${sig_input}.${sig}"
}
JWT=""
JWT_EXP=0
make_jwt
LOGD=/tmp/asc-debug.log
logd() { printf '%s\n' "$*" | tee -a "$LOGD" >&2; }

# Refresh helper - MUST be called from the parent scope. asc() also tries,
# but every call site uses $(asc ...), a subshell whose variable mutations
# never propagate - that made the first attempt at in-asc refresh a no-op.
refresh_jwt_if_needed() {
  if (( $(date +%s) >= JWT_EXP - 300 )); then
    logd "JWT expired/near-expiry - refreshing (exp was ${JWT_EXP})"
    make_jwt
  fi
}

asc() { # asc_fatal "step-name" METHOD PATH [JSON_BODY] — non-2xx = ::error:: + exit 1
  local fatal="${1:-}"; shift
  local method="$1" path="$2" body="${3:-}"
  local resp http_code
  if (( $(date +%s) >= JWT_EXP - 300 )); then
    logd "JWT expired/near-expiry - refreshing (exp was ${JWT_EXP})"
    make_jwt
  fi
  if [[ -n "$body" ]]; then
    resp=$(curl -sS --max-time 60 -w '\n__HTTP__%{http_code}' -X "$method" "$API_BASE$path" \
      -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" -d "$body")
  else
    resp=$(curl -sS --max-time 60 -w '\n__HTTP__%{http_code}' -X "$method" "$API_BASE$path" -H "Authorization: Bearer $JWT")
  fi
  http_code="${resp##*__HTTP__}"
  resp="${resp%__HTTP__*}"
  printf '%s' "$http_code" > /tmp/asc-last-code
  logd "=== ASC $method $path -> HTTP $http_code ==="
  logd "request-body: ${body:-(none)}"
  logd "response: $resp"
  if [[ "$http_code" != 2* ]]; then
    if [[ "$fatal" != "-" ]]; then
      # Run 42359007107 lesson: soft-warning here produced false-success logs
      # ("removed"/"set on"/"bound" printed after 403/409) and the failure
      # only surfaced at the very last submit. State-mutating steps must be
      # fatal with the ASC error detail inlined.
      echo "::error::ASC $fatal failed: HTTP $http_code $method $path - $(echo "$resp" | jq -rj '.errors[0].detail // .errors[0].title // empty' 2>/dev/null) | raw: ${resp:0:300}" >&2
      exit 1
    fi
    echo "::warning::ASC $method $path returned HTTP $http_code (non-2xx) - response logged" >&2
  fi
  printf '%s' "$resp"
}

# ---------- locate the app ----------
refresh_jwt_if_needed
APP_JSON=$(asc "-" GET "/v1/apps?filter%5BbundleId%5D=${BUNDLE_ID}")
APP_COUNT=$(echo "$APP_JSON" | jq '.data | length' 2>/dev/null || echo 0)
APP_ID=$(echo "$APP_JSON" | jq -r '.data[0].id // empty')
if [[ "$APP_COUNT" -gt 1 && -n "$APP_ID_ARG" ]] && echo "$APP_JSON" | jq -e --arg id "$APP_ID_ARG" '.data[] | select(.id == $id)' >/dev/null 2>&1; then
  # Multiple apps share this bundleId (legacy + current, #5431): .data[0]
  # order is not stable and run 42988168677 polled the legacy app for 40 min
  # while the upload (routed by bundle-id) landed on the real one. The
  # workflow pins the verified app id - honor it when it is among matches.
  APP_ID="$APP_ID_ARG"
  echo "bundleId lookup returned ${APP_COUNT} apps; pinned --app-id ${APP_ID}"
fi
if [[ -z "$APP_ID" ]]; then
  # Run 13 evidence: this failure was silent, and the fallback id pointed at a
  # nonexistent resource (build stayed VALID in TestFlight while poll said
  # last=none for 40 min). Always surface the raw response for diagnosis.
  echo "::warning::bundleId lookup returned no app; raw response: ${APP_JSON:0:400}"
fi
if [[ -z "$APP_ID" && -n "$APP_ID_ARG" ]]; then
  echo "::warning::falling back to --app-id ${APP_ID_ARG}"
  APP_ID="$APP_ID_ARG"
fi
[[ -n "$APP_ID" ]] || { echo "::error::no ASC app for bundleId ${BUNDLE_ID}: ${APP_JSON}" >&2; exit 1; }
echo "app id: ${APP_ID}"

# ---------- poll the build through processing ----------
# altool upload returns before Apple finishes processing; the build only
# becomes usable (and submittable) once processingState == VALID.
echo "polling build ${VERSION} for processing (max ${MAX_WAIT} min)..."
BUILD_ID=""
CONSEC_401=0
for i in $(seq 1 "$((MAX_WAIT*2))"); do
  refresh_jwt_if_needed
  # Poll keyed on the BUILD number (CFBundleVersion) when given - the short
  # version alone can match a stale build or none at all (#5139 invariant 1).
  # Run 42351262813: ASC rejects BOTH filter[version] and sort on the
  # builds-in-app relationship endpoint (HTTP 400 PARAMETER_ERROR.ILLEGAL,
  # one error per parameter) - the 401 chain previously masked this. Fetch a
  # page without filter/sort and match client-side instead.
  # Paginate client-side: first page only truncated at limit=50 (run
  # 42988168677 saw total=300 nextCursor=Mg and matched nothing, #5431).
  # ASC rejects filter/sort on this endpoint (see run 42351262813), so walk
  # meta.paging.next instead. Early-break once the build version matches.
  PAGE_PATH="/v1/apps/${APP_ID}/builds?limit=50"
  MATCH=""; N_BUILDS=0; PAGES=0
  while [[ -n "$PAGE_PATH" && $PAGES -lt 12 ]]; do
    B=$(asc "-" GET "$PAGE_PATH")
    PAGES=$((PAGES+1))
    N_BUILDS=$((N_BUILDS + $(echo "$B" | jq '.data | length' 2>/dev/null || echo 0)))
    if [[ -n "$BUILD_VERSION" ]]; then
      PAGE_MATCH=$(echo "$B" | jq -c "[.data[] | select(.attributes.version == \"${BUILD_VERSION}\")] | sort_by(.attributes.uploadedDate) | reverse | .[0] // empty" 2>/dev/null)
      if [[ -n "$PAGE_MATCH" && "$PAGE_MATCH" != "null" ]]; then MATCH="$PAGE_MATCH"; fi
    elif [[ $PAGES -eq 1 ]]; then
      MATCH=$(echo "$B" | jq -c '.data | sort_by(.attributes.uploadedDate) | reverse | .[0] // empty' 2>/dev/null)
    fi
    [[ -n "$MATCH" ]] && break
    PAGE_PATH=$(echo "$B" | jq -r '.links.next // empty' 2>/dev/null)
  done
  STATE=$(echo "$MATCH" | jq -r '.attributes.processingState // empty' 2>/dev/null)
  BUILD_ID=$(echo "$MATCH" | jq -r '.id // empty' 2>/dev/null)
  LAST_CODE=$(cat /tmp/asc-last-code 2>/dev/null || echo 000)
  if [[ "$LAST_CODE" == "401" ]]; then
    CONSEC_401=$((CONSEC_401 + 1))
    # Fresh token + still 401 = credentials problem (key revoked, kid/issuer
    # mismatch), not a timing problem - fail fast with a pointed message
    # instead of looping silently for the remaining wait budget.
    if (( CONSEC_401 >= 5 )); then
      echo "::error::ASC still returning 401 after ${CONSEC_401} consecutive polls with freshly-signed JWTs - check APPLE_API_KEY_P8/APPLE_API_KEY_ID/APPLE_API_ISSUER_ID secret pairing and key validity; last response: ${B:0:300}" >&2
      exit 1
    fi
  else
    CONSEC_401=0
  fi
  echo "poll #$i: state=${STATE:-NONE} build_id=${BUILD_ID:-NONE} matches=$N_BUILDS"
  case "$STATE" in
    VALID) echo "build ${BUILD_ID} processed (VALID) after ~$((i/2)) min"; break;;
    FAILED|INVALID) echo "::error::build processing failed: $(echo "$B" | jq -c '.data[0].attributes // .')" >&2; exit 1;;
    "") :;; # build not visible yet (upload still ingesting)
    *) echo "  state=${STATE}";;
  esac
  sleep 30
done
[[ "$STATE" == "VALID" ]] || { echo "::error::build not VALID after ${MAX_WAIT} min (last=${STATE:-none})" >&2; exit 1; }
[[ "$NO_SUBMIT" == "1" ]] && { echo "--no-submit: stopping after processing poll"; exit 0; }


# ---------- fully automated review submission ----------
# Legacy appStoreVersionSubmissions API (the reviewSubmissions resource has
# NO cancel endpoint, and filter[appStoreState]=PREPARE_FOR_SUBMISSION is
# rejected with HTTP 400 on the versions endpoint - run 42355690984 log).
# Flow: locate version -> developer-reject if WAITING_FOR_REVIEW/IN_REVIEW
# -> generic What's New -> bind the fresh build -> submit for review.
WHATS_NEW="${MAS_WHATS_NEW:-Bug fixes and stability improvements.}"

refresh_jwt_if_needed
AVS=$(asc "-" GET "/v1/apps/${APP_ID}/appStoreVersions?limit=50")
AV=$(echo "$AVS" | jq -c "[.data[] | select(.attributes.versionString == \"${VERSION}\")] | .[0] // empty")
AV_ID=$(echo "$AV" | jq -r '.id // empty')
AV_STATE=$(echo "$AV" | jq -r '.attributes.appStoreState // empty')

# developer reject: remove the live submission so the version is editable
if [[ "$AV_STATE" == "WAITING_FOR_REVIEW" || "$AV_STATE" == "IN_REVIEW" ]]; then
  echo "version ${VERSION} is ${AV_STATE} - developer-rejecting to rebind build ${BUILD_VERSION}"
  SUBL=$(asc "-" GET "/v1/appStoreVersions/${AV_ID}/appStoreVersionSubmission")
  SUB_ID=$(echo "$SUBL" | jq -r '.data.id // empty')
  if [[ -n "$SUB_ID" ]]; then
    asc "developer-reject" DELETE "/v1/appStoreVersionSubmissions/${SUB_ID}" > /dev/null
    echo "submission ${SUB_ID} removed (Developer Rejected)"
    for w in 1 2 3 4 5 6 8 10; do
      sleep 10
      refresh_jwt_if_needed
      AVS=$(asc "-" GET "/v1/apps/${APP_ID}/appStoreVersions?limit=50")
      AV_STATE=$(echo "$AVS" | jq -r "[.data[] | select(.id == \"${AV_ID}\")][0].attributes.appStoreState // empty")
      [[ "$AV_STATE" != "WAITING_FOR_REVIEW" && "$AV_STATE" != "IN_REVIEW" ]] && break
    done
  else
    echo "::warning::no live appStoreVersionSubmission found for ${AV_STATE} version - continuing"
  fi
fi

# create the version if it does not exist yet
if [[ -z "$AV_ID" ]]; then
  echo "version ${VERSION} not found on ASC - creating"
  refresh_jwt_if_needed
  # Run 42999275069 evidence: POST /v1/apps/{id}/appStoreVersions is 405
  # METHOD_NOT_ALLOWED - that nested path is GET-only. appStoreVersions
  # createInstance is a TOP-LEVEL POST /v1/appStoreVersions with a required
  # app relationship in the body (ASC schema: relationships.app required).
  AVC=$(asc "create-version" POST "/v1/appStoreVersions" '{"data":{"type":"appStoreVersions","attributes":{"versionString":"'"${VERSION}"'","platform":"'"${PLATFORM}"'"},"relationships":{"app":{"data":{"type":"apps","id":"'"${APP_ID}"'"}}}}}')
  AV_ID=$(echo "$AVC" | jq -r '.data.id // empty')
  # L3 (run 43003971586): 409 "cannot create a new version of the App in
  # the current state" means the single editable slot is occupied by an
  # UNRELATED stuck version (user-confirmed case: 1.3.276 sitting in
  # WAITING_FOR_REVIEW since May). Developer-reject any blocker version,
  # wait for the state to settle, then retry the create once.
  if [[ -z "$AV_ID" ]] && echo "$AVC" | grep -q "cannot create a new version"; then
    echo "editable slot occupied - developer-rejecting blocker versions"
    refresh_jwt_if_needed
    BLOCKERS=$(asc "-" GET "/v1/apps/${APP_ID}/appStoreVersions?limit=50")
    for BID in $(echo "$BLOCKERS" | jq -r --arg v "${VERSION}" '.data[] | select(.attributes.versionString != $v and (.attributes.appStoreState == "WAITING_FOR_REVIEW" or .attributes.appStoreState == "IN_REVIEW")) | .id'); do
      BSUB=$(asc "-" GET "/v1/appStoreVersions/${BID}/appStoreVersionSubmission")
      BSUB_ID=$(echo "$BSUB" | jq -r '.data.id // empty')
      if [[ -n "$BSUB_ID" ]]; then
        asc "developer-reject-blocker" DELETE "/v1/appStoreVersionSubmissions/${BSUB_ID}" > /dev/null
        echo "blocker version ${BID} developer-rejected (submission ${BSUB_ID} removed)"
      fi
    done
    sleep 30
    refresh_jwt_if_needed
    AVC=$(asc "create-version" POST "/v1/appStoreVersions" '{"data":{"type":"appStoreVersions","attributes":{"versionString":"'"${VERSION}"'","platform":"'"${PLATFORM}"'"},"relationships":{"app":{"data":{"type":"apps","id":"'"${APP_ID}"'"}}}}}')
    AV_ID=$(echo "$AVC" | jq -r '.data.id // empty')
  fi
  [[ -n "$AV_ID" ]] || { echo "::error::could not create appStoreVersion ${VERSION}: ${AVC:0:400}" >&2; exit 1; }
fi

# generic What's New across all localizations (MAS_WHATS_NEW overrides)
refresh_jwt_if_needed
LOCS=$(asc "-" GET "/v1/appStoreVersions/${AV_ID}/appStoreVersionLocalizations")
LOC_IDS=$(echo "$LOCS" | jq -r '.data[].id // empty' | tr '\n' ' ')
if [[ -z "${LOC_IDS// }" ]]; then
  echo "::warning::no appStoreVersionLocalizations on version ${VERSION} - skipping What's New"
fi
for LOC_ID in $LOC_IDS; do
  refresh_jwt_if_needed
  # Soft asc() cannot fail its caller: it warns on non-2xx and still ends
  # with `printf '%s' "$resp"`, so it always returns exit 0 - the old
  # `if asc ...` always took the then-branch and printed "whats-new set on"
  # after a failed PATCH (HTTP 409, run 42359007107), leaving the warning
  # branch below dead code (#5234). Judge success by the HTTP status asc()
  # records in /tmp/asc-last-code (same source the poll loop trusts for its
  # 401 streak); `|| true` keeps set -e out of this arm, and "000" covers
  # curl transport failures where no HTTP status exists at all.
  asc "-" PATCH "/v1/appStoreVersionLocalizations/${LOC_ID}" '{"data":{"type":"appStoreVersionLocalizations","id":"'"${LOC_ID}"'","attributes":{"whatsNewText":"'"${WHATS_NEW}"'"}}}' > /dev/null || true
  LOC_PATCH_CODE=$(cat /tmp/asc-last-code 2>/dev/null || echo 000)
  if [[ "$LOC_PATCH_CODE" == 2* ]]; then
    echo "whats-new set on ${LOC_ID}"
  else
    echo "::warning::whats-new patch failed on ${LOC_ID} (HTTP ${LOC_PATCH_CODE}) - continuing"
  fi
done

# bind the fresh build to the version
refresh_jwt_if_needed
asc "bind-build" PATCH "/v1/appStoreVersions/${AV_ID}" '{"data":{"type":"appStoreVersions","id":"'"${AV_ID}"'","relationships":{"build":{"data":{"type":"builds","id":"'"${BUILD_ID}"'"}}}}}' > /dev/null
echo "build ${BUILD_VERSION} (${BUILD_ID}) bound to version ${VERSION}"

# submit for review
refresh_jwt_if_needed
# submit for review via the NEW reviewSubmissions API. Evidence chain
# (runs 42366577692 / 42369905096 / 42372506928 / 42375326486): legacy
# appStoreVersionSubmissions CREATE returned 403 "does not allow 'CREATE'.
# Allowed operation is: DELETE" in EVERY run regardless of version state,
# while legacy DELETE (developer-reject) succeeded and the new-API chain
# (POST reviewSubmissions + items attach + PATCH submitted) succeeded under
# the same key in run 42355690984. The 403 is the key's permission matrix
# on the legacy resource, not a state lock - so submit goes through the
# new API; only the reject path uses the legacy DELETE.
refresh_jwt_if_needed
NRS=$(asc "create-reviewSubmission" POST "/v1/reviewSubmissions" '{"data":{"type":"reviewSubmissions","attributes":{"platform":"'"${PLATFORM}"'"},"relationships":{"app":{"data":{"type":"apps","id":"'"${APP_ID}"'"}}}}}')
NRS_ID=$(echo "$NRS" | jq -r '.data.id // empty')
[[ -n "$NRS_ID" ]] || { echo "::error::reviewSubmissions create failed: ${NRS:0:400}" >&2; exit 1; }
echo "reviewSubmission created: ${NRS_ID}"

refresh_jwt_if_needed
asc "attach-review-item" POST "/v1/reviewSubmissionItems" '{"data":{"type":"reviewSubmissionItems","attributes":{},"relationships":{"reviewSubmission":{"data":{"type":"reviewSubmissions","id":"'"${NRS_ID}"'"}},"appStoreVersion":{"data":{"type":"appStoreVersions","id":"'"${AV_ID}"'"}}}}}' > /dev/null
echo "version ${AV_ID} attached to ${NRS_ID}"

refresh_jwt_if_needed
NSUB=$(asc "submit-reviewSubmission" PATCH "/v1/reviewSubmissions/${NRS_ID}" '{"data":{"type":"reviewSubmissions","id":"'"${NRS_ID}"'","attributes":{"submitted":true}}}')
ST=$(echo "$NSUB" | jq -r '.data.attributes.state // empty')
case "$ST" in
  WAITING_FOR_REVIEW) echo "submitted: version ${VERSION} with build ${BUILD_VERSION} is now WAITING_FOR_REVIEW (submission ${NRS_ID})";;
  *) echo "::error::submit PATCH returned state=${ST:-none}: $(echo "$NSUB" | head -c 400)" >&2; exit 1;;
esac
