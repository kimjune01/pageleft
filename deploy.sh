#!/usr/bin/env bash
set -euo pipefail

SERVER="ubuntu@44.245.126.104"
BINARY="pageleft-linux-arm64"

echo "==> Cross-compiling for linux/arm64..."
GOOS=linux GOARCH=arm64 go build -o "$BINARY" ./cmd/pageleft

echo "==> Uploading to server..."
scp "$BINARY" "$SERVER:/tmp/pageleft"

echo "==> Replacing binary and restarting..."
ssh "$SERVER" 'sudo mv /tmp/pageleft /usr/local/bin/pageleft && sudo chmod +x /usr/local/bin/pageleft && sudo systemctl restart pageleft-server'

echo "==> Waiting for startup..."
sleep 3

echo "==> Smoke test..."
curl -sf 'https://pageleft.cc/api/stats' && echo ""

echo "==> Cleaning up local binary..."
rm -f "$BINARY"

echo "==> Done!"
