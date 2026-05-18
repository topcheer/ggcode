#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
RELAY_DIR="$SCRIPT_DIR"

echo "=== ggcode-relay deploy script ==="

# Check Railway CLI
if ! command -v railway &>/dev/null; then
    echo "Error: railway CLI not installed."
    echo "Install: npm install -g @railway/cli"
    exit 1
fi

# Check logged in
if ! railway whoami &>/dev/null; then
    echo "Logging in to Railway..."
    railway login
fi

cd "$RELAY_DIR"

# Build and test locally first
echo "Building locally..."
go build -o /dev/null .

echo "Local build OK."

# Deploy
echo "Deploying to Railway..."
railway up

echo ""
echo "Deploy complete!"
echo ""
echo "Next steps:"
echo "  1. railway domain (to get the default domain)"
echo "  2. Add custom domain: gateway.ggcode.dev"
echo "     → Railway dashboard > Settings > Domains > Custom Domain"
echo "     → Add CNAME: gateway.ggcode.dev → <your-railway-domain>"
