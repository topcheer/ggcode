#!/usr/bin/env bash

set -euo pipefail

BINARY="${1:?usage: smoke-binary.sh <binary-path>}"

chmod +x "${BINARY}" || true
"${BINARY}" --help >/dev/null
"${BINARY}" completion bash >/dev/null
"${BINARY}" mcp --help >/dev/null
