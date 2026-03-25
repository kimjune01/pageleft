# Search Ranking Pipeline

How a query becomes ranked results. Three stages: score, pool, rerank.

## 1. Score

Every chunk is compared to the query embedding via cosine similarity. Best chunk per page wins (dedup by page_id). Two scores computed per result:

- **Similarity**: `CosineSim(query, chunk)` — pure semantic match. Used for pool admission and DPP gain.
- **FinalScore**: `Similarity * (1 + log(1 + PageRank * n)) * quality * compilableBoost` — blended rank. Not used for pool admission. Available to DPP but currently unused there too.

The BGE query prefix (`"Represent this sentence for searching relevant passages: "`) is prepended to the query but not to documents. This widens score discrimination from stdev ~0.03 to ~0.07.

## 2. Pool (overfetch)

Sort all scored results by **Similarity descending**. Take the top `limit * 5`.

Why Similarity, not FinalScore? A page about linear algebra with zero PageRank should still be a DPP candidate. PageRank gates authority, not relevance. Pool admission is the Filter stage — it should pass anything semantically relevant. The Attend stage (DPP) decides what to show.

## 3. Rerank (DPP)

Greedy determinantal point process over the pool. At each step, pick the candidate that maximizes:

```
gain = Similarity * (relevanceFloor + (1 - relevanceFloor) * (1 - maxSim))
```

Where `maxSim` is the highest similarity to any already-selected result, computed as:

```
sim = CosineSim(candidate.embedding, selected.embedding)
if sameSource(candidate, selected):
    sim = min(1.0, sim + sourcePenalty)
```

### Parameters

| Parameter | Value | Why |
|---|---|---|
| `overfetchMultiplier` | 5 | Pool = 5x the requested limit. Enough for DPP diversity without scanning the entire index. |
| `relevanceFloor` | 0.7 | Even a near-duplicate of an existing selection keeps 70% of its relevance score. Prevents diversity from burying highly relevant results. |
| `sourcePenalty` | 0.3 | Same-domain candidates look 0.3 more similar to each other. Spreads selections across sources. |

### Behavior

- **First pick**: `maxSim = 0` (nothing selected yet), so `gain = Similarity * 1.0`. Pure relevance wins.
- **Second pick**: diversity kicks in. A near-duplicate of the first pick gets penalized. A result from a different domain with slightly lower relevance can win.
- **Source penalty**: only applies against the *selected set*, not all candidates. The first result from any domain pays no source penalty. The second result from the same domain does.

### Why not FinalScore in DPP?

FinalScore includes PageRank. A homepage with high PageRank but mediocre semantic match would dominate DPP selection over a textbook chapter with perfect semantic match but zero PageRank. DPP's job is to pick the most relevant, diverse set — relevance should be semantic, not authority-weighted.

PageRank's role: it influences which pages are in the index (via crawl priority) and could influence tie-breaking in future. It doesn't influence which results the user sees for a given query.

## Parts bin mapping

| Stage | Search pipe | Parts bin |
|---|---|---|
| Perceive | Query → BGE embedding | Learned encoding (BPE → embedding) |
| Cache | In-memory chunk cache | Hash index (flat, lossless) |
| Filter | Pool admission by Similarity | Threshold filtering (top 5x) |
| Attend | DPP rerank | MMR variant × embedding_space |
| Consolidate | Not implemented | Learning-to-rank (needs eval set) |
| Remember | Stateless | N/A |
