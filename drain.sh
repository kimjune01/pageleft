#!/usr/bin/env bash
set -euo pipefail

# Drain the PageLeft embed queue using the public API.
# No dependencies beyond python3 and curl. No auth, no local writes.
# Read this script before running it: https://github.com/kimjune01/pageleft/blob/main/drain.sh
#
#   ./drain.sh          # run until queue is empty
#   ./drain.sh 500      # stop after 500 embeddings

API="https://pageleft.cc/api"
LIMIT="${1:-0}"

echo "Draining embed queue via $API"
[ "$LIMIT" -gt 0 ] 2>/dev/null && echo "Stopping after $LIMIT embeddings" || true
echo ""

python3 -u - "$API" "$LIMIT" <<'PYTHON'
import json, sys, time, urllib.request

API, LIMIT = sys.argv[1], int(sys.argv[2])
BATCH = 32
total = 0
empty = 0

while True:
    if LIMIT > 0 and total >= LIMIT:
        break

    try:
        with urllib.request.urlopen(f"{API}/work/embed?limit={BATCH}", timeout=30) as r:
            work = json.loads(r.read())
        items = work.get("items", [])
    except Exception as e:
        print(f"  fetch failed: {e}", flush=True)
        time.sleep(5)
        continue

    if not items:
        empty += 1
        if empty >= 3:
            break
        time.sleep(2)
        continue
    empty = 0

    # Batch embed via public API
    texts = [it["text"] for it in items]
    body = json.dumps({"texts": texts}).encode()
    req = urllib.request.Request(
        f"{API}/embed", data=body,
        headers={"Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(req, timeout=120) as r:
            embs = json.loads(r.read())["embeddings"]
    except Exception as e:
        print(f"  embed failed: {e}", flush=True)
        time.sleep(5)
        continue

    # Batch submit
    batch = []
    for item, vec in zip(items, embs):
        batch.append({"chunk_id": item["chunk_id"], "embedding": vec})

    body = json.dumps(batch).encode()
    req = urllib.request.Request(
        f"{API}/contribute/embeddings", data=body,
        headers={"Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            result = json.loads(r.read())
        total += result.get("accepted", 0)
    except Exception as e:
        print(f"  submit failed: {e}", flush=True)
        time.sleep(5)
        continue

    print(f"  {total} embeddings submitted", flush=True)

print(f"Done. {total} embeddings submitted.", flush=True)
PYTHON
