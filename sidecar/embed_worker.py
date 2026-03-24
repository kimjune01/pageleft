#!/usr/bin/env python3
"""Drain the PageLeft embed work queue using HuggingFace Inference API."""

import json
import os
import sys
import time
import urllib.request

API = "https://pageleft.cc/api"
SIDECAR = os.environ.get("SIDECAR_URL", "http://127.0.0.1:8081")
BATCH = 10


def fetch_work(limit: int = BATCH) -> list[dict]:
    url = f"{API}/work/embed?limit={limit}"
    req = urllib.request.Request(url)
    with urllib.request.urlopen(req, timeout=30) as resp:
        data = json.loads(resp.read())
    return data.get("items", [])


def embed(text: str) -> list[float]:
    body = json.dumps({"text": text}).encode()
    req = urllib.request.Request(
        f"{SIDECAR}/embed",
        data=body,
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=60) as resp:
        result = json.loads(resp.read())
    return result["embedding"]


def submit(chunk_id: int, embedding: list[float]) -> bool:
    body = json.dumps({"chunk_id": chunk_id, "embedding": embedding}).encode()
    req = urllib.request.Request(
        f"{API}/contribute/embedding",
        data=body,
        headers={"Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            data = json.loads(resp.read())
        return data.get("accepted", False)
    except Exception as e:
        print(f"  submit failed: {e}", flush=True)
        return False


def main():
    total = 0
    empty_rounds = 0

    while True:
        try:
            items = fetch_work(BATCH)
        except Exception as e:
            print(f"  fetch failed: {e}, retrying in 10s...", flush=True)
            time.sleep(10)
            continue

        if not items:
            empty_rounds += 1
            if empty_rounds >= 3:
                print(f"Queue drained. {total} embeddings submitted.", flush=True)
                break
            print("No items, waiting 5s...", flush=True)
            time.sleep(5)
            continue

        empty_rounds = 0
        for item in items:
            chunk_id = item["chunk_id"]
            text = item["text"]
            try:
                vec = embed(text)
                ok = submit(chunk_id, vec)
                total += 1
                if total % 50 == 0:
                    print(f"  {total} embeddings submitted", flush=True)
            except Exception as e:
                print(f"  chunk {chunk_id} failed: {e}", flush=True)
                time.sleep(5)


if __name__ == "__main__":
    main()
