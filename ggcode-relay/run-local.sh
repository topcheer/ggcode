#!/bin/bash
# Run relay server locally for testing
set -e
cd "$(dirname "$0")"
echo "Starting ggcode-relay on :8080..."
PORT=8080 go run .
