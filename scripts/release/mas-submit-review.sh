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

asc() { # asc METHOD PATH [JSON_BODY]
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
    resp=$(curl -sS --max-time 60 -w '\n__HTTP__%{http_code}' "$API_BASE$path" -H "Authorization: Bearer $JWT")
  fi
  http_code="${resp##*__HTTP__}"
  resp="${resp%__HTTP__*}"
  printf '%s' "$http_code" > /tmp/asc-last-code
  logd "=== ASC $method $path -> HTTP $http_code ==="
  logd "request-body: ${body:-(none)}"
  logd "response: $resp"
  if [[ "$http_code" != 2* ]]; then
    echo "::warning::ASC $method $path returned HTTP $http_code (non-2xx) - response logged" >&2
  fi
  printf '%s' "$resp"
}

# ---------- locate the app ----------
refresh_jwt_if_needed
APP_JSON=$(asc GET "/v1/apps?filter%5BbundleId%5D=${BUNDLE_ID}")
APP_ID=$(echo "$APP_JSON" | jq -r '.data[0].id // empty')
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
  if [[ -n "$BUILD_VERSION" ]]; then
    B=$(asc GET "/v1/apps/${APP_ID}/builds?filter%5Bversion%5D=${BUILD_VERSION}&sort=-uploadedDate&limit=1")
  else
    B=$(asc GET "/v1/apps/${APP_ID}/builds?sort=-uploadedDate&limit=1")
  fi
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
  STATE=$(echo "$B" | jq -r '.data[0].attributes.processingState // empty' 2>/dev/null)
  BUILD_ID=$(echo "$B" | jq -r '.data[0].id // empty' 2>/dev/null)
  N_BUILDS=$(echo "$B" | jq -r '.data | length' 2>/dev/null)
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

# ---------- open/attach the review submission ----------
# A submission may already exist in PREPARE_FOR_SUBMISSION state (e.g. the
# metadata round created one); reuse it, else create.
refresh_jwt_if_needed
RS=$(asc GET "/v1/reviewSubmissions?filter%5Bapp%5D=${APP_ID}&filter%5Bplatform%5D=${PLATFORM}&filter%5Bstate%5D=PREPARE_FOR_SUBMISSION")
RS_ID=$(echo "$RS" | jq -r '.data[0].id // empty')
if [[ -z "$RS_ID" ]]; then
  refresh_jwt_if_needed
  RS=$(asc POST /v1/reviewSubmissions '{"data":{"type":"reviewSubmissions","attributes":{"platform":"'"${PLATFORM}"'"},"relationships":{"app":{"data":{"type":"apps","id":"'"${APP_ID}"'"}}}}}')
  RS_ID=$(echo "$RS" | jq -r '.data.id // empty')
fi
[[ -n "$RS_ID" ]] || { echo "::error::could not obtain reviewSubmission: ${RS}" >&2; exit 1; }
echo "reviewSubmission: ${RS_ID}"

# ---------- locate the appStoreVersion to submit ----------
refresh_jwt_if_needed
AV=$(asc GET "/v1/apps/${APP_ID}/appStoreVersions?filter%5BappStoreState%5D=PREPARE_FOR_SUBMISSION&filter%5Bplatform%5D=${PLATFORM}")
AV_ID=$(echo "$AV" | jq -r '.data[0].id // empty')
if [[ -z "$AV_ID" ]]; then
  # A version already in review means nothing to submit - that is the EXPECTED
  # conflict class, not a defect. Emit a uniquely greppable line so acceptance
  # logic never conflates it with a real failure (#5139 invariant 2).
  refresh_jwt_if_needed
  INREVIEW=$(asc GET "/v1/apps/${APP_ID}/appStoreVersions?filter%5BappStoreState%5D=WAITING_FOR_REVIEW&filter%5Bplatform%5D=${PLATFORM}")
  if [[ "$(echo "$INREVIEW" | jq -r '.data | length')" -gt 0 ]]; then
    echo "EXPECTED_CONFLICT_ALREADY_IN_REVIEW: version $(echo "$INREVIEW" | jq -r '.data[0].attributes.versionString') is WAITING_FOR_REVIEW - nothing to submit"
    exit 0
  fi
  echo "::error::no PREPARE_FOR_SUBMISSION version and none in review: ${AV}" >&2; exit 1
fi

# ---------- attach + submit ----------
refresh_jwt_if_needed
asc POST "/v1/reviewSubmissions/${RS_ID}/items" \
  '{"data":{"type":"reviewSubmissionItems","attributes":{},"relationships":{"appStoreVersion":{"data":{"type":"appStoreVersions","id":"'"${AV_ID}"'"}}}}}' \
  > /dev/null || true # already-attached items are not an error
refresh_jwt_if_needed
RES=$(asc PATCH "/v1/reviewSubmissions/${RS_ID}" '{"data":{"type":"reviewSubmissions","id":"'"${RS_ID}"'","attributes":{"submitted":true}}}')
ST=$(echo "$RES" | jq -r '.data.attributes.state // empty')
case "$ST" in
  WAITING_FOR_REVIEW) echo "submitted: version ${VERSION} is now WAITING_FOR_REVIEW";;
  *) echo "::warning::submit patch returned state=${ST:-none}: $(echo "$RES" | head -c 400)"; exit 1;;
esac
