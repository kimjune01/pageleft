# Roadmap

What exists, what's next, and what's blocked.

PageLeft is a search engine for ideas that chose to be free. Semantic search over copyleft-licensed pages, with attribution intact. Five pipes (search, crawl, embed, quality, link) write to a shared store via the API. Each pipe maps to the six-stage pipeline from the [parts bin](https://june.kim/the-parts-bin).

## The search pipe

**Goal**: surface the most relevant copyleft-licensed source for a query, preserving the attribution chain that LLMs strip.

| Stage | Status | Parts bin algorithm |
|---|---|---|
| Perceive | ✓ | BGE query prefix (`EmbedQuery()`) |
| Cache | ✓ | In-memory chunk cache with dirty flag |
| Filter | ✓ | `&compiles` parameter |
| Attend | ✓ | Source-diverse DPP (relevance floor 0.7, source penalty 0.3) |
| Consolidate | ✗ | Learning-to-rank — needs eval set |
| Remember | N/A | Stateless (results returned, not stored) |

### Done

- Semantic search over copyleft-licensed pages (CC BY-SA, AGPL, GPL, etc.)
- Per-paragraph chunk embeddings (BGE-small-en-v1.5, 384D) with 50-char minimum to filter nav fragments
- BGE query prefix: `EmbedQuery()` prepends the retrieval instruction BGE expects, widening score discrimination from stdev 0.03 to 0.07
- PageRank from inter-page link graph
- Compilable flag: 2x ranking boost, `&compiles` search filter
- Source-diverse DPP reranker: overfetch 5x, greedy selection using `Similarity * (floor + (1 - floor) * (1 - maxSim))`. Same-domain candidates get a 0.3 similarity penalty, spreading results across sources. Relevance floor at 0.7 prevents diversity from burying highly relevant results. One kernel handles embedding diversity, source diversity, and relevance preservation.
- Snippet highlighting: return the matching chunk text with query terms bolded
- Version tracking: git SHA embedded via ldflags, `pageleft version` command, exposed in `/api/stats`
- In-memory chunk cache: loaded on first search, invalidated when embeddings arrive. Double-checked locking avoids reload races.

### Open problems

- **Consolidate (score balancing)**: final rank is `semantic * (1 + log(1 + rank * n)) * quality * compilable_boost`. The relative weight between semantic similarity and PageRank is implicit, not tuned. No evaluation set exists yet. Prescription: collect implicit feedback (which results get clicked/loaded by agents), fit a learning-to-rank model, update the weight vector. Parts bin: Consolidate × flat → gradient descent or delta-bar-delta.
- **Cache (storage)**: SQLite with JSON arrays for embeddings. In-memory cache works at ~2,500 pages / 100K chunks. Estimated ~10K pages before memory pressure. Candidates: [fogfish/hnsw](https://github.com/fogfish/hnsw) or [TFMV/hnsw](https://github.com/TFMV/hnsw) (both MIT, pure Go). Parts bin: Cache × embedding_space → HNSW index.
- **Compilation mode**: the compilable flag is currently a boolean. Two compilation modes exist: **artifact** (page compiles into code, visualization, simulation) and **judgment** (loading page into context improves agent output on domain-specific tasks). Extend the schema from `compilable bool` to `compilation_mode text` (`artifact`, `judgment`, or null). See [public-domain.md](public-domain.md) for the criteria and [Theory Is Load-Bearing](https://june.kim/theory-is-load-bearing) for the evidence that judgment-mode compilation is real.

## Supporting pipes

Crawl and embed run on federated workers — PageLeft serves the work queue and accepts results but doesn't incur the compute. Quality reviews and link analysis also run externally. Each pipe has its own goal and writes to PageLeft's store via the API.

### Crawl pipe

**Goal**: grow the index with new copyleft-licensed pages.

| Stage | Status | Parts bin algorithm |
|---|---|---|
| Perceive | ✓ | HTML/PDF parsing, Wikipedia REST API, forge README fetch |
| Cache | ✓ | Hash index (URL dedup), SHA-256 (content dedup) |
| Filter | ✓ | Unified `Resolve()` chain + persistent Bloom filter |
| Attend | ✓ | `log(1 + inbound) * (1 + noise)` frontier priority |
| Consolidate | ✗ | EMA convergence gate for recrawl — not started |
| Remember | ✓ | SQLite WAL (pages, chunks, frontier tables) |

**Done**:
- Federated work queues, robots.txt, license detection via `<meta>`, `dc.rights`, and copyleft domain allowlist
- Wikipedia/Wikimedia fetched via REST API. Wiki→wiki excluded from frontier (depth-1 policy)
- Unified filter chain: `crawler.Resolve(url)` runs protocol → blocked domain → Bloom filter → forge → Wikipedia → copyleft domain → allow. One function, one decision. Returns `{Action, License, FetchURL, Reason}`.
- Persistent Bloom filter (`nonpermissive.bloom`): learns domains that are neither copyleft nor public domain. Seeded from block lists, grows at runtime when pages fail license verification. 10K capacity, 0.1% FPR, ~18KB.
- GitHub/Codeberg forge indexing: detect `/{owner}/{repo}`, check license via API (SPDX match) with LICENSE file keyword fallback for NOASSERTION repos. Fetches raw README only.
- Frontier Attend: `inbound` column tracks how many indexed pages link to each frontier URL. Priority = `log(1 + inbound) * (1 + uniform(0, 0.1))`. Overfetch 3x, score, sort, return top N. Stochastic noise shuffles within tiers.
- Parallel crawl worker (`crawl_worker.py`): N threads pull from frontier, submit pages, drain embed queue.
- Domain lists as embedded text files: `blocked_domains.txt` (indexing), `copyleft_domains.txt` (license bypass), `frontier_blocked_domains.txt` (frontier filter).
- Content dedup via `content_hash` (SHA-256). `ON CONFLICT(url) DO NOTHING` on pages, `ON CONFLICT(url) DO UPDATE SET inbound = inbound + 1` on frontier.

**Next: Consolidate** (recrawl + freshness)
- Push stale pages back onto the frontier, sorted by oldest `crawled_at`. Same fetch pipeline, different seed. One mechanism handles freshness, invalidation, and tombstoning:
  - 200 + same `content_hash` → bump `crawled_at`, done
  - 200 + new `content_hash` → re-chunk, null old embeddings, re-enter embed work queue
  - 301/302 → update canonical URL, merge with existing entry at target
  - 4xx (after 2-3 consecutive failures) → null embeddings (drops from search), keep page row
  - 5xx → do nothing, server might be temporarily down
  - Conditional GET (`ETag`, `Last-Modified`) to skip unchanged pages cheaply
- Freshness as ranking signal: multiply rank by decay factor based on `crawled_at` staleness. Recently verified pages rank higher.
- Algorithm: **exponential decay + EMA convergence gate** (parts bin: Consolidate × flat → EMA convergence gate).
  - Each page has a `freshness` score: `exp(-λ * days_since_crawl)`. λ chosen so freshness halves every 90 days.
  - Recrawl priority = `(1 - freshness) * pagerank`. High-authority stale pages recrawl first.
  - Convergence gate: if `content_hash` unchanged across N recrawls, extend interval exponentially (1w → 2w → 1m → 3m). Parts bin: EMA applied to change frequency.

**Other improvements**:
- Domain blocklist expansion: [UT1 blacklists](https://dsi.ut-capitole.fr/blacklists/) (CC BY-SA). Flat lookup, zero inference cost.
- Crawl discovery: accept sitemap.xml and RSS feeds as Perceive sources.
- Bloom filter for URL dedup in `AddToFrontier`: currently does a full `GetPageByURL` query. At scale, a probabilistic cache avoids the DB round-trip. Parts bin: Cache × probabilistic → Bloom filter.

**Open**: license detection misses prose/footer declarations. False negatives are safe; false positives are not.

### Embed pipe

**Goal**: make every page searchable by producing chunk-level embeddings.

| Stage | Status | Parts bin algorithm |
|---|---|---|
| Perceive | ✓ | Work queue (`GET /api/work/embed`) |
| Cache | ✗ | Needs HNSW at scale (100K+ chunks in linear scan) |
| Filter | N/A | All chunks get embedded |
| Attend | N/A | FIFO |
| Consolidate | ✗ | No embedding invalidation on content change |
| Remember | ✓ | Auto page embedding + quality URL hint |

**Done**: unified embed work queue (`{chunk_id, page_id, text}`), auto-chunking on demand. Auto page embedding: when the last chunk embedding arrives, the server averages chunk embeddings into a page-level embedding and returns `page_complete: true` with a `next.quality` hint so workers can immediately submit quality reviews.

**Next**:
- Embedding invalidation handled by recrawl-via-frontier (see crawl pipe). When `content_hash` changes, old chunk embeddings are nulled and new chunks enter the work queue. This is a Remember → Perceive handshake break — stale embeddings violate the contract. See [The Handshake](https://june.kim/the-handshake).

**Later**: multi-model embeddings — accept vectors from different models, normalize to a common similarity space.

### Quality pipe

**Goal**: set the structural floor so pages without substance don't pollute search results.

| Stage | Status | Parts bin algorithm |
|---|---|---|
| Perceive | ✓ | Work queue (`GET /api/work/quality`) |
| Cache | N/A | — |
| Filter | ✓ | Structural heuristic scorer (rank aggregation) |
| Attend | N/A | Scores compound independently |
| Consolidate | ✗ | Time-decayed quality — not started |
| Remember | ✓ | Geometric mean compounding |

Quality is not a page-level score that an LLM assigns. Originality, novelty, and diversity are relational — they depend on what else is in the result set. DPP handles all three at search time. The quality pipe's job is narrower: gate out pages that have no substance at all (photos, nav junk, empty shells). The structural heuristic scorer does this without LLM calls.

**Done**:
- Structural heuristic scorer (`rank-agg-v1`): code fences, equations, citations, headings, domain tier. Rank aggregation produces 0-1 scores.
- Compounding quality scores (geometric mean), random sampling, quality_coverage metric (threshold=1), anonymous contributor leaderboard.
- Worker client (`quality_scorer.py`): pulls from `/api/work/quality`, scores, submits.

**Next**:
- Run quality scorer on new pages — quality_coverage dropped from 99% to 62% after indexing ~1,000 new pages.
- Raise quality_coverage threshold from 1 to 3 when federated quality workers are active.
- Time-decayed quality: a 0.9 review from six months ago weighs less than a 0.7 from today. Parts bin: Consolidate × flat → EMA convergence gate.

**Open**: review gaming — random sampling and dedup prevent naive Sybil attacks. Coordinated attackers can still inflate scores.

**Not planned**: LLM-based quality scoring. Subjective rubrics are exploit surfaces (see [slop-detection](https://june.kim/slop-detection)). Structural heuristics set the floor; DPP handles the rest at search time.

### Link pipe

**Goal**: measure authority so well-linked sources rank above isolated ones.

| Stage | Status | Parts bin algorithm |
|---|---|---|
| Perceive | ✓ | `ExtractLinks()` during page indexing |
| Cache | ✓ | `links` table with UNIQUE constraint |
| Filter | partial | Links to non-indexed pages go to frontier, not links table |
| Attend | N/A | PageRank is global, not per-query |
| Consolidate | ✓ (manual) | Iterative PageRank — `reindex` command |
| Remember | ✓ | `InsertLink()`, `UpdatePageRank()` |

**Done**: link extraction, iterative PageRank (damping=0.85, 50 iterations). Links discovered during page indexing go to the frontier if the target isn't indexed yet.

**Next**: auto-reindex PageRank after batch indexing. Currently requires manual `pageleft reindex` command. Could trigger automatically when page count increases by >5% since last reindex.

**Open**: network bias — PageRank favors well-linked authors. A brilliant page with no inbound links ranks poorly. DPP in embedding space partially compensates — an unlinked page with a novel idea still gets selected for diversity. But the bias is structural.

### Feed reader pipe (not started)

**Goal**: discover fresh copyleft content from feeds instead of manual URL submission.

Uses the [embedding pipe](https://june.kim/embedding-pipe) pattern: Perceive (parse feed entries) → Cache (dedup by URL) → Filter (novelty via distance to existing coverage) → Remember (write to PageLeft's store). See [embedding pipe § Example](https://june.kim/embedding-pipe#example-article-feed).

## Eviction (store-level, not pipe-level)

Pages that don't contribute to provenance or quality decay quietly. Redundant chunks (near-duplicates of higher-quality sources) lose quality over time. Dead pages (consecutive 4xx on recrawl) get their embeddings nulled — they stop appearing in search but stay in the index for dedup. See recrawl-via-frontier in the crawl pipe for the mechanism. Revoked licenses drop quality to zero. Below a threshold, the page is wiped. No graveyard, no archival — if the page mattered, it would have earned quality from reviews or links.

**Embedding eviction for low-quality sources**: when a page's quality drops below a threshold, null its chunk embeddings so it stops appearing in search results. Keep the page row and content_hash in the index — the page is still known, still deduped, still prevents re-crawl. It just doesn't pollute the search space. If quality recovers (new reviews, updated content), re-embed from the work queue. This is compaction, not deletion: the index stays complete, the search stays clean.

## Not planned

- **Web UI**: this is an A2A protocol.
- **Accounts / auth**: contributions are anonymous by design.
- **Moderation dashboard**: quality scores compound. Low-quality pages sink.
- **Popularity-based eviction**: PageLeft keeps what others won't cite.
- **LLM quality axes**: subjective, prompt-relative, and gameable. See [slop-detection](https://june.kim/slop-detection).
