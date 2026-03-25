#!/usr/bin/env python3
"""Parallel crawl worker: pull from frontier, submit to PageLeft, embed chunks."""

import json
import sys
import time
import urllib.request
from concurrent.futures import ThreadPoolExecutor, as_completed

API = "https://pageleft.cc/api"
SIDECAR = "http://127.0.0.1:8081"
WORKERS = int(sys.argv[1]) if len(sys.argv) > 1 else 4
BATCH = 20


def fetch_frontier(limit: int = BATCH) -> list[dict]:
    req = urllib.request.Request(f"{API}/frontier?limit={limit}")
    with urllib.request.urlopen(req, timeout=30) as resp:
        return json.loads(resp.read())


def submit_page(url: str) -> dict:
    body = json.dumps({"url": url}).encode()
    req = urllib.request.Request(
        f"{API}/contribute/page",
        data=body,
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=60) as resp:
        return json.loads(resp.read())


def embed_chunk(chunk: dict) -> bool:
    # Embed via local sidecar
    body = json.dumps({"text": chunk["text"]}).encode()
    req = urllib.request.Request(
        f"{SIDECAR}/embed",
        data=body,
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=60) as resp:
        result = json.loads(resp.read())

    # Submit embedding
    body = json.dumps({
        "chunk_id": chunk["chunk_id"],
        "embedding": result["embedding"],
    }).encode()
    req = urllib.request.Request(
        f"{API}/contribute/embedding",
        data=body,
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=30) as resp:
        return json.loads(resp.read()).get("accepted", False)


def crawl_one(url: str) -> str:
    try:
        result = submit_page(url)
        if result.get("accepted"):
            return f"OK  {result.get('chunks', 0):2d} chunks  {url[:70]}"
        else:
            return f"SKIP  {result.get('error', '?')[:40]}  {url[:50]}"
    except Exception as e:
        return f"ERR   {str(e)[:40]}  {url[:50]}"


def drain_embed_queue():
    """Embed all pending chunks."""
    total = 0
    empty = 0
    while empty < 3:
        try:
            req = urllib.request.Request(f"{API}/work/embed?limit=10")
            with urllib.request.urlopen(req, timeout=30) as resp:
                data = json.loads(resp.read())
            items = data.get("items") or []
        except Exception:
            time.sleep(5)
            continue

        if not items:
            empty += 1
            time.sleep(2)
            continue
        empty = 0

        for chunk in items:
            try:
                embed_chunk(chunk)
                total += 1
            except Exception:
                time.sleep(2)

    return total


def main():
    print(f"Crawl worker: {WORKERS} threads, batch size {BATCH}", flush=True)
    pages_ok, pages_fail, embeds = 0, 0, 0
    rounds = 0

    while True:
        frontier = fetch_frontier(BATCH)
        if not frontier:
            print("Frontier empty.", flush=True)
            break

        urls = [e["url"] for e in frontier]
        rounds += 1

        # Crawl in parallel
        with ThreadPoolExecutor(max_workers=WORKERS) as pool:
            futures = {pool.submit(crawl_one, url): url for url in urls}
            for f in as_completed(futures):
                result = f.result()
                if result.startswith("OK"):
                    pages_ok += 1
                else:
                    pages_fail += 1
                print(f"  {result}", flush=True)

        # Embed after each batch
        n = drain_embed_queue()
        embeds += n
        if n > 0:
            print(f"  embedded {n} chunks", flush=True)

        print(
            f"[round {rounds}] pages: {pages_ok} ok / {pages_fail} fail, "
            f"embeddings: {embeds}",
            flush=True,
        )

    print(
        f"Done. {pages_ok} pages indexed, {pages_fail} failed, "
        f"{embeds} embeddings.",
        flush=True,
    )


if __name__ == "__main__":
    main()
