# Roadmap

What exists, what's next, and what's blocked.

PageLeft is a search engine for ideas that chose to be free. Semantic search over copyleft-licensed pages, with attribution intact. Crawl, embed, quality, and link analysis are separate pipes that write to PageLeft's store via the API.

## The search pipe

**Goal**: surface the most relevant copyleft-licensed source for a query, preserving the attribution chain that LLMs strip.

### Done

- Semantic search over copyleft-licensed pages (CC BY-SA, AGPL, GPL, etc.)
- Per-paragraph chunk embeddings (BGE-small-en-v1.5, 384D)
- PageRank from inter-page link graph
- Compilable flag: 2x ranking boost, `&compiles` search filter
- DPP reranker in embedding space: overfetch 5x, greedy selection maximizing `relevance * (1 - maxSim)`. Surfaces pages adjacent to existing results but not redundant — original ideas near known ones, not distant noise. Handles diversity, novelty, and originality at search time with one mechanism.
- Snippet highlighting: return the matching chunk text with query terms bolded

### Open problems

- **Score balancing**: final rank is `semantic * pagerank * quality * compilable_boost`. The relative weight between semantic similarity and PageRank is implicit, not tuned. No evaluation set exists yet. Prescription: Consolidate from the [parts bin](https://june.kim/the-parts-bin) — collect implicit feedback, fit a learning-to-rank model, update the weight vector.
- **Storage**: SQLite with JSON arrays for embeddings. Linear scan works at ~1,600 pages. Estimated ~50K pages before needing ANN. Candidates: [fogfish/hnsw](https://github.com/fogfish/hnsw) or [TFMV/hnsw](https://github.com/TFMV/hnsw) (both MIT, pure Go).
- **Compilation mode**: the compilable flag is currently a boolean (page has a reference implementation or it doesn't). Two compilation modes exist: **artifact** (page compiles into code, visualization, simulation) and **judgment** (loading page into context improves agent output on domain-specific tasks). Extend the schema from `compilable bool` to `compilation_mode text` (`artifact`, `judgment`, or null). Search filter `&compiles` matches either mode. New filter `&compiles=artifact` or `&compiles=judgment` for mode-specific queries. See [public-domain.md](public-domain.md) for the criteria and [Theory Is Load-Bearing](https://june.kim/theory-is-load-bearing) for the evidence that judgment-mode compilation is real.

## Supporting pipes

Crawl and embed run on federated workers — PageLeft serves the work queue and accepts results but doesn't incur the compute. Quality reviews and link analysis also run externally. Each pipe has its own goal and writes to PageLeft's store via the API.

At current scale (~1,600 pages), I am Attend and Consolidate. I review pages, tune ranking weights, and decide crawl priorities by hand. The supporting pipes automate Perceive through Filter. Automating Attend and Consolidate waits for enough data to justify it.

### Crawl pipe

**Goal**: grow the index with new copyleft-licensed pages.

**Done**: federated work queues, robots.txt, license detection via `<meta>` tags.

**Next**:
- Domain blocklist: [UT1 blacklists](https://dsi.ut-capitole.fr/blacklists/) (CC BY-SA). Flat lookup, zero inference cost.
- Recrawl via frontier: push stale pages back onto the frontier, sorted by oldest `crawled_at`. Same fetch pipeline, different seed. One mechanism handles freshness, invalidation, and tombstoning:
  - 200 + same `content_hash` → bump `last_crawled_at`, done
  - 200 + new `content_hash` → re-chunk, null old embeddings, re-enter embed work queue
  - 301/302 → update canonical URL, merge with existing entry at target if one exists
  - 4xx (after 2-3 consecutive failures) → null embeddings (drops from search), keep page row (prevents re-indexing dead URL)
  - 5xx → do nothing, server might be temporarily down
  - Conditional GET (`ETag`, `Last-Modified`) to skip unchanged pages cheaply
- Freshness as ranking signal: multiply rank by decay factor based on `last_crawled_at` staleness. Recently verified pages rank higher. Incentivizes draining the recrawl queue.
- Crawl discovery: accept sitemap.xml and RSS feeds as seed sources.

**Open**: license detection misses prose/footer declarations. False negatives are safe; false positives are not.

### Embed pipe

**Goal**: make every page searchable by producing chunk-level embeddings.

**Done**: unified embed work queue (`{chunk_id, page_id, text}`), auto-chunking on demand. Auto page embedding: when the last chunk embedding arrives via `POST /api/contribute/embedding`, the server averages chunk embeddings into a page-level embedding. No manual backfill needed.

**Next**:
- Embedding invalidation handled by recrawl-via-frontier (see crawl pipe above). When `content_hash` changes, old chunk embeddings are nulled and new chunks enter the work queue. This is a Remember → Perceive handshake break — stale embeddings violate the contract. See [The Handshake](https://june.kim/the-handshake).

**Later**:
- Multi-model embeddings — accept vectors from different models, normalize to a common similarity space.
- Search score discrimination — semantic scores cluster in a narrow band (0.6–0.7), making ranking insensitive to query relevance. Needs an eval set before tuning. Related to score balancing in the search pipe.

### Quality pipe

**Goal**: set the structural floor so pages without substance don't pollute search results.

Quality is not a page-level score that an LLM assigns. Originality, novelty, and diversity are relational — they depend on what else is in the result set. DPP handles all three at search time. The quality pipe's job is narrower: gate out pages that have no substance at all (photos, nav junk, empty shells). The structural heuristic scorer does this without LLM calls.

**Done**:
- Structural heuristic scorer (`rank-agg-v1`): code fences, equations, citations, headings, domain tier. Rank aggregation produces 0-1 scores. Cold start complete — 1,598 pages scored.
- Compounding quality scores (geometric mean), random sampling, quality_coverage metric, anonymous contributor leaderboard.
- Worker client (`quality_scorer.py`): pulls from `/api/work/quality`, scores, submits.

**Next**:
- Raise quality_coverage threshold from 1 to 3 when federated quality workers are active. Currently the structural scorer is the only reviewer, so requiring 3+ reviews makes the metric meaningless. The threshold gates the `/api/stats` coverage number, not the ranking formula.
- Time-decayed quality: a 0.9 review from six months ago weighs less than a 0.7 from today. Decay is a Consolidate operation — read review timestamps, write decayed scores. See [parts bin Consolidate catalog](https://june.kim/the-parts-bin#catalog).

**Open**: review gaming — random sampling and dedup prevent naive Sybil attacks. Coordinated attackers can still inflate scores.

**Not planned**: LLM-based quality scoring. Subjective rubrics are exploit surfaces (see [slop-detection](https://june.kim/slop-detection)). Structural heuristics set the floor; DPP handles the rest at search time.

### Link pipe

**Goal**: measure authority so well-linked sources rank above isolated ones.

**Done**: link extraction, iterative PageRank (damping=0.85, 50 iterations).

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
