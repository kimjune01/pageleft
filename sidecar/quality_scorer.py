#!/usr/bin/env python3
"""Quality cold start: rank aggregation from structural features + domain tier.

Two features, no models, no weights. Produces a 0-1 quality score per page
and submits via the PageLeft API.
"""

from __future__ import annotations

import json
import re
import sys
import urllib.request
from urllib.parse import urlparse

API = "https://pageleft.cc/api"

# --- Feature 1: Structural score ---

_CODE_FENCE = re.compile(r"```")
_INDENTED_CODE = re.compile(r"^    \S", re.MULTILINE)
_LATEX_INLINE = re.compile(r"\$[^$]+\$")
_LATEX_PAREN = re.compile(r"\\\(.+?\\\)")
_CITATION_RFC = re.compile(r"RFC\s?\d{3,}")
_CITATION_SECTION = re.compile(r"§\s?\d+")
_CITATION_DOI = re.compile(r"DOI:\S+", re.IGNORECASE)
_CITATION_BRACKET = re.compile(r"\[\d+\]")
_EXAMPLE = re.compile(r"^(Example|Proof|Solution|Theorem|Lemma|Corollary)\s*[:.]", re.MULTILINE | re.IGNORECASE)
_HEADING = re.compile(r"^#+\s", re.MULTILINE)
_LIST_ITEM = re.compile(r"^[\-\*]\s", re.MULTILINE)


def structural_score(text: str) -> int:
    """Count structural signals in text. Higher = more implementation-grade."""
    score = 0
    # Code
    if _CODE_FENCE.search(text) or _INDENTED_CODE.search(text):
        score += 1
    # Equations
    if _LATEX_INLINE.search(text) or _LATEX_PAREN.search(text):
        score += 1
    # Citations
    citations = (
        len(_CITATION_RFC.findall(text))
        + len(_CITATION_SECTION.findall(text))
        + len(_CITATION_DOI.findall(text))
        + len(_CITATION_BRACKET.findall(text))
    )
    if citations > 0:
        score += min(citations, 3)  # cap at 3
    # Worked examples
    score += min(len(_EXAMPLE.findall(text)), 3)  # cap at 3
    # Headings
    score += len(_HEADING.findall(text))
    # Lists
    lists = len(_LIST_ITEM.findall(text))
    if lists >= 3:
        score += 1  # at least one substantial list
    return score


# --- Feature 2: Domain tier ---

_TIER_1 = [
    "rfc-editor.org",
    "bartoszmilewski.com",
    "math.dartmouth.edu",
    "gutenberg.org",
]

_TIER_2 = [
    "libretexts.org",
    "nordstrommath.com",
    "uscode.house.gov",
    "ecfr.gov",
    "ntrs.nasa.gov",
    "nist.gov",
    "june.kim",
]

_TIER_3 = [
    "pressbooks.pub",
]


def domain_tier(url: str) -> int:
    """Return 1-4 tier for a URL based on domain."""
    host = urlparse(url).hostname or ""
    host = host.lower().removeprefix("www.")
    for d in _TIER_1:
        if host == d or host.endswith("." + d):
            return 1
    for d in _TIER_2:
        if host == d or host.endswith("." + d):
            return 2
    for d in _TIER_3:
        if host == d or host.endswith("." + d):
            return 3
    return 4


# --- Rank aggregation ---


def rank_aggregate(pages: list[dict]) -> dict[int, float]:
    """Rank pages by structural score and domain tier, return normalized 0-1 scores.

    Each page dict must have: id, structural, tier.
    Returns {page_id: quality_score}.
    """
    n = len(pages)
    if n == 0:
        return {}
    if n == 1:
        return {pages[0]["id"]: 1.0}

    # Rank by structural score (higher = better = lower rank number)
    by_struct = sorted(pages, key=lambda p: -p["structural"])
    struct_rank = {}
    i = 0
    while i < n:
        j = i
        while j < n and by_struct[j]["structural"] == by_struct[i]["structural"]:
            j += 1
        avg_rank = (i + j + 1) / 2  # average rank for ties (1-based)
        for k in range(i, j):
            struct_rank[by_struct[k]["id"]] = avg_rank
        i = j

    # Rank by domain tier (lower tier = better = lower rank number)
    by_tier = sorted(pages, key=lambda p: p["tier"])
    tier_rank = {}
    i = 0
    while i < n:
        j = i
        while j < n and by_tier[j]["tier"] == by_tier[i]["tier"]:
            j += 1
        avg_rank = (i + j + 1) / 2
        for k in range(i, j):
            tier_rank[by_tier[k]["id"]] = avg_rank
        i = j

    # Sum ranks
    rank_sums = {}
    for p in pages:
        rank_sums[p["id"]] = struct_rank[p["id"]] + tier_rank[p["id"]]

    # Normalize to 0-1 (lower sum = higher quality)
    min_sum = min(rank_sums.values())
    max_sum = max(rank_sums.values())
    if max_sum == min_sum:
        return {pid: 1.0 for pid in rank_sums}

    return {
        pid: 1.0 - (s - min_sum) / (max_sum - min_sum)
        for pid, s in rank_sums.items()
    }


# --- API interaction ---


def fetch_all_pages() -> list[dict]:
    """Fetch all pages from the quality work queue."""
    pages = []
    limit = 100
    # The quality endpoint returns random pages, so we fetch many rounds
    # to cover the index. Dedup by page_id.
    seen = set()
    empty_rounds = 0
    while empty_rounds < 5:
        url = f"{API}/work/quality?limit={limit}"
        req = urllib.request.Request(url)
        with urllib.request.urlopen(req, timeout=30) as resp:
            items = json.loads(resp.read())
        new = 0
        for item in items:
            pid = item["page_id"]
            if pid not in seen:
                seen.add(pid)
                pages.append(item)
                new += 1
        if new == 0:
            empty_rounds += 1
        else:
            empty_rounds = 0
        print(f"  fetched {len(pages)} unique pages...", flush=True)
    return pages


def submit_score(page_id: int, score: float) -> bool:
    body = json.dumps({
        "page_id": page_id,
        "score": max(0.01, score),  # avoid zero (multiplicative death)
        "model": "rank-agg-v1",
    }).encode()
    req = urllib.request.Request(
        f"{API}/contribute/quality",
        data=body,
        headers={"Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            data = json.loads(resp.read())
        return data.get("accepted", False)
    except Exception as e:
        print(f"  submit failed for page {page_id}: {e}", flush=True)
        return False


def main():
    print("Fetching pages...", flush=True)
    raw_pages = fetch_all_pages()
    print(f"Got {len(raw_pages)} pages.", flush=True)

    if not raw_pages:
        print("No pages to score.", flush=True)
        sys.exit(0)

    # Compute features
    pages = []
    for p in raw_pages:
        pages.append({
            "id": p["page_id"],
            "url": p["url"],
            "structural": structural_score(p.get("content", "")),
            "tier": domain_tier(p["url"]),
        })

    # Rank aggregate
    scores = rank_aggregate(pages)

    # Submit
    submitted = 0
    for pid, score in scores.items():
        if submit_score(pid, score):
            submitted += 1
            if submitted % 50 == 0:
                print(f"  {submitted} scores submitted", flush=True)

    print(f"Done. {submitted}/{len(scores)} scores submitted.", flush=True)


if __name__ == "__main__":
    main()
