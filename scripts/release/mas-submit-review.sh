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

# ---------- JWT (ES256, the only scheme ASC accepts for API keys) ----------
b64url() { openssl base64 -A | tr '+/' '-_' | tr -d '='; }
NOW=$(date +%s)
HEADER=$(printf '{"alg":"ES256","kid":"%s","typ":"JWT"}' "$KEY_ID" | b64url)
PAYLOAD=$(printf '{"iss":"%s","iat":%d,"exp":%d}' "$ISSUER" "$((NOW))" "$((NOW+1200))" | b64url)
SIG_INPUT="${HEADER}.${PAYLOAD}"
SIG=$(printf '%s' "$SIG_INPUT" | openssl dgst -sha256 -sign "$KEY_PATH" -binary | b64url)
JWT="${SIG_INPUT}.${SIG}"
asc() { # asc METHOD PATH [JSON_BODY]
  local method="$1" path="$2" body="${3:-}"
  if [[ -n "$body" ]]; then
    curl -sS --max-time 60 -X "$method" "$API_BASE$path" \
      -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" -d "$body"
  else
    curl -sS --max-time 60 "$API_BASE$path" -H "Authorization: Bearer $JWT"
  fi
}

# ---------- locate the app ----------
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
for i in $(seq 1 "$((MAX_WAIT*2))"); do
  # Poll keyed on the BUILD number (CFBundleVersion) when given - the short
  # version alone can match a stale build or none at all (#5139 invariant 1).
  if [[ -n "$BUILD_VERSION" ]]; then
    B=$(asc GET "/v1/apps/${APP_ID}/builds?filter%5Bversion%5D=${BUILD_VERSION}&sort=-uploadedDate&limit=1")
  else
    B=$(asc GET "/v1/apps/${APP_ID}/builds?sort=-uploadedDate&limit=1")
  fi
  STATE=$(echo "$B" | jq -r '.data[0].attributes.processingState // empty' 2>/dev/null)
  BUILD_ID=$(echo "$B" | jq -r '.data[0].id // empty' 2>/dev/null)
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
RS=$(asc GET "/v1/reviewSubmissions?filter%5Bapp%5D=${APP_ID}&filter%5Bplatform%5D=${PLATFORM}&filter%5Bstate%5D=PREPARE_FOR_SUBMISSION")
RS_ID=$(echo "$RS" | jq -r '.data[0].id // empty')
if [[ -z "$RS_ID" ]]; then
  RS=$(asc POST /v1/reviewSubmissions '{"data":{"type":"reviewSubmissions","attributes":{"platform":"'"${PLATFORM}"'"},"relationships":{"app":{"data":{"type":"apps","id":"'"${APP_ID}"'"}}}}}')
  RS_ID=$(echo "$RS" | jq -r '.data.id // empty')
fi
[[ -n "$RS_ID" ]] || { echo "::error::could not obtain reviewSubmission: ${RS}" >&2; exit 1; }
echo "reviewSubmission: ${RS_ID}"

# ---------- locate the appStoreVersion to submit ----------
AV=$(asc GET "/v1/apps/${APP_ID}/appStoreVersions?filter%5BappStoreState%5D=PREPARE_FOR_SUBMISSION&filter%5Bplatform%5D=${PLATFORM}")
AV_ID=$(echo "$AV" | jq -r '.data[0].id // empty')
if [[ -z "$AV_ID" ]]; then
  # A version already in review means nothing to submit - that is the EXPECTED
  # conflict class, not a defect. Emit a uniquely greppable line so acceptance
  # logic never conflates it with a real failure (#5139 invariant 2).
  INREVIEW=$(asc GET "/v1/apps/${APP_ID}/appStoreVersions?filter%5BappStoreState%5D=WAITING_FOR_REVIEW&filter%5Bplatform%5D=${PLATFORM}")
  if [[ "$(echo "$INREVIEW" | jq -r '.data | length')" -gt 0 ]]; then
    echo "EXPECTED_CONFLICT_ALREADY_IN_REVIEW: version $(echo "$INREVIEW" | jq -r '.data[0].attributes.versionString') is WAITING_FOR_REVIEW - nothing to submit"
    exit 0
  fi
  echo "::error::no PREPARE_FOR_SUBMISSION version and none in review: ${AV}" >&2; exit 1
fi

# ---------- attach + submit ----------
asc POST "/v1/reviewSubmissions/${RS_ID}/items" \
  '{"data":{"type":"reviewSubmissionItems","attributes":{},"relationships":{"appStoreVersion":{"data":{"type":"appStoreVersions","id":"'"${AV_ID}"'"}}}}}' \
  > /dev/null || true # already-attached items are not an error
RES=$(asc PATCH "/v1/reviewSubmissions/${RS_ID}" '{"data":{"type":"reviewSubmissions","id":"'"${RS_ID}"'","attributes":{"submitted":true}}}')
ST=$(echo "$RES" | jq -r '.data.attributes.state // empty')
case "$ST" in
  WAITING_FOR_REVIEW) echo "submitted: version ${VERSION} is now WAITING_FOR_REVIEW";;
  *) echo "::warning::submit patch returned state=${ST:-none}: $(echo "$RES" | head -c 400)"; exit 1;;
esac
