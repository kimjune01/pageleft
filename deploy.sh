#!/usr/bin/env bash
set -euo pipefail

SERVER="ubuntu@44.245.126.104"
BINARY="pageleft-linux-arm64"
SHA=$(git rev-parse --short HEAD)
LDFLAGS="-X main.Version=${SHA}"

echo "==> Deploying ${SHA} ($(git log -1 --format='%s'))"

echo "==> Building (native check)..."
go build ./...

echo "==> Cross-compiling for linux/arm64..."
GOOS=linux GOARCH=arm64 go build -ldflags "${LDFLAGS}" -o "$BINARY" ./cmd/pageleft

echo "==> Uploading to server..."
scp "$BINARY" "$SERVER:/tmp/pageleft"

echo "==> Replacing binary and restarting..."
ssh "$SERVER" 'sudo mv /tmp/pageleft /usr/local/bin/pageleft && sudo chmod +x /usr/local/bin/pageleft && sudo systemctl restart pageleft-server'

echo "==> Waiting for startup..."
sleep 3

echo "==> Smoke test..."
STATS=$(curl -sf 'https://pageleft.cc/api/stats')
echo "$STATS" | python3 -c "import sys,json; d=json.load(sys.stdin); v=d.get('version','?'); print(f'  version: {v}  pages: {d[\"pages\"]}  chunks: {d[\"chunks\"]}')"

DEPLOYED=$(echo "$STATS" | python3 -c "import sys,json; print(json.load(sys.stdin).get('version',''))")
if [ "$DEPLOYED" != "$SHA" ]; then
  echo "WARNING: deployed version '${DEPLOYED}' != expected '${SHA}'"
  exit 1
fi

echo "==> Cleaning up local binary..."
rm -f "$BINARY"

echo "==> Done! ${SHA} is live."
